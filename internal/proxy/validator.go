package proxy

import (
	"errors"
	"net/http"
	"strings"
)

var (
	// ErrPathContainsTraversal indicates dot-segments (.. or .) were detected.
	ErrPathContainsTraversal = errors.New("path contains traversal dot-segments")
	// ErrPathContainsEncodedSlash indicates encoded slashes or backslashes were detected.
	ErrPathContainsEncodedSlash = errors.New("path contains encoded directory separators")
	// ErrPathContainsDoubleSlash indicates consecutive slashes were detected.
	ErrPathContainsDoubleSlash = errors.New("path contains duplicate slashes")
	// ErrPathContainsNulByte indicates raw or encoded NUL bytes were detected.
	ErrPathContainsNulByte = errors.New("path contains NUL bytes")
	// ErrPathInvalidPrefix indicates path does not start with an absolute leading slash.
	ErrPathInvalidPrefix = errors.New("path does not start with /")
)

// ValidatePathZeroRepair inspects the raw wire bytes in r.RequestURI and rejects
// traversal attempts, encoded separators, duplicate slashes, and NUL bytes.
// It never calls path.Clean() or mutates the path.
func ValidatePathZeroRepair(r *http.Request) (string, error) {
	rawURI := r.RequestURI
	rawPath := rawURI
	if idx := strings.IndexByte(rawURI, '?'); idx != -1 {
		rawPath = rawURI[:idx]
	}
	if idx := strings.IndexByte(rawPath, '#'); idx != -1 {
		rawPath = rawPath[:idx]
	}

	lower := strings.ToLower(rawPath)

	// 1. Detect duplicate slashes
	if strings.Contains(rawPath, "//") {
		return "", ErrPathContainsDoubleSlash
	}

	// 2. Detect NUL bytes (raw or encoded)
	if strings.Contains(rawPath, "\x00") || strings.Contains(lower, "%00") {
		return "", ErrPathContainsNulByte
	}

	// 3. Detect encoded slashes (%2f, %5c) and backslashes
	if strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") || strings.Contains(rawPath, "\\") {
		return "", ErrPathContainsEncodedSlash
	}

	// 4. Detect dot-segments (literal and encoded: /../, /./, %2e%2e, .%2e, %2e., /%2e/, /%2e)
	if strings.Contains(rawPath, "/../") || strings.HasSuffix(rawPath, "/..") || rawPath == ".." ||
		strings.Contains(rawPath, "/./") || strings.HasSuffix(rawPath, "/.") || rawPath == "." ||
		strings.Contains(lower, "%2e%2e") || strings.Contains(lower, ".%2e") || strings.Contains(lower, "%2e.") ||
		strings.Contains(lower, "/%2e/") || strings.HasSuffix(lower, "/%2e") {
		return "", ErrPathContainsTraversal
	}

	// 5. Must start with absolute slash
	if !strings.HasPrefix(rawPath, "/") {
		return "", ErrPathInvalidPrefix
	}

	return rawPath, nil
}

// PathValidationMiddleware creates middleware that rejects malicious paths with HTTP 400 Bad Request.
func PathValidationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := ValidatePathZeroRepair(r)
		if err != nil {
			reqID := r.Header.Get("X-Request-ID")
			WriteProblemDetails(
				w,
				http.StatusBadRequest,
				"Bad Request",
				err.Error(),
				"https://aegis.local/errors/invalid-path",
				reqID,
			)
			return
		}
		next.ServeHTTP(w, r)
	})
}
