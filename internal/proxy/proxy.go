package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// NewReverseProxy creates a reverse proxy configured with the Go 1.20+ Rewrite hook.
// It unconditionally purges client-supplied X-Aegis-* headers, Forwarded headers,
// and Authorization tokens, and forwards the exact canonical validated path bytes.
func NewReverseProxy(targetURL *url.URL, canonicalPath string, requestID string) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// 1. Point outbound request to target backend URL
			pr.SetURL(targetURL)

			// 2. Forward exact validated canonical path bytes (Invariant 7)
			pr.Out.URL.Path = canonicalPath
			pr.Out.URL.RawPath = ""

			// 3. Unconditionally delete all client-supplied X-Aegis-* headers case-insensitively
			for k := range pr.Out.Header {
				if strings.HasPrefix(strings.ToLower(k), "x-aegis-") {
					pr.Out.Header.Del(k)
				}
			}

			// 4. Delete client-supplied Forwarded and X-Forwarded-* headers
			pr.Out.Header.Del("Forwarded")
			pr.Out.Header.Del("X-Forwarded-For")
			pr.Out.Header.Del("X-Forwarded-Host")
			pr.Out.Header.Del("X-Forwarded-Proto")
			pr.Out.Header.Del("X-Forwarded-Server")

			// 5. Ingress user token is stripped: backends run in private network
			pr.Out.Header.Del("Authorization")

			// 6. Inject gateway correlation tracking header
			reqID := requestID
			if reqID == "" && pr.In != nil {
				reqID = pr.In.Header.Get("X-Request-ID")
			}
			if reqID != "" {
				pr.Out.Header.Set("X-Request-ID", reqID)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			reqID := requestID
			if reqID == "" && r != nil {
				reqID = r.Header.Get("X-Request-ID")
			}
			WriteProblemDetails(
				w,
				http.StatusBadGateway,
				"Bad Gateway",
				"Upstream backend unreachable",
				"https://aegis.local/errors/bad-gateway",
				reqID,
			)
		},
	}
}
