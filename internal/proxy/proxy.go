package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// CreateUpstreamTransport instantiates a shared, connection-pooled mTLS http.Transport.
// Reusing a single transport avoids ephemeral port exhaustion under high load (Pitfall 4).
func CreateUpstreamTransport(clientCert tls.Certificate, caPool *x509.CertPool) *http.Transport {
	return &http.Transport{
		TLSClientConfig: &tls.Config{
			Certificates:       []tls.Certificate{clientCert},
			RootCAs:            caPool,
			MinVersion:         tls.VersionTLS13,
			InsecureSkipVerify: false, // Invariant 4: Never disable verification
		},
		MaxIdleConns:        1000,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}
}

// NewReverseProxyWithMTLS creates a reverse proxy configured with mTLS transport and assertion injection.
func NewReverseProxyWithMTLS(
	targetURL *url.URL,
	canonicalPath string,
	requestID string,
	assertionJWT string,
	transport *http.Transport,
) *httputil.ReverseProxy {
	var rt http.RoundTripper = transport
	if transport == nil {
		rt = http.DefaultTransport
	}
	return &httputil.ReverseProxy{
		Transport: rt,
		Rewrite: func(pr *httputil.ProxyRequest) {
			wrapOutboundBody(pr)
			pr.SetURL(targetURL)
			pr.Out.URL.Path = canonicalPath
			pr.Out.URL.RawPath = ""

			// Scrub all client-supplied X-Aegis-* headers case-insensitively
			for k := range pr.Out.Header {
				if strings.HasPrefix(strings.ToLower(k), "x-aegis-") {
					pr.Out.Header.Del(k)
				}
			}

			// Scrub forwarding headers
			pr.Out.Header.Del("Forwarded")
			pr.Out.Header.Del("X-Forwarded-For")
			pr.Out.Header.Del("X-Forwarded-Host")
			pr.Out.Header.Del("X-Forwarded-Proto")
			pr.Out.Header.Del("X-Forwarded-Server")

			// Scrub inbound Authorization token
			pr.Out.Header.Del("Authorization")

			// Inject correlation tracking ID
			reqID := requestID
			if reqID == "" && pr.In != nil {
				reqID = pr.In.Header.Get("X-Request-ID")
			}
			if reqID != "" {
				pr.Out.Header.Set("X-Request-ID", reqID)
			}

			// Inject signed gateway assertion JWT (AUTH-05)
			if assertionJWT != "" {
				pr.Out.Header.Set("X-Aegis-Assertion", assertionJWT)
			}
		},
		ModifyResponse: modifyResponseEnforceLimit,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			reqID := requestID
			if reqID == "" && r != nil {
				reqID = r.Header.Get("X-Request-ID")
			}
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				writeBodyTooLarge(w, reqID)
				return
			}
			WriteProblemDetails(
				w,
				http.StatusBadGateway,
				"Bad Gateway",
				"Upstream backend unreachable or TLS handshake failed",
				"https://aegis.local/errors/bad-gateway",
				reqID,
			)
		},
	}
}

// NewReverseProxy creates a reverse proxy configured with the Go 1.20+ Rewrite hook.
// It unconditionally purges client-supplied X-Aegis-* headers, Forwarded headers,
// and Authorization tokens, and forwards the exact canonical validated path bytes.
// Maintained for backward compatibility.
func NewReverseProxy(targetURL *url.URL, canonicalPath string, requestID string) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			wrapOutboundBody(pr)

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
		ModifyResponse: modifyResponseEnforceLimit,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			reqID := requestID
			if reqID == "" && r != nil {
				reqID = r.Header.Get("X-Request-ID")
			}
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				writeBodyTooLarge(w, reqID)
				return
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

// limitTrackingBody serialises access to a request body that may be wrapped by
// http.MaxBytesReader, so that ModifyResponse can drain the remainder after an
// early upstream reply and surface *http.MaxBytesError (GW-01). Close is a
// no-op: the ingress server closes the underlying body itself.
type limitTrackingBody struct {
	mu sync.Mutex
	rc io.ReadCloser
}

func (b *limitTrackingBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.rc.Read(p)
}

func (b *limitTrackingBody) Close() error { return nil }

// wrapOutboundBody wraps the outbound request body so modifyResponseEnforceLimit
// can drain it after an early upstream reply.
func wrapOutboundBody(pr *httputil.ProxyRequest) {
	if pr.Out.Body == nil || pr.Out.Body == http.NoBody {
		return
	}
	pr.Out.Body = &limitTrackingBody{rc: pr.Out.Body}
}

// modifyResponseEnforceLimit drains the remainder of the request body when the
// upstream answered before consuming it. The drain is bounded by the ingress
// MaxBytesReader; an over-limit body yields *http.MaxBytesError, which the
// ErrorHandler maps to 413 so oversize chunked bodies never surface as 2xx.
func modifyResponseEnforceLimit(resp *http.Response) error {
	if resp.Request == nil {
		return nil
	}
	tb, ok := resp.Request.Body.(*limitTrackingBody)
	if !ok {
		return nil
	}
	_, err := io.Copy(io.Discard, tb)
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return err
	}
	return nil
}
