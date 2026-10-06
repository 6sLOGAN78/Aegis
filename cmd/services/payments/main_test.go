package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPaymentsService(t *testing.T) {
	ts := httptest.NewServer(Routes())
	defer ts.Close()

	t.Run("GET /api/payments returns HTTP 200 with JSON payment records", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/api/payments")
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

		var payments []Payment
		err = json.NewDecoder(resp.Body).Decode(&payments)
		require.NoError(t, err)
		require.Len(t, payments, 2)
		require.Equal(t, "pay_201", payments[0].ID)
	})

	t.Run("POST /api/payments returns HTTP 200 with confirmation", func(t *testing.T) {
		resp, err := http.Post(ts.URL+"/api/payments", "application/json", nil)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

		var res map[string]string
		err = json.NewDecoder(resp.Body).Decode(&res)
		require.NoError(t, err)
		require.Equal(t, "processed", res["status"])
		require.Equal(t, "pay_201", res["payment_id"])
	})

	t.Run("DELETE /api/payments returns 405 Method Not Allowed", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodDelete, ts.URL+"/api/payments", nil)
		require.NoError(t, err)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	})
}
