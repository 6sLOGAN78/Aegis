package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"

	snapshotv1 "aegis/pkg/api/snapshot/v1"
)

// ErrRouteNotFound indicates that no configured route matched the incoming request.
var ErrRouteNotFound = errors.New("no matching upstream route found")

// Route defines an upstream routing entry from the route catalog.
type Route struct {
	RouteID              string                      `json:"route_id"`
	ServiceID            string                      `json:"service_id"`
	HTTPMethod           string                      `json:"http_method"`
	PathTemplate         string                      `json:"path_template"`
	UpstreamURL          string                      `json:"upstream_url"`
	UpstreamSpiffeId     string                      `json:"upstream_spiffe_id,omitempty"`
	RateLimit            *snapshotv1.RateLimitPolicy `json:"rate_limit,omitempty"`
	Timeout              *snapshotv1.TimeoutPolicy   `json:"timeout,omitempty"`
	RequiresWorkloadMTLS bool                        `json:"requires_workload_mtls,omitempty"`
	parsedURL            *url.URL
}

// ParsedURL returns the parsed URL for the upstream route.
func (r *Route) ParsedURL() *url.URL {
	return r.parsedURL
}

// Router maintains the static catalog of upstream service routes.
type Router struct {
	routes []Route
}

// NewRouter constructs a Router from a slice of Routes.
func NewRouter(routes []Route) (*Router, error) {
	schemeOverride := os.Getenv("AEGIS_UPSTREAM_SCHEME")
	for i := range routes {
		u, err := url.Parse(routes[i].UpstreamURL)
		if err != nil {
			return nil, fmt.Errorf("invalid upstream URL %q for route %s: %w", routes[i].UpstreamURL, routes[i].RouteID, err)
		}
		if schemeOverride != "" {
			u.Scheme = schemeOverride
		}
		routes[i].parsedURL = u
	}
	return &Router{routes: routes}, nil
}

// AddProtobufRoute validates, parses, and appends a Protobuf RouteDefinition to the router.
func (r *Router) AddProtobufRoute(route *snapshotv1.RouteDefinition) error {
	if route == nil {
		return errors.New("cannot add nil route definition")
	}

	u, err := url.Parse(route.UpstreamUrl)
	if err != nil {
		return fmt.Errorf("invalid upstream URL %q for route %s: %w", route.UpstreamUrl, route.RouteId, err)
	}

	if schemeOverride := os.Getenv("AEGIS_UPSTREAM_SCHEME"); schemeOverride != "" {
		u.Scheme = schemeOverride
	}

	r.routes = append(r.routes, Route{
		RouteID:              route.RouteId,
		ServiceID:            route.ServiceId,
		HTTPMethod:           route.HttpMethod,
		PathTemplate:         route.PathTemplate,
		UpstreamURL:          route.UpstreamUrl,
		UpstreamSpiffeId:     route.UpstreamSpiffeId,
		RateLimit:            route.RateLimit,
		Timeout:              route.Timeout,
		RequiresWorkloadMTLS: route.RequiresWorkloadMtls,
		parsedURL:            u,
	})

	return nil
}

// NewRouterFromProtobuf constructs a Router from a slice of Protobuf RouteDefinitions.
func NewRouterFromProtobuf(routes []*snapshotv1.RouteDefinition) (*Router, error) {
	r := &Router{routes: make([]Route, 0, len(routes))}
	for _, route := range routes {
		if err := r.AddProtobufRoute(route); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// NewRouterFromJSON loads route catalog definitions from a JSON file.
func NewRouterFromJSON(filePath string) (*Router, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read routes file %s: %w", filePath, err)
	}

	var routes []Route
	if err := json.Unmarshal(data, &routes); err != nil {
		return nil, fmt.Errorf("failed to parse routes JSON: %w", err)
	}

	return NewRouter(routes)
}

// Match deterministically matches an HTTP method and canonical path against configured routes.
// The client Host header is completely ignored for upstream selection (Invariant 6).
func (r *Router) Match(method, path string) (*Route, error) {
	for i := range r.routes {
		if r.routes[i].HTTPMethod == method && r.routes[i].PathTemplate == path {
			return &r.routes[i], nil
		}
	}
	return nil, ErrRouteNotFound
}

// Routes returns all registered routes.
func (r *Router) Routes() []Route {
	return r.routes
}
