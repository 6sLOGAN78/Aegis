package storage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	controlv1 "aegis/pkg/api/control/v1"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// ListAuditEventsParams defines query parameters for filtered audit log searches.
type ListAuditEventsParams struct {
	PrincipalID   *string
	ServiceID     *string
	Decision      *string
	FromTimestamp *time.Time
	ToTimestamp   *time.Time
	Cursor        *string
	Limit         *int
}

// AuditEventListResult contains paginated audit events and an optional next cursor.
type AuditEventListResult struct {
	Events     []controlv1.AuditEvent
	NextCursor *string
}

// AuditRepo handles indexed querying of partitioned audit_events in PostgreSQL.
type AuditRepo struct {
	db DBPool
}

// NewAuditRepo constructs an AuditRepo backed by a database pool.
func NewAuditRepo(db DBPool) *AuditRepo {
	return &AuditRepo{db: db}
}

// encodeCursor serializes a timestamp and event UUID into an opaque base64 cursor.
func encodeCursor(t time.Time, id string) string {
	raw := fmt.Sprintf("%s|%s", t.Format(time.RFC3339Nano), id)
	return base64.URLEncoding.EncodeToString([]byte(raw))
}

// decodeCursor deserializes an opaque base64 cursor into a timestamp and event UUID.
func decodeCursor(c string) (time.Time, string, error) {
	bytes, err := base64.URLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("invalid cursor encoding: %w", err)
	}
	parts := strings.Split(string(bytes), "|")
	if len(parts) != 2 {
		return time.Time{}, "", errors.New("invalid cursor format")
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", fmt.Errorf("invalid cursor timestamp: %w", err)
	}
	return t, parts[1], nil
}

