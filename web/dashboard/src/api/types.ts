export type AuditEventDecision = 'allow' | 'deny';
export type AuditEventEventType = 'completion' | 'decision';
export type AuditEventPrincipalKind = 'anonymous' | 'user' | 'workload';

export interface AuditEvent {
  event_id: string;
  event_type: AuditEventEventType;
  request_id: string;
  timestamp: string;
  principal_id: string;
  principal_kind?: AuditEventPrincipalKind;
  service_id: string;
  route_id: string;
  http_method?: string;
  request_path?: string;
  decision: AuditEventDecision;
  reason_code: string;
  snapshot_version: number;
  status_code?: number;
  duration_ms?: number;
  roles?: string[];
}

export interface AuditEventListResponse {
  events: AuditEvent[];
  next_cursor?: string;
}

export type GatewayStatusStatus = 'healthy' | 'degraded' | 'partitioned';

export interface GatewayStatus {
  gateway_id: string;
  active_version: number;
  status: GatewayStatusStatus;
  connected_at: string;
  last_heartbeat_at?: string;
  lease_expires_at?: string;
  error_message?: string;
}

export interface GatewayListResponse {
  gateways: GatewayStatus[];
}

export interface PolicyDraft {
  policy_id: string;
  name: string;
  source_rego: string;
  created_at?: string;
  updated_at?: string;
}

export interface PolicyCreateRequest {
  policy_id: string;
  name: string;
  source_rego: string;
}

export interface PolicyPublishRequest {
  comment?: string;
}

export interface PolicyPublishResponse {
  snapshot_version: number;
  published_at: string;
  signing_key_id: string;
}

export interface PolicyRollbackRequest {
  target_version_id: string;
  comment?: string;
}

export interface PolicySimulationRequest {
  candidate_rego?: string;
  input_context: Record<string, unknown>;
}

export interface PolicySimulationResponse {
  allow: boolean;
  reason_code: string;
  duration_us: number;
  diagnostics?: Record<string, unknown>;
}

export interface PolicyValidationRequest {
  candidate_rego?: string;
}

export interface PolicyValidationResponse {
  valid: boolean;
  test_count: number;
  passed_count: number;
  failed_count: number;
  errors?: string[];
}

export interface PolicyVersion {
  version_id: string;
  policy_id: string;
  snapshot_version: number;
  source_rego: string;
  published_by: string;
  published_at: string;
}

export interface PolicyVersionListResponse {
  versions: PolicyVersion[];
  next_cursor?: string;
}

export type RouteHttpMethod = 'GET' | 'POST' | 'PUT' | 'DELETE' | 'PATCH' | 'HEAD' | 'OPTIONS';

export interface Route {
  route_id: string;
  service_id: string;
  http_method: RouteHttpMethod;
  path_template: string;
  upstream_url: string;
  upstream_spiffe_id?: string;
  requires_workload_mtls?: boolean;
  rate_limit_rps?: number;
  rate_limit_burst?: number;
  timeout_ms?: number;
  created_at?: string;
  updated_at?: string;
}

export interface RouteListResponse {
  routes: Route[];
  next_cursor?: string;
}

export type QuarantineRecordStatus = 'active' | 'revoked' | 'expired';

export interface QuarantineRecord {
  principal_id: string;
  reason: string;
  status: QuarantineRecordStatus;
  quarantined_at: string;
  expires_at?: string;
  actor?: string;
}

export interface QuarantineRequest {
  reason: string;
  actor?: string;
  expires_at?: string;
  ttl_seconds?: number;
}

export interface UnquarantineRequest {
  reason?: string;
}

export interface UnquarantineResponse {
  principal_id: string;
  unquarantined_at: string;
}

export interface ErrorResponse {
  code: string;
  message: string;
  request_id?: string;
  timestamp?: string;
  details?: Record<string, unknown>;
}

export interface SessionInfo {
  username: string;
  role: 'sec-ops' | 'auditor' | 'viewer';
  csrf_token: string;
  expires_at: string;
}
