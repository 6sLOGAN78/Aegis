package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrNotFound is returned when a requested record does not exist in the database.
var ErrNotFound = errors.New("record not found")

// ServiceRecord represents a backend microservice registered in the control plane.
type ServiceRecord struct {
	ID          string          `json:"id"`
	Environment string          `json:"environment"`
	Enabled     bool            `json:"enabled"`
	Metadata    json.RawMessage `json:"metadata"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// RouteRecord represents an ingress routing rule mapping paths to upstream backends.
type RouteRecord struct {
	RouteID              string    `json:"route_id"`
	ServiceID            string    `json:"service_id"`
	HTTPMethod           string    `json:"http_method"`
	PathTemplate         string    `json:"path_template"`
	UpstreamURL          string    `json:"upstream_url"`
	UpstreamSPIFFEID     string    `json:"upstream_spiffe_id"`
	RateLimitRPS         int       `json:"rate_limit_rps"`
	RateLimitBurst       int       `json:"rate_limit_burst"`
	TimeoutMS            int       `json:"timeout_ms"`
	RequiresWorkloadMTLS bool      `json:"requires_workload_mtls"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// RouteRepo handles database persistence for services and route catalog definitions.
type RouteRepo struct {
	db DBPool
}

// NewRouteRepo constructs a new RouteRepo backed by a database pool.
func NewRouteRepo(db DBPool) *RouteRepo {
	return &RouteRepo{db: db}
}

const upsertServiceQuery = `
INSERT INTO services (id, environment, enabled, metadata, created_at, updated_at)
VALUES ($1, $2, $3, $4, NOW(), NOW())
ON CONFLICT (id) DO UPDATE SET
    environment = EXCLUDED.environment,
    enabled = EXCLUDED.enabled,
    metadata = EXCLUDED.metadata,
    updated_at = NOW();`

// UpsertService creates or updates a service entry.
func (r *RouteRepo) UpsertService(ctx context.Context, svc ServiceRecord) error {
	if svc.Environment == "" {
		svc.Environment = "production"
	}
	if len(svc.Metadata) == 0 {
		svc.Metadata = json.RawMessage("{}")
	}

	_, err := r.db.Exec(ctx, upsertServiceQuery, svc.ID, svc.Environment, svc.Enabled, svc.Metadata)
	if err != nil {
		return fmt.Errorf("failed to upsert service %q: %w", svc.ID, err)
	}
	return nil
}

const getServiceQuery = `
SELECT id, environment, enabled, metadata, created_at, updated_at
FROM services
WHERE id = $1;`

// GetService retrieves a service by ID, returning ErrNotFound if it does not exist.
func (r *RouteRepo) GetService(ctx context.Context, id string) (*ServiceRecord, error) {
	var svc ServiceRecord
	err := r.db.QueryRow(ctx, getServiceQuery, id).Scan(
		&svc.ID,
		&svc.Environment,
		&svc.Enabled,
		&svc.Metadata,
		&svc.CreatedAt,
		&svc.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get service %q: %w", id, err)
	}
	return &svc, nil
}

const upsertRouteQuery = `
INSERT INTO routes (
    route_id, service_id, http_method, path_template, upstream_url, upstream_spiffe_id,
    rate_limit_rps, rate_limit_burst, timeout_ms, requires_workload_mtls, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW(), NOW())
ON CONFLICT (route_id) DO UPDATE SET
    service_id = EXCLUDED.service_id,
    http_method = EXCLUDED.http_method,
    path_template = EXCLUDED.path_template,
    upstream_url = EXCLUDED.upstream_url,
    upstream_spiffe_id = EXCLUDED.upstream_spiffe_id,
    rate_limit_rps = EXCLUDED.rate_limit_rps,
    rate_limit_burst = EXCLUDED.rate_limit_burst,
    timeout_ms = EXCLUDED.timeout_ms,
    requires_workload_mtls = EXCLUDED.requires_workload_mtls,
    updated_at = NOW();`

// UpsertRoute creates or updates a route entry.
func (r *RouteRepo) UpsertRoute(ctx context.Context, route RouteRecord) error {
	if route.RateLimitRPS <= 0 {
		route.RateLimitRPS = 100
	}
	if route.RateLimitBurst <= 0 {
		route.RateLimitBurst = 200
	}
	if route.TimeoutMS <= 0 {
		route.TimeoutMS = 5000
	}

	_, err := r.db.Exec(ctx, upsertRouteQuery,
		route.RouteID,
		route.ServiceID,
		route.HTTPMethod,
		route.PathTemplate,
		route.UpstreamURL,
		route.UpstreamSPIFFEID,
		route.RateLimitRPS,
		route.RateLimitBurst,
		route.TimeoutMS,
		route.RequiresWorkloadMTLS,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert route %q: %w", route.RouteID, err)
	}
	return nil
}

const getRouteQuery = `
SELECT route_id, service_id, http_method, path_template, upstream_url, upstream_spiffe_id,
       rate_limit_rps, rate_limit_burst, timeout_ms, requires_workload_mtls, created_at, updated_at
FROM routes
WHERE route_id = $1;`

// GetRoute retrieves a single route by route ID, returning ErrNotFound if absent.
func (r *RouteRepo) GetRoute(ctx context.Context, routeID string) (*RouteRecord, error) {
	var route RouteRecord
	err := r.db.QueryRow(ctx, getRouteQuery, routeID).Scan(
		&route.RouteID,
		&route.ServiceID,
		&route.HTTPMethod,
		&route.PathTemplate,
		&route.UpstreamURL,
		&route.UpstreamSPIFFEID,
		&route.RateLimitRPS,
		&route.RateLimitBurst,
		&route.TimeoutMS,
		&route.RequiresWorkloadMTLS,
		&route.CreatedAt,
		&route.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get route %q: %w", routeID, err)
	}
	return &route, nil
}

const listRoutesQuery = `
SELECT route_id, service_id, http_method, path_template, upstream_url, upstream_spiffe_id,
       rate_limit_rps, rate_limit_burst, timeout_ms, requires_workload_mtls, created_at, updated_at
FROM routes
ORDER BY route_id ASC;`

// ListRoutes returns all configured routes ordered by route ID ascending.
func (r *RouteRepo) ListRoutes(ctx context.Context) ([]RouteRecord, error) {
	rows, err := r.db.Query(ctx, listRoutesQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to query routes: %w", err)
	}
	defer rows.Close()

	routes := make([]RouteRecord, 0)
	for rows.Next() {
		var route RouteRecord
		if err := rows.Scan(
			&route.RouteID,
			&route.ServiceID,
			&route.HTTPMethod,
			&route.PathTemplate,
			&route.UpstreamURL,
			&route.UpstreamSPIFFEID,
			&route.RateLimitRPS,
			&route.RateLimitBurst,
			&route.TimeoutMS,
			&route.RequiresWorkloadMTLS,
			&route.CreatedAt,
			&route.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan route row: %w", err)
		}
		routes = append(routes, route)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return routes, nil
}

const deleteRouteQuery = `DELETE FROM routes WHERE route_id = $1;`

// DeleteRoute removes a route by its route ID, returning ErrNotFound if it didn't exist.
func (r *RouteRepo) DeleteRoute(ctx context.Context, routeID string) error {
	cmdTag, err := r.db.Exec(ctx, deleteRouteQuery, routeID)
	if err != nil {
		return fmt.Errorf("failed to delete route %q: %w", routeID, err)
	}
	if cmdTag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
