package audit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJTI_NormalizeClipsAndStripsNUL(t *testing.T) {
	e := CompletionEvent{JTI: strings.Repeat("j", 200)}
	e.Normalize()
	assert.Len(t, e.JTI, 128)
	n := CompletionEvent{JTI: "a\x00b"}
	n.Normalize()
	assert.Equal(t, "ab", n.JTI)
}

func TestJTI_JSONCompat(t *testing.T) {
	var old CompletionEvent
	require.NoError(t, json.Unmarshal([]byte(`{"event_id":"e","timestamp":"2026-01-01T00:00:00Z"}`), &old))
	assert.Equal(t, "", old.JTI)

	b, err := json.Marshal(CompletionEvent{JTI: "abc"})
	require.NoError(t, err)
	var back CompletionEvent
	require.NoError(t, json.Unmarshal(b, &back))
	assert.Equal(t, "abc", back.JTI)

	empty, err := json.Marshal(CompletionEvent{})
	require.NoError(t, err)
	assert.NotContains(t, string(empty), "jti")
}

func TestJTI_SetJTIPropagatesToEvent(t *testing.T) {
	var nilAC *AuditContext
	nilAC.SetJTI("x") // no-op, must not panic

	_, ac := WithAuditContext(context.Background(), "req-jti")
	ac.SetJTI("jti-1")
	assert.Equal(t, "jti-1", ac.ToCompletionEvent("GET", "10.0.0.5", 1).JTI)
}

func TestJTI_WorkerInsertArg(t *testing.T) {
	cases := []struct {
		name string
		jti  string
		want any
	}{
		{"set", "abc", "abc"},
		{"empty", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.want
			ev := &CompletionEvent{EventID: uuid.NewString(), RequestID: uuid.NewString(), Timestamp: time.Now().UTC(), JTI: tc.jti}
			runWorkerOnce(t, []*CompletionEvent{ev}, map[int]any{20: argMatcher(func(v any) bool { return v == want })})
		})
	}
}
