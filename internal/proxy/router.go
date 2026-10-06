package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
)

// ErrRouteNotFound indicates that no configured route matched the incoming request.
var ErrRouteNotFound = errors.New("no matching upstream route found")

// Route defines an upstream routing entry from the route catalog.
type Route struct {
	RouteID      string   `json:"route_id"`
	ServiceID    string   `json:"service_id"`
	HTTPMethod   string   `json:"http_method"`
	PathTemplate string   `json:"path_template"`
	UpstreamURL  string   `json:"upstream_url"`
	parsedURL    *url.URL
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
