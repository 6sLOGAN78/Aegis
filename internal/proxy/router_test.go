package proxy

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRouteResolution(t *testing.T) {
	router, err := NewRouterFromJSON("../../policies/data/routes.json")
	require.NoError(t, err)
	require.NotEmpty(t, router.Routes())

	t.Run("Matches GET /api/orders to orders.list", func(t *testing.T) {
		route, err := router.Match("GET", "/api/orders")
		require.NoError(t, err)
		require.Equal(t, "orders.list", route.RouteID)
		require.Equal(t, "orders", route.ServiceID)
		require.NotNil(t, route.ParsedURL())
		require.Equal(t, "orders:8081", route.ParsedURL().Host)
	})

	t.Run("Matches POST /api/payments to payments.create", func(t *testing.T) {
		route, err := router.Match("POST", "/api/payments")
		require.NoError(t, err)
		require.Equal(t, "payments.create", route.RouteID)
		require.Equal(t, "payments", route.ServiceID)
		require.NotNil(t, route.ParsedURL())
		require.Equal(t, "payments:8082", route.ParsedURL().Host)
	})

	t.Run("Matches GET /api/payments to payments.get", func(t *testing.T) {
		route, err := router.Match("GET", "/api/payments")
		require.NoError(t, err)
		require.Equal(t, "payments.get", route.RouteID)
	})

	t.Run("Matches GET /api/admin/users to admin.users.list", func(t *testing.T) {
		route, err := router.Match("GET", "/api/admin/users")
		require.NoError(t, err)
		require.Equal(t, "admin.users.list", route.RouteID)
	})

	t.Run("Matches POST /api/admin/users to admin.users.manage", func(t *testing.T) {
		route, err := router.Match("POST", "/api/admin/users")
		require.NoError(t, err)
		require.Equal(t, "admin.users.manage", route.RouteID)
	})

	t.Run("Unmapped path returns ErrRouteNotFound", func(t *testing.T) {
		_, err := router.Match("GET", "/api/unknown")
		require.ErrorIs(t, err, ErrRouteNotFound)
	})

	t.Run("Unmapped method returns ErrRouteNotFound", func(t *testing.T) {
		_, err := router.Match("DELETE", "/api/orders")
		require.ErrorIs(t, err, ErrRouteNotFound)
	})
}
