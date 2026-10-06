package security

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"aegis/internal/policy"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRBACMatrix(t *testing.T) {
	policyPath := filepath.Join("..", "..", "policies", "rego", "authz.rego")
	regoBytes, err := os.ReadFile(policyPath)
	require.NoError(t, err, "failed to read policies/rego/authz.rego")

	engine, err := policy.NewEngine(context.Background(), string(regoBytes))
	require.NoError(t, err, "failed to initialize OPA engine")

	testCases := []struct {
		name           string
		principal      policy.PrincipalInput
		resource       policy.ResourceInput
		request        policy.RequestInput
		expectedAllow  bool
		expectedReason string
	}{
		// 1. Developer Role
		{
			name: "developer read orders -> allowed",
			principal: policy.PrincipalInput{
				ID:    "usr_dev_01",
				Kind:  "user",
				Roles: []string{"developer"},
			},
			resource: policy.ResourceInput{
				Service: "orders",
				Route:   "orders.list",
			},
			request: policy.RequestInput{
				Method: "GET",
				Path:   "/api/orders",
			},
			expectedAllow:  true,
			expectedReason: "ALLOWED_DEVELOPER_ORDERS",
		},
		{
			name: "developer read payments -> allowed",
			principal: policy.PrincipalInput{
				ID:    "usr_dev_01",
				Kind:  "user",
				Roles: []string{"developer"},
			},
			resource: policy.ResourceInput{
				Service: "payments",
				Route:   "payments.get",
			},
			request: policy.RequestInput{
				Method: "GET",
				Path:   "/api/payments",
			},
			expectedAllow:  true,
			expectedReason: "ALLOWED_DEVELOPER_PAYMENTS",
		},
		{
			name: "developer create payments -> denied default",
			principal: policy.PrincipalInput{
				ID:    "usr_dev_01",
				Kind:  "user",
				Roles: []string{"developer"},
			},
			resource: policy.ResourceInput{
				Service: "payments",
				Route:   "payments.create",
			},
			request: policy.RequestInput{
				Method: "POST",
				Path:   "/api/payments",
			},
			expectedAllow:  false,
			expectedReason: "DENIED_DEFAULT",
		},
		{
			name: "developer read admin users -> denied developer admin forbidden",
			principal: policy.PrincipalInput{
				ID:    "usr_dev_01",
				Kind:  "user",
				Roles: []string{"developer"},
			},
			resource: policy.ResourceInput{
				Service: "admin",
				Route:   "admin.users.list",
			},
			request: policy.RequestInput{
				Method: "GET",
				Path:   "/api/admin/users",
			},
			expectedAllow:  false,
			expectedReason: "DENIED_DEVELOPER_ADMIN_FORBIDDEN",
		},
		{
			name: "developer create admin users -> denied developer admin forbidden",
			principal: policy.PrincipalInput{
				ID:    "usr_dev_01",
				Kind:  "user",
				Roles: []string{"developer"},
			},
			resource: policy.ResourceInput{
				Service: "admin",
				Route:   "admin.users.manage",
			},
			request: policy.RequestInput{
				Method: "POST",
				Path:   "/api/admin/users",
			},
			expectedAllow:  false,
			expectedReason: "DENIED_DEVELOPER_ADMIN_FORBIDDEN",
		},

		// 2. Finance Role
		{
			name: "finance read payments -> allowed",
			principal: policy.PrincipalInput{
				ID:    "usr_fin_01",
				Kind:  "user",
				Roles: []string{"finance"},
			},
			resource: policy.ResourceInput{
				Service: "payments",
				Route:   "payments.get",
			},
			request: policy.RequestInput{
				Method: "GET",
				Path:   "/api/payments",
			},
			expectedAllow:  true,
			expectedReason: "ALLOWED_FINANCE_PAYMENTS",
		},
		{
			name: "finance create payments -> allowed",
			principal: policy.PrincipalInput{
				ID:    "usr_fin_01",
				Kind:  "user",
				Roles: []string{"finance"},
			},
			resource: policy.ResourceInput{
				Service: "payments",
				Route:   "payments.create",
			},
			request: policy.RequestInput{
				Method: "POST",
				Path:   "/api/payments",
			},
			expectedAllow:  true,
			expectedReason: "ALLOWED_FINANCE_PAYMENTS",
		},
		{
			name: "finance read orders -> denied default",
			principal: policy.PrincipalInput{
				ID:    "usr_fin_01",
				Kind:  "user",
				Roles: []string{"finance"},
			},
			resource: policy.ResourceInput{
				Service: "orders",
				Route:   "orders.list",
			},
			request: policy.RequestInput{
				Method: "GET",
				Path:   "/api/orders",
			},
			expectedAllow:  false,
			expectedReason: "DENIED_DEFAULT",
		},
		{
			name: "finance read admin users -> denied default",
			principal: policy.PrincipalInput{
				ID:    "usr_fin_01",
				Kind:  "user",
				Roles: []string{"finance"},
			},
			resource: policy.ResourceInput{
				Service: "admin",
				Route:   "admin.users.list",
			},
			request: policy.RequestInput{
				Method: "GET",
				Path:   "/api/admin/users",
			},
			expectedAllow:  false,
			expectedReason: "DENIED_DEFAULT",
		},

		// 3. Application Admin Role
		{
			name: "admin read admin users -> allowed",
			principal: policy.PrincipalInput{
				ID:    "usr_admin_01",
				Kind:  "user",
				Roles: []string{"application-admin"},
			},
			resource: policy.ResourceInput{
				Service: "admin",
				Route:   "admin.users.list",
			},
			request: policy.RequestInput{
				Method: "GET",
				Path:   "/api/admin/users",
			},
			expectedAllow:  true,
			expectedReason: "ALLOWED_ADMIN_USERS",
		},
		{
			name: "admin manage admin users -> allowed",
			principal: policy.PrincipalInput{
				ID:    "usr_admin_01",
				Kind:  "user",
				Roles: []string{"application-admin"},
			},
			resource: policy.ResourceInput{
				Service: "admin",
				Route:   "admin.users.manage",
			},
			request: policy.RequestInput{
				Method: "POST",
				Path:   "/api/admin/users",
			},
			expectedAllow:  true,
			expectedReason: "ALLOWED_ADMIN_USERS",
		},

		// 4. Workload Identity
		{
			name: "workload orders create payments -> allowed",
			principal: policy.PrincipalInput{
				ID:    "spiffe://aegis.local/workload/orders",
				Kind:  "workload",
				Roles: []string{},
			},
			resource: policy.ResourceInput{
				Service: "payments",
				Route:   "payments.create",
			},
			request: policy.RequestInput{
				Method: "POST",
				Path:   "/api/payments",
			},
			expectedAllow:  true,
			expectedReason: "ALLOWED_WORKLOAD_ORDERS_PAYMENTS",
		},
		{
			name: "workload orders read admin -> denied workload admin forbidden",
			principal: policy.PrincipalInput{
				ID:    "spiffe://aegis.local/workload/orders",
				Kind:  "workload",
				Roles: []string{},
			},
			resource: policy.ResourceInput{
				Service: "admin",
				Route:   "admin.users.list",
			},
			request: policy.RequestInput{
				Method: "GET",
				Path:   "/api/admin/users",
			},
			expectedAllow:  false,
			expectedReason: "DENIED_WORKLOAD_ADMIN_FORBIDDEN",
		},

		// 5. Invalid / Anonymous Identity
		{
			name: "anonymous principal missing ID -> denied invalid principal",
			principal: policy.PrincipalInput{
				ID:    "",
				Kind:  "anonymous",
				Roles: []string{},
			},
			resource: policy.ResourceInput{
				Service: "orders",
				Route:   "orders.list",
			},
			request: policy.RequestInput{
				Method: "GET",
				Path:   "/api/orders",
			},
			expectedAllow:  false,
			expectedReason: "DENIED_INVALID_PRINCIPAL",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			input := policy.PolicyInput{
				Principal: tc.principal,
				Resource:  tc.resource,
				Request:   tc.request,
				Context: policy.ContextInput{
					RiskScore: 0,
					RiskState: "available",
				},
				SnapshotVersion: 1,
			}

			decision, err := engine.Evaluate(context.Background(), input)
			require.NoError(t, err, "evaluation must not return error for valid inputs")
			assert.Equal(t, tc.expectedAllow, decision.Allow, "decision allow mismatch")
			assert.Equal(t, tc.expectedReason, decision.ReasonCode, "decision reason code mismatch")
			assert.Equal(t, int64(1), decision.SnapshotVersion, "snapshot version mismatch")
		})
	}
}
