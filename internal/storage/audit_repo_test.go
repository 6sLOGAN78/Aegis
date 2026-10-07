package storage

import (
	"context"
	"regexp"
	"testing"
	"time"

	controlv1 "aegis/pkg/api/control/v1"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuditRepo_ListAuditEvents_Default24h(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	repo := NewAuditRepo(mock)
	ctx := context.Background()

	eventID := uuid.New()
	reqID := uuid.New()
	now := time.Now().UTC().Truncate(time.Millisecond)
	method := "GET"
	path := "/api/orders"
	durMs := 1.25
	status := 200
	kind := "user"
	roles := []byte(`["orders.read"]`)

	rows := mock.NewRows([]string{
		"event_id", "event_type", "request_id", "timestamp", "principal_id", "principal_kind",
		"roles", "service_id", "route_id", "http_method", "request_path", "decision", "reason_code",
		"snapshot_version", "http_status", "duration_ms",
	}).AddRow(
		eventID, "decision", reqID, now, "user-123", &kind,
		roles, "orders", "route-orders-get", &method, &path, "allow", "RULE_ALLOW",
		int64(1), &status, &durMs,
	)

	// Expect query with default 24h bounding ($1, $2) and default limit 51 ($3)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT event_id, event_type, request_id, timestamp, principal_id, principal_kind")).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), 51).
		WillReturnRows(rows)

	res, err := repo.ListAuditEvents(ctx, ListAuditEventsParams{})
	require.NoError(t, err)
	require.Len(t, res.Events, 1)
	assert.Nil(t, res.NextCursor)

	event := res.Events[0]
	assert.Equal(t, eventID, event.EventId)
	assert.Equal(t, controlv1.AuditEventEventType("decision"), event.EventType)
	assert.Equal(t, reqID, event.RequestId)
	assert.Equal(t, "user-123", event.PrincipalId)
	assert.Equal(t, controlv1.AuditEventDecisionAllow, event.Decision)
	assert.Equal(t, "RULE_ALLOW", event.ReasonCode)
	assert.Equal(t, 200, *event.StatusCode)
	assert.Equal(t, 1, *event.DurationMs)
	require.NotNil(t, event.Roles)
	assert.Equal(t, []string{"orders.read"}, *event.Roles)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAuditRepo_ListAuditEvents_WithFiltersAndPagination(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	repo := NewAuditRepo(mock)
	ctx := context.Background()

	id1 := uuid.New()
	id2 := uuid.New()
	id3 := uuid.New()
	reqID := uuid.New()
	t1 := time.Now().UTC().Truncate(time.Millisecond)
	t2 := t1.Add(-time.Minute)
	t3 := t1.Add(-2 * time.Minute)
	method := "POST"
	path := "/api/orders"
	durMs := 2.5
	status := 403
	kind := "user"
	roles := []byte(`[]`)

	// Limit is 2; mock returns 3 rows to test NextCursor generation
	rows := mock.NewRows([]string{
		"event_id", "event_type", "request_id", "timestamp", "principal_id", "principal_kind",
		"roles", "service_id", "route_id", "http_method", "request_path", "decision", "reason_code",
		"snapshot_version", "http_status", "duration_ms",
	}).
		AddRow(id1, "decision", reqID, t1, "user-bad", &kind, roles, "orders", "route-orders-post", &method, &path, "deny", "POLICY_DENY", int64(1), &status, &durMs).
		AddRow(id2, "decision", reqID, t2, "user-bad", &kind, roles, "orders", "route-orders-post", &method, &path, "deny", "POLICY_DENY", int64(1), &status, &durMs).
		AddRow(id3, "decision", reqID, t3, "user-bad", &kind, roles, "orders", "route-orders-post", &method, &path, "deny", "POLICY_DENY", int64(1), &status, &durMs)

	principalID := "user-bad"
	serviceID := "orders"
	decision := "deny"
	limit := 2

	mock.ExpectQuery(regexp.QuoteMeta("SELECT event_id, event_type, request_id, timestamp, principal_id, principal_kind")).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), principalID, serviceID, decision, 3).
		WillReturnRows(rows)

	res, err := repo.ListAuditEvents(ctx, ListAuditEventsParams{
		PrincipalID: &principalID,
		ServiceID:   &serviceID,
		Decision:    &decision,
		Limit:       &limit,
	})
	require.NoError(t, err)
	// 3 rows returned for limit 2 -> pop 1, len = 2, nextCursor present
	require.Len(t, res.Events, 2)
	require.NotNil(t, res.NextCursor)

	parsedTime, parsedID, err := decodeCursor(*res.NextCursor)
	require.NoError(t, err)
	assert.Equal(t, t2, parsedTime)
	assert.Equal(t, id2.String(), parsedID)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAuditRepo_GetAuditEvent(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	repo := NewAuditRepo(mock)
	ctx := context.Background()

	eventID := uuid.New()
	reqID := uuid.New()
	now := time.Now().UTC().Truncate(time.Millisecond)
	method := "DELETE"
	path := "/api/orders/1"
	durMs := 3.0
	status := 204
	kind := "workload"
	roles := []byte(`["admin"]`)

	// 1. Found
	rows := mock.NewRows([]string{
		"event_id", "event_type", "request_id", "timestamp", "principal_id", "principal_kind",
		"roles", "service_id", "route_id", "http_method", "request_path", "decision", "reason_code",
		"snapshot_version", "http_status", "duration_ms",
	}).AddRow(
		eventID, "decision", reqID, now, "service-cart", &kind,
		roles, "orders", "route-orders-delete", &method, &path, "allow", "RULE_ALLOW",
		int64(2), &status, &durMs,
	)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT event_id, event_type, request_id, timestamp, principal_id, principal_kind")).
		WithArgs(eventID).
		WillReturnRows(rows)

	event, err := repo.GetAuditEvent(ctx, eventID.String())
	require.NoError(t, err)
	assert.Equal(t, eventID, event.EventId)
	assert.Equal(t, "service-cart", event.PrincipalId)

	// 2. Not found
	notFoundID := uuid.New()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT event_id, event_type, request_id, timestamp, principal_id, principal_kind")).
		WithArgs(notFoundID).
		WillReturnError(pgx.ErrNoRows)

	_, err = repo.GetAuditEvent(ctx, notFoundID.String())
	assert.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAuditRepo_PolicyRepo_CRUD(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	repo := NewPolicyRepo(mock)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	// 1. CreateDraft
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO policy_drafts")).
		WithArgs("draft-1", "aegis.authz", "policy.rego", "package aegis.authz\ndefault allow = false", "draft", "admin").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = repo.CreateDraft(ctx, &PolicyDraftRecord{
		DraftID:     "draft-1",
		PackageName: "aegis.authz",
		ModuleName:  "policy.rego",
		SourceRego:  "package aegis.authz\ndefault allow = false",
		Status:      "draft",
		CreatedBy:   "admin",
	})
	require.NoError(t, err)

	// 2. GetDraft
	rows := mock.NewRows([]string{
		"draft_id", "package_name", "module_name", "source_rego", "status", "created_by", "created_at", "updated_at",
	}).AddRow("draft-1", "aegis.authz", "policy.rego", "package aegis.authz\ndefault allow = false", "draft", "admin", now, now)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT draft_id, package_name, module_name, source_rego, status, created_by, created_at, updated_at FROM policy_drafts WHERE draft_id = $1")).
		WithArgs("draft-1").
		WillReturnRows(rows)

	draft, err := repo.GetDraft(ctx, "draft-1")
	require.NoError(t, err)
	assert.Equal(t, "draft-1", draft.DraftID)
	assert.Equal(t, "admin", draft.CreatedBy)

	// 3. UpdateDraft
	mock.ExpectExec(regexp.QuoteMeta("UPDATE policy_drafts")).
		WithArgs("draft-1", "package aegis.authz\ndefault allow = true", "policy.rego", "aegis.authz", "validated").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	err = repo.UpdateDraft(ctx, &PolicyDraftRecord{
		DraftID:     "draft-1",
		PackageName: "aegis.authz",
		ModuleName:  "policy.rego",
		SourceRego:  "package aegis.authz\ndefault allow = true",
		Status:      "validated",
	})
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}
