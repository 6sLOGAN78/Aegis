package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOrdersService(t *testing.T) {
	ts := httptest.NewServer(Routes())
	defer ts.Close()

	t.Run("GET /api/orders returns HTTP 200 with JSON orders array", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/api/orders")
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

		var orders []Order
		err = json.NewDecoder(resp.Body).Decode(&orders)
		require.NoError(t, err)
		require.Len(t, orders, 2)
		require.Equal(t, "ord_101", orders[0].ID)
		require.Equal(t, "Cloud Scanner", orders[0].Item)
	})

	t.Run("POST /api/orders returns 405 Method Not Allowed", func(t *testing.T) {
		resp, err := http.Post(ts.URL+"/api/orders", "application/json", nil)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	})
}
