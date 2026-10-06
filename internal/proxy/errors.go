package proxy

import (
	"encoding/json"
	"net/http"
)

// RFC7807Problem represents an RFC 7807 problem details payload.
type RFC7807Problem struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance,omitempty"`
}

// WriteProblemDetails writes an RFC 7807 problem details response to the ResponseWriter.
func WriteProblemDetails(w http.ResponseWriter, status int, title, detail, problemType, requestID string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	prob := RFC7807Problem{
		Type:     problemType,
		Title:    title,
		Status:   status,
		Detail:   detail,
		Instance: requestID,
	}
	_ = json.NewEncoder(w).Encode(prob)
}
