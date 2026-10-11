package storage

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"aegis/migrations"

	"github.com/pashagolub/pgxmock/v4"
	"github.com/stretchr/testify/require"
)

func TestSecurityStateRepo_UpsertQuarantineIdempotent(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	repo := NewSecurityStateRepo(mock)
	exp := time.Now().Add(time.Hour)
	for i := 0; i < 2; i++ {
		mock.ExpectExec(regexp.QuoteMeta("ON CONFLICT (principal_id) DO UPDATE")).
			WithArgs("p1", "why", "admin", exp).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))
		require.NoError(t, repo.UpsertQuarantine(context.Background(), QuarantineRecord{PrincipalID: "p1", Reason: "why", CreatedBy: "admin", ExpiresAt: exp}))
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSecurityStateRepo_UpsertErrorsWrapped(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	repo := NewSecurityStateRepo(mock)
	boom := errors.New("boom")
	mock.ExpectExec("INSERT INTO principal_quarantines").WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).WillReturnError(boom)
	err = repo.UpsertQuarantine(context.Background(), QuarantineRecord{PrincipalID: "p1"})
	require.ErrorIs(t, err, boom)
	require.Contains(t, err.Error(), `"p1"`)

	mock.ExpectExec(regexp.QuoteMeta("ON CONFLICT (jti) DO UPDATE")).WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).WillReturnError(boom)
	err = repo.UpsertRevokedToken(context.Background(), RevokedTokenRecord{JTI: "j1"})
	require.ErrorIs(t, err, boom)
	require.Contains(t, err.Error(), `"j1"`)
}

func TestSecurityStateRepo_ListUnexpired(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	repo := NewSecurityStateRepo(mock)
	now := time.Now()

	mock.ExpectQuery(regexp.QuoteMeta("expires_at > NOW()")).
		WillReturnRows(mock.NewRows([]string{"principal_id", "reason", "created_by", "created_at", "expires_at"}).
			AddRow("p1", "r", "a", now, now.Add(time.Hour)))
	qs, err := repo.ListUnexpiredQuarantines(context.Background())
	require.NoError(t, err)
	require.Len(t, qs, 1)
	require.Equal(t, "p1", qs[0].PrincipalID)

	mock.ExpectQuery(regexp.QuoteMeta("expires_at > NOW()")).
		WillReturnRows(mock.NewRows([]string{"jti", "reason", "created_by", "created_at", "expires_at"}))
	ts, err := repo.ListUnexpiredRevokedTokens(context.Background())
	require.NoError(t, err)
	require.NotNil(t, ts)
	require.Empty(t, ts)

	mock.ExpectQuery("FROM principal_quarantines").WillReturnError(errors.New("down"))
	_, err = repo.ListUnexpiredQuarantines(context.Background())
	require.Error(t, err)
}

func TestSecurityStateRepo_DeleteSemantics(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	repo := NewSecurityStateRepo(mock)

	mock.ExpectExec("DELETE FROM principal_quarantines WHERE principal_id").WithArgs("missing").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	require.NoError(t, repo.DeleteQuarantine(context.Background(), "missing"))

	mock.ExpectExec("DELETE FROM principal_quarantines WHERE expires_at").WillReturnResult(pgxmock.NewResult("DELETE", 2))
	mock.ExpectExec("DELETE FROM revoked_tokens WHERE expires_at").WillReturnResult(pgxmock.NewResult("DELETE", 3))
	n, err := repo.DeleteExpired(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 5, n)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSecurityStateMigrationFile(t *testing.T) {
	b, err := migrations.FS.ReadFile("000005_create_security_state.sql")
	require.NoError(t, err)
	s := string(b)
	require.Contains(t, s, "-- +goose Up")
	require.Contains(t, s, "-- +goose Down")
	require.Contains(t, s, "principal_quarantines")
	require.Contains(t, s, "revoked_tokens")
	down := s[strings.Index(s, "-- +goose Down"):]
	require.Less(t, strings.Index(down, "revoked_tokens"), strings.Index(down, "principal_quarantines"))
}
