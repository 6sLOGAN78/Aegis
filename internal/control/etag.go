package control

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

// FormatETag formats a monotonic integer version as an RFC 7232 quoted entity tag.
func FormatETag(version int64) string {
	return fmt.Sprintf(`"%d"`, version)
}

// FormatResourceETag computes a strong SHA-256 entity tag from raw payload bytes.
func FormatResourceETag(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf(`"%s"`, hex.EncodeToString(sum[:16]))
}

// VerifyIfMatch checks whether the supplied If-Match header matches currentVersion.
// Returns false if If-Match is provided but does not match currentVersion.
func VerifyIfMatch(ifMatch string, currentVersion int64) bool {
	clean := strings.Trim(strings.TrimSpace(ifMatch), `"`)
	if clean == "*" {
		return true
	}
	expected := fmt.Sprintf("%d", currentVersion)
	return clean == expected
}

// ValidateIfMatch is an alias for VerifyIfMatch.
func ValidateIfMatch(ifMatch string, currentVersion int64) bool {
	return VerifyIfMatch(ifMatch, currentVersion)
}

// RequireIfMatch validates that the request contains a matching If-Match header.
// If missing or mismatched, it writes HTTP 412 Precondition Failed and returns false.
func RequireIfMatch(w http.ResponseWriter, r *http.Request, currentVersion int64) bool {
	ifMatch := r.Header.Get("If-Match")
	if ifMatch == "" {
		WritePreconditionFailed(w, r, "Missing mandatory If-Match header for optimistic concurrency control")
		return false
	}

	if !VerifyIfMatch(ifMatch, currentVersion) {
		w.Header().Set("ETag", FormatETag(currentVersion))
		WritePreconditionFailed(w, r, fmt.Sprintf("Resource modified concurrently. Active version is %d", currentVersion))
		return false
	}

	return true
}

// WritePreconditionFailed writes an HTTP 412 Precondition Failed problem details response.
func WritePreconditionFailed(w http.ResponseWriter, r *http.Request, message string) {
	writeErrorResponse(w, r, http.StatusPreconditionFailed, "PRECONDITION_FAILED", message)
}
