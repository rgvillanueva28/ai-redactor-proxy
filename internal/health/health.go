// Package health provides the proxy's liveness endpoint.
package health

import "net/http"

// Handler returns 200 OK with body "ok".
//
// The endpoint is intentionally dependency-free: it reports that the HTTP
// server is accepting and answering requests, not that upstreams are reachable,
// so a local health check stays fast and never touches PII or the network.
func Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}
}
