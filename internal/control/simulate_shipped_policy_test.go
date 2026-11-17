package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	controlv1 "aegis/pkg/api/control/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The simulator must agree with the gateway on the shipped policy. The
// dashboard's example requests omit snapshot_version, which the policy's
// decision object references; the simulator fills it in like the gateway.
func TestSimulateShippedPolicyWithoutSnapshotVersion(t *testing.T) {
	src, err := os.ReadFile("../../policies/rego/authz.rego")
	require.NoError(t, err)

	h := setupTestHarness(t)
	defer h.mockDB.Close()
	handler := h.server.Handler()
	cookie, csrfToken := loginOperator(t, handler, "admin", "admin-secret")

	cases := []struct {
		name   string
		input  map[string]interface{}
		allow  bool
		reason string
	}{
		{
			name: "developer reads orders",
			input: map[string]interface{}{
				"principal": map[string]interface{}{"kind": "user", "id": "usr_dev_alice", "roles": []string{"developer"}},
				"resource":  map[string]interface{}{"service": "orders"},
				"request":   map[string]interface{}{"method": "GET", "path": "/api/orders"},
			},
			allow:  true,
			reason: "ALLOWED_DEVELOPER_ORDERS",
		},
		{
			name: "orders workload pays",
			input: map[string]interface{}{
				"principal": map[string]interface{}{"kind": "workload", "id": "spiffe://aegis.local/workload/orders", "roles": []string{}},
				"resource":  map[string]interface{}{"service": "payments"},
				"request":   map[string]interface{}{"method": "POST", "path": "/api/payments"},
			},
			allow:  true,
			reason: "ALLOWED_WORKLOAD_ORDERS_PAYMENTS",
		},
		{
			name: "developer opens admin",
			input: map[string]interface{}{
				"principal": map[string]interface{}{"kind": "user", "id": "usr_dev_alice", "roles": []string{"developer"}},
				"resource":  map[string]interface{}{"service": "admin"},
				"request":   map[string]interface{}{"method": "GET", "path": "/api/admin/users"},
			},
			allow:  false,
			reason: "DENIED_DEVELOPER_ADMIN_FORBIDDEN",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]interface{}{"candidate_rego": string(src), "input_context": tc.input})
			require.NoError(t, err)
			req := httptest.NewRequest(http.MethodPost, "/control/v1/policies/simulate", bytes.NewReader(body))
			req.AddCookie(cookie)
			req.Header.Set("X-CSRF-Token", csrfToken)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			var resp controlv1.PolicySimulationResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			assert.Equal(t, tc.allow, resp.Allow)
			assert.Equal(t, tc.reason, resp.ReasonCode)
		})
	}
}
