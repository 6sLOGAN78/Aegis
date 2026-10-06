package aegis.authz

import rego.v1

# Invariant 1: Strict default deny. No match = deny.
default allow := false
default reason_code := "DENIED_DEFAULT"
default matched_rule_id := "default_deny"

# -------------------------------------------------------------------------
# Rule 1: Developer reading orders
# -------------------------------------------------------------------------
allow if {
    input.principal.kind == "user"
    "developer" in input.principal.roles
    input.resource.service == "orders"
    input.request.method == "GET"
}

reason_code := "ALLOWED_DEVELOPER_ORDERS" if {
    input.principal.kind == "user"
    "developer" in input.principal.roles
    input.resource.service == "orders"
    input.request.method == "GET"
}

matched_rule_id := "developer_orders_read" if {
    input.principal.kind == "user"
    "developer" in input.principal.roles
    input.resource.service == "orders"
    input.request.method == "GET"
}

# -------------------------------------------------------------------------
# Rule 2: Developer reading payments
# -------------------------------------------------------------------------
allow if {
    input.principal.kind == "user"
    "developer" in input.principal.roles
    input.resource.service == "payments"
    input.request.method == "GET"
}

reason_code := "ALLOWED_DEVELOPER_PAYMENTS" if {
    input.principal.kind == "user"
    "developer" in input.principal.roles
    input.resource.service == "payments"
    input.request.method == "GET"
}

matched_rule_id := "developer_payments_read" if {
    input.principal.kind == "user"
    "developer" in input.principal.roles
    input.resource.service == "payments"
    input.request.method == "GET"
}

# -------------------------------------------------------------------------
# Rule 3: Finance creating or reading payments
# -------------------------------------------------------------------------
allow if {
    input.principal.kind == "user"
    "finance" in input.principal.roles
    input.resource.service == "payments"
    input.request.method in ["GET", "POST"]
}

reason_code := "ALLOWED_FINANCE_PAYMENTS" if {
    input.principal.kind == "user"
    "finance" in input.principal.roles
    input.resource.service == "payments"
    input.request.method in ["GET", "POST"]
}

matched_rule_id := "finance_payments" if {
    input.principal.kind == "user"
    "finance" in input.principal.roles
    input.resource.service == "payments"
    input.request.method in ["GET", "POST"]
}

# -------------------------------------------------------------------------
# Rule 4: Application Admin managing admin users
# -------------------------------------------------------------------------
allow if {
    input.principal.kind == "user"
    "application-admin" in input.principal.roles
    input.resource.service == "admin"
}

reason_code := "ALLOWED_ADMIN_USERS" if {
    input.principal.kind == "user"
    "application-admin" in input.principal.roles
    input.resource.service == "admin"
}

matched_rule_id := "admin_users" if {
    input.principal.kind == "user"
    "application-admin" in input.principal.roles
    input.resource.service == "admin"
}

# -------------------------------------------------------------------------
# Rule 5: Workload 'orders' invoking payments mutation
# -------------------------------------------------------------------------
allow if {
    input.principal.kind == "workload"
    input.principal.id == "spiffe://aegis.local/workload/orders"
    input.resource.service == "payments"
    input.request.method == "POST"
}

reason_code := "ALLOWED_WORKLOAD_ORDERS_PAYMENTS" if {
    input.principal.kind == "workload"
    input.principal.id == "spiffe://aegis.local/workload/orders"
    input.resource.service == "payments"
    input.request.method == "POST"
}

matched_rule_id := "workload_orders_payments" if {
    input.principal.kind == "workload"
    input.principal.id == "spiffe://aegis.local/workload/orders"
    input.resource.service == "payments"
    input.request.method == "POST"
}

# -------------------------------------------------------------------------
# Explicit Denials & Reason Codes
# -------------------------------------------------------------------------
reason_code := "DENIED_DEVELOPER_ADMIN_FORBIDDEN" if {
    input.principal.kind == "user"
    "developer" in input.principal.roles
    input.resource.service == "admin"
}

reason_code := "DENIED_WORKLOAD_ADMIN_FORBIDDEN" if {
    input.principal.kind == "workload"
    input.principal.id == "spiffe://aegis.local/workload/orders"
    input.resource.service == "admin"
}

invalid_principal if not input.principal.id
invalid_principal if input.principal.id == ""

reason_code := "DENIED_INVALID_PRINCIPAL" if {
    invalid_principal
}

# -------------------------------------------------------------------------
# Structured Decision Output Object
# -------------------------------------------------------------------------
decision := {
    "allow": allow,
    "reason_code": reason_code,
    "snapshot_version": input.snapshot_version,
}
