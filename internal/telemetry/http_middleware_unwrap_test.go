package telemetry

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The metrics middleware must not hide the connection from
// http.ResponseController (the proxy's body-drain timeout relies on it).
func TestMetricsMiddleware_ResponseControllerReachesConn(t *testing.T) {
	var deadlineErr error
	h := MetricsMiddleware(NewMetrics())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadlineErr = http.NewResponseController(w).SetReadDeadline(time.Now().Add(time.Second))
	}))
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if deadlineErr != nil {
		t.Fatalf("SetReadDeadline through statusRecorder: %v", deadlineErr)
	}
}
