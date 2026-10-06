package aegis.authz_test

import rego.v1
import data.aegis.authz

# 1. Developer can read orders -> ALLOW
test_developer_can_read_orders if {
    input_data := {
        "principal": {"id": "user:ayush", "kind": "user", "roles": ["developer"]},
        "resource": {"service": "orders", "route": "orders.list"},
        "request": {"method": "GET", "path": "/api/orders"},
        "context": {"risk_score": 0, "risk_state": "available"},
        "snapshot_version": 1,
    }
    authz.allow with input as input_data
    authz.reason_code == "ALLOWED_DEVELOPER_ORDERS" with input as input_data
}

# 2. Developer can read payments -> ALLOW
test_developer_can_read_payments if {
    input_data := {
        "principal": {"id": "user:ayush", "kind": "user", "roles": ["developer"]},
        "resource": {"service": "payments", "route": "payments.get"},
        "request": {"method": "GET", "path": "/api/payments"},
        "context": {"risk_score": 0, "risk_state": "available"},
        "snapshot_version": 1,
    }
    authz.allow with input as input_data
    authz.reason_code == "ALLOWED_DEVELOPER_PAYMENTS" with input as input_data
}

# 3. Developer accessing admin -> DENY
test_developer_cannot_access_admin if {
    input_data := {
        "principal": {"id": "user:ayush", "kind": "user", "roles": ["developer"]},
        "resource": {"service": "admin", "route": "admin.users.list"},
        "request": {"method": "GET", "path": "/api/admin/users"},
        "context": {"risk_score": 0, "risk_state": "available"},
        "snapshot_version": 1,
    }
    not authz.allow with input as input_data
    authz.reason_code == "DENIED_DEVELOPER_ADMIN_FORBIDDEN" with input as input_data
}

# 4. Finance can create payments -> ALLOW
test_finance_can_create_payments if {
    input_data := {
        "principal": {"id": "user:finance1", "kind": "user", "roles": ["finance"]},
        "resource": {"service": "payments", "route": "payments.create"},
        "request": {"method": "POST", "path": "/api/payments"},
        "context": {"risk_score": 0, "risk_state": "available"},
        "snapshot_version": 1,
    }
    authz.allow with input as input_data
    authz.reason_code == "ALLOWED_FINANCE_PAYMENTS" with input as input_data
}

# 5. Application admin can access admin users -> ALLOW
test_admin_can_access_admin if {
    input_data := {
        "principal": {"id": "user:admin1", "kind": "user", "roles": ["application-admin"]},
        "resource": {"service": "admin", "route": "admin.users.list"},
        "request": {"method": "GET", "path": "/api/admin/users"},
        "context": {"risk_score": 0, "risk_state": "available"},
        "snapshot_version": 1,
    }
    authz.allow with input as input_data
    authz.reason_code == "ALLOWED_ADMIN_USERS" with input as input_data
}

# 6. Workload orders can create payments -> ALLOW
test_workload_orders_can_post_payments if {
    input_data := {
        "principal": {"id": "spiffe://aegis.local/workload/orders", "kind": "workload", "roles": []},
        "resource": {"service": "payments", "route": "payments.create"},
        "request": {"method": "POST", "path": "/api/payments"},
        "context": {"risk_score": 0, "risk_state": "available"},
        "snapshot_version": 1,
    }
    authz.allow with input as input_data
    authz.reason_code == "ALLOWED_WORKLOAD_ORDERS_PAYMENTS" with input as input_data
}

# 7. Workload orders cannot access admin -> DENY
test_workload_orders_cannot_access_admin if {
    input_data := {
        "principal": {"id": "spiffe://aegis.local/workload/orders", "kind": "workload", "roles": []},
        "resource": {"service": "admin", "route": "admin.users.list"},
        "request": {"method": "GET", "path": "/api/admin/users"},
        "context": {"risk_score": 0, "risk_state": "available"},
        "snapshot_version": 1,
    }
    not authz.allow with input as input_data
    authz.reason_code == "DENIED_WORKLOAD_ADMIN_FORBIDDEN" with input as input_data
}

# 8. Missing identity -> DENY
test_missing_identity_denied if {
    input_data := {
        "principal": {"id": "", "kind": "anonymous", "roles": []},
        "resource": {"service": "orders", "route": "orders.list"},
        "request": {"method": "GET", "path": "/api/orders"},
        "context": {"risk_score": 0, "risk_state": "available"},
        "snapshot_version": 1,
    }
    not authz.allow with input as input_data
    authz.reason_code == "DENIED_INVALID_PRINCIPAL" with input as input_data
}
