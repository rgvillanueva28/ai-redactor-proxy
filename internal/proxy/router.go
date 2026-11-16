package proxy

import (
	"context"
	"net/http"
	"strings"
	"sync"
)

// Provider identifies the upstream API dialect a request should be routed to.
type Provider string

// Known providers. Any unrecognised upstream is treated as ProviderGeneric.
const (
	ProviderOpenAI    Provider = "openai"
	ProviderAnthropic Provider = "anthropic"
	ProviderGemini    Provider = "gemini"
	ProviderGeneric   Provider = "generic"
)

// Route is the resolution result for a single request.
type Route struct {
	// Provider selects the upstream API dialect.
	Provider Provider
	// UpstreamURL is the base URL requests for this provider are sent to.
	// For ProviderGeneric it is the request URL as-is.
	UpstreamURL string
}

// providerContextKey is the type of the context key used to carry a resolved
// provider. The type is unexported so no other package can create colliding
// keys; read values with ContextProvider.
type providerContextKey struct{}

// UpstreamProviderKey is the context key under which a resolved Provider is
// stored. Its type is unexported; use ContextProvider to read the value.
var UpstreamProviderKey = providerContextKey{}

// contextWithProvider stores p in ctx for later retrieval with ContextProvider.
func contextWithProvider(ctx context.Context, p Provider) context.Context {
	return context.WithValue(ctx, UpstreamProviderKey, p)
}

// ContextProvider returns the provider stored in ctx, or ProviderGeneric when
// none is present.
func ContextProvider(ctx context.Context) Provider {
	if ctx == nil {
		return ProviderGeneric
	}
	if p, ok := ctx.Value(UpstreamProviderKey).(Provider); ok && p != "" {
		return p
	}
	return ProviderGeneric
}

// defaultHosts maps a host substring to the route used for matching hosts.
// Entries are matched case-insensitively; when several substrings match, the
// longest one wins so more specific mappings can override broader ones.
var defaultHosts = map[string]Route{
	"api.openai.com":                    {Provider: ProviderOpenAI, UpstreamURL: "https://api.openai.com"},
	"api.anthropic.com":                 {Provider: ProviderAnthropic, UpstreamURL: "https://api.anthropic.com"},
	"generativelanguage.googleapis.com": {Provider: ProviderGemini, UpstreamURL: "https://generativelanguage.googleapis.com"},
}

// Router resolves the upstream route for an incoming request.
//
// The zero value is ready to use and resolves against the built-in provider
// host table. Callers that need extra host mappings should build a Router with
// NewRouter and add them via WithProvider. A Router is safe for concurrent
// use.
type Router struct {
	mu    sync.RWMutex
	hosts map[string]Route
}

// NewRouter returns a Router preloaded with the built-in provider host table.
func NewRouter() *Router {
	hosts := make(map[string]Route, len(defaultHosts))
	for k, v := range defaultHosts {
		hosts[k] = v
	}
	return &Router{hosts: hosts}
}

// WithProvider registers hostSubstring (matched case-insensitively against the
// request Host) as a route to upstream. The provider is inferred from the host
// substring and upstream URL; hosts that mention a known provider (for example
// "openai" or "anthropic") inherit that provider, everything else is treated
// as generic. WithProvider returns the receiver so calls can be chained. It is
// safe to call concurrently with Resolve.
func (rt *Router) WithProvider(hostSubstring, upstream string) *Router {
	hostSubstring = strings.ToLower(strings.TrimSpace(hostSubstring))
	upstream = strings.TrimSpace(upstream)
	if hostSubstring == "" || upstream == "" {
		return rt
	}
	upstream = strings.TrimRight(upstream, "/")
	route := Route{
		Provider:    inferProvider(hostSubstring, upstream),
		UpstreamURL: upstream,
	}

	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.hosts == nil {
		rt.hosts = make(map[string]Route, len(defaultHosts)+1)
		for k, v := range defaultHosts {
			rt.hosts[k] = v
		}
	}
	rt.hosts[hostSubstring] = route
	return rt
}

