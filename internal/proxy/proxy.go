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
			if errors.Is(err, errBodyDrainTimeout) {
				writeDrainTimeout(w, reqID)
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
			if errors.Is(err, errBodyDrainTimeout) {
				writeDrainTimeout(w, reqID)
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
	// The drain is bounded in time as well as size: a client trickling a
	// chunked body must not hold a concurrency slot indefinitely. On timeout
	// the gateway fails closed (408) rather than forwarding a response for a
	// body it could not check. The goroutine ends when net/http closes the
	// inbound body after the handler returns.
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, tb)
		done <- err
	}()
	timer := time.NewTimer(bodyDrainTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return err
		}
		return nil
	case <-timer.C:
		return errBodyDrainTimeout
	}
}

// bodyDrainTimeout caps how long ModifyResponse waits for the unread rest of
// a request body after an early upstream reply. A var so tests can shorten it.
var bodyDrainTimeout = 5 * time.Second

var errBodyDrainTimeout = errors.New("proxy: timed out draining request body")

// writeDrainTimeout maps errBodyDrainTimeout to 408 problem+json.
// It also expires the connection's read deadline so the blocked drain read
// (and net/http's own post-handler body discard) return at once.
// If no wrapper exposes the connection's deadline, the connection is hijacked
// and closed instead: a stalled drain must never outlive the request.
func writeDrainTimeout(w http.ResponseWriter, reqID string) {
	rc := http.NewResponseController(w)
	if err := rc.SetReadDeadline(time.Now()); err != nil {
		if conn, _, herr := rc.Hijack(); herr == nil {
			_ = conn.Close()
			return
		}
	}
	w.Header().Set("Connection", "close")
	WriteProblemDetails(
		w,
		http.StatusRequestTimeout,
		"Request Timeout",
		"Request body was not received in time",
		"https://aegis.local/errors/request-timeout",
		reqID,
	)
}