// ListAuditEvents queries partitioned audit_events with mandatory time bounds, filters, and cursor pagination.
func (r *AuditRepo) ListAuditEvents(ctx context.Context, params ListAuditEventsParams) (*AuditEventListResult, error) {
	// 1. Constrain query window: default from = now - 24h to leverage active partition B-tree indexes
	from := time.Now().Add(-24 * time.Hour)
	if params.FromTimestamp != nil && !params.FromTimestamp.IsZero() {
		from = *params.FromTimestamp
	}

	to := time.Now()
	if params.ToTimestamp != nil && !params.ToTimestamp.IsZero() {
		to = *params.ToTimestamp
	}

	limit := 50
	if params.Limit != nil && *params.Limit > 0 {
		limit = *params.Limit
		if limit > 200 {
			limit = 200
		}
	}

	query := `
SELECT event_id, event_type, request_id, timestamp, principal_id, principal_kind,
       roles, service_id, route_id, http_method, request_path, decision, reason_code,
       snapshot_version, http_status, duration_ms
FROM audit_events
WHERE timestamp >= $1 AND timestamp <= $2`

	args := []any{from, to}
	argIdx := 3

	if params.PrincipalID != nil && *params.PrincipalID != "" {
		query += fmt.Sprintf(" AND principal_id = $%d", argIdx)
		args = append(args, *params.PrincipalID)
		argIdx++
	}

	if params.ServiceID != nil && *params.ServiceID != "" {
		query += fmt.Sprintf(" AND service_id = $%d", argIdx)
		args = append(args, *params.ServiceID)
		argIdx++
	}

	if params.Decision != nil && *params.Decision != "" {
		query += fmt.Sprintf(" AND decision = $%d", argIdx)
		args = append(args, *params.Decision)
		argIdx++
	}

	if params.Cursor != nil && *params.Cursor != "" {
		curTime, curID, err := decodeCursor(*params.Cursor)
		if err == nil {
			query += fmt.Sprintf(" AND (timestamp, event_id) < ($%d, $%d)", argIdx, argIdx+1)
			args = append(args, curTime, curID)
			argIdx += 2
		}
	}

	query += fmt.Sprintf(" ORDER BY timestamp DESC, event_id DESC LIMIT $%d", argIdx)
	args = append(args, limit+1)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query audit events: %w", err)
	}
	defer rows.Close()

	events := make([]controlv1.AuditEvent, 0, limit)
	for rows.Next() {
		var (
			eventIDUUID, reqIDUUID openapi_types.UUID
			eventType, principalID, serviceID, routeID, decision, reason string
			principalKind, method, path                                  *string
			rolesBytes                                                   []byte
			snapVer                                                      int64
			httpStatus                                                   *int
			durationMs                                                   *float64
			ts                                                           time.Time
		)

		if err := rows.Scan(
			&eventIDUUID,
			&eventType,
			&reqIDUUID,
			&ts,
			&principalID,
			&principalKind,
			&rolesBytes,
			&serviceID,
			&routeID,
			&method,
			&path,
			&decision,
			&reason,
			&snapVer,
			&httpStatus,
			&durationMs,
		); err != nil {
			return nil, fmt.Errorf("failed to scan audit event: %w", err)
		}

		var durInt *int
		if durationMs != nil {
			v := int(*durationMs)
			durInt = &v
		}

		var rolesSlice *[]string
		if len(rolesBytes) > 0 {
			var parsedRoles []string
			if err := json.Unmarshal(rolesBytes, &parsedRoles); err == nil {
				rolesSlice = &parsedRoles
			}
		}

		var pk *controlv1.AuditEventPrincipalKind
		if principalKind != nil {
			k := controlv1.AuditEventPrincipalKind(*principalKind)
			pk = &k
		}

		events = append(events, controlv1.AuditEvent{
			EventId:         eventIDUUID,
			EventType:       controlv1.AuditEventEventType(eventType),
			RequestId:       reqIDUUID,
			Timestamp:       ts,
			PrincipalId:     principalID,
			PrincipalKind:   pk,
			Roles:           rolesSlice,
			ServiceId:       serviceID,
			RouteId:         routeID,
			HttpMethod:      method,
			RequestPath:     path,
			Decision:        controlv1.AuditEventDecision(decision),
			ReasonCode:      reason,
			SnapshotVersion: snapVer,
			StatusCode:      httpStatus,
			DurationMs:      durInt,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	var nextCursor *string
	if len(events) > limit {
		// Pop the extra row used to probe for next page
		lastEvent := events[limit-1]
		c := encodeCursor(lastEvent.Timestamp, lastEvent.EventId.String())
		nextCursor = &c
		events = events[:limit]
	}

	return &AuditEventListResult{
		Events:     events,
		NextCursor: nextCursor,
	}, nil
}

const getAuditEventQuery = `
SELECT event_id, event_type, request_id, timestamp, principal_id, principal_kind,
       roles, service_id, route_id, http_method, request_path, decision, reason_code,
       snapshot_version, http_status, duration_ms
FROM audit_events
WHERE event_id = $1
LIMIT 1;`

// GetAuditEvent retrieves a single audit event by its event ID.
func (r *AuditRepo) GetAuditEvent(ctx context.Context, eventID string) (*controlv1.AuditEvent, error) {
	var (
		eventIDUUID, reqIDUUID openapi_types.UUID
		eventType, principalID, serviceID, routeID, decision, reason string
		principalKind, method, path                                  *string
		rolesBytes                                                   []byte
		snapVer                                                      int64
		httpStatus                                                   *int
		durationMs                                                   *float64
		ts                                                           time.Time
	)

	// Validate UUID format
	parsedUUID, err := uuid.Parse(eventID)
	if err != nil {
		return nil, ErrNotFound
	}

	err = r.db.QueryRow(ctx, getAuditEventQuery, parsedUUID).Scan(
		&eventIDUUID,
		&eventType,
		&reqIDUUID,
		&ts,
		&principalID,
		&principalKind,
		&rolesBytes,
		&serviceID,
		&routeID,
		&method,
		&path,
		&decision,
		&reason,
		&snapVer,
		&httpStatus,
		&durationMs,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get audit event %q: %w", eventID, err)
	}

	var durInt *int
	if durationMs != nil {
		v := int(*durationMs)
		durInt = &v
	}

	var rolesSlice *[]string
	if len(rolesBytes) > 0 {
		var parsedRoles []string
		if err := json.Unmarshal(rolesBytes, &parsedRoles); err == nil {
			rolesSlice = &parsedRoles
		}
	}

	var pk *controlv1.AuditEventPrincipalKind
	if principalKind != nil {
		k := controlv1.AuditEventPrincipalKind(*principalKind)
		pk = &k
	}

	return &controlv1.AuditEvent{
		EventId:         eventIDUUID,
		EventType:       controlv1.AuditEventEventType(eventType),
		RequestId:       reqIDUUID,
		Timestamp:       ts,
		PrincipalId:     principalID,
		PrincipalKind:   pk,
		Roles:           rolesSlice,
		ServiceId:       serviceID,
		RouteId:         routeID,
		HttpMethod:      method,
		RequestPath:     path,
		Decision:        controlv1.AuditEventDecision(decision),
		ReasonCode:      reason,
		SnapshotVersion: snapVer,
		StatusCode:      httpStatus,
		DurationMs:      durInt,
	}, nil
}