// Resolve returns the Route for r. Resolution order:
//
//  1. Host header substring match against the routing table.
//  2. Authorization Bearer token heuristics (sk-ant-* is Anthropic, sk-* is
//     OpenAI).
//  3. URL path prefix /v1beta/models/ (Gemini's native API shape).
//  4. Generic fallback, forwarding to the request URL as-is.
//
// Resolve does not mutate r. Callers that want the provider on the request
// context should store it themselves, for example:
//
//	route := router.Resolve(r)
//	r = r.WithContext(contextWithProvider(r.Context(), route.Provider))
func (rt *Router) Resolve(r *http.Request) Route {
	if r == nil {
		return Route{Provider: ProviderGeneric}
	}

	host := strings.ToLower(r.Host)
	if host == "" && r.URL != nil {
		host = strings.ToLower(r.URL.Host)
	}
	if route, ok := rt.lookupHost(host); ok {
		return route
	}

	if p, ok := providerFromAuth(r.Header.Get("Authorization")); ok {
		return Route{Provider: p, UpstreamURL: defaultUpstream(p)}
	}

	if r.URL != nil && strings.HasPrefix(r.URL.Path, "/v1beta/models/") {
		return Route{Provider: ProviderGemini, UpstreamURL: defaultUpstream(ProviderGemini)}
	}

	return Route{Provider: ProviderGeneric, UpstreamURL: requestURL(r)}
}

// lookupHost matches host against the routing table, preferring the longest
// matching substring.
func (rt *Router) lookupHost(host string) (Route, bool) {
	if rt != nil {
		rt.mu.RLock()
		defer rt.mu.RUnlock()
		if len(rt.hosts) > 0 {
			return matchHost(rt.hosts, host)
		}
	}
	return matchHost(defaultHosts, host)
}

// matchHost performs substring matching over routes. Longest match wins;
// because map iteration order is random the length comparison is what makes
// the result deterministic.
func matchHost(routes map[string]Route, host string) (Route, bool) {
	bestLen := 0
	var best Route
	found := false
	for substr, route := range routes {
		if len(substr) <= bestLen || !strings.Contains(host, substr) {
			continue
		}
		bestLen = len(substr)
		best = route
		found = true
	}
	return best, found
}

// providerFromAuth applies Bearer token prefix heuristics. Reports false when
// the header is absent or does not look like a known API key.
func providerFromAuth(header string) (Provider, bool) {
	const bearer = "bearer "
	if len(header) < len(bearer) || !strings.EqualFold(header[:len(bearer)], bearer) {
		return "", false
	}
	token := strings.TrimSpace(header[len(bearer):])
	switch {
	case strings.HasPrefix(token, "sk-ant-"):
		return ProviderAnthropic, true
	case strings.HasPrefix(token, "sk-"):
		return ProviderOpenAI, true
	default:
		return "", false
	}
}

// inferProvider guesses a provider from a host substring and upstream URL.
func inferProvider(hostSubstring, upstream string) Provider {
	s := strings.ToLower(hostSubstring + " " + upstream)
	switch {
	case strings.Contains(s, "anthropic"):
		return ProviderAnthropic
	case strings.Contains(s, "openai"):
		return ProviderOpenAI
	case strings.Contains(s, "generativelanguage"), strings.Contains(s, "googleapis"), strings.Contains(s, "gemini"):
		return ProviderGemini
	default:
		return ProviderGeneric
	}
}

// defaultUpstream returns the canonical base URL for a known provider.
func defaultUpstream(p Provider) string {
	for _, route := range defaultHosts {
		if route.Provider == p {
			return route.UpstreamURL
		}
	}
	return ""
}

// requestURL reconstructs the request URL as an absolute URL, preferring the
// URL already present on the request. Requests received by a server normally
// carry only a path, so the scheme is derived from TLS state and defaults to
// https because upstream traffic is encrypted.
func requestURL(r *http.Request) string {
	if r.URL == nil {
		return ""
	}
	if r.URL.IsAbs() {
		return r.URL.String()
	}
	scheme := "https"
	if r.TLS == nil && r.URL.Scheme != "" {
		scheme = r.URL.Scheme
	}
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	if host == "" {
		return r.URL.RequestURI()
	}
	return scheme + "://" + host + r.URL.RequestURI()
}
