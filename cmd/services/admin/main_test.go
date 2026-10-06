package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminService(t *testing.T) {
	ts := httptest.NewServer(Routes())
	defer ts.Close()

	t.Run("GET /api/admin/users returns HTTP 200 with JSON admin data", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/api/admin/users")
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

		var users []AdminUser
		err = json.NewDecoder(resp.Body).Decode(&users)
		require.NoError(t, err)
		require.Len(t, users, 2)
		require.Equal(t, "usr_admin_01", users[0].ID)
		require.Equal(t, "application-admin", users[0].Role)
	})

	t.Run("POST /api/admin/users returns HTTP 200 with confirmation", func(t *testing.T) {
		resp, err := http.Post(ts.URL+"/api/admin/users", "application/json", nil)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

		var res map[string]string
		err = json.NewDecoder(resp.Body).Decode(&res)
		require.NoError(t, err)
		require.Equal(t, "success", res["status"])
	})

	t.Run("PUT /api/admin/users returns 405 Method Not Allowed", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPut, ts.URL+"/api/admin/users", nil)
		require.NoError(t, err)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	})
}
