// Package ui serves the embedded single-page application used to inspect the
// proxy's log stream and to edit its configuration.
//
// The SPA is built by the web/ project (Svelte + Vite) and compiled into the
// binary with go:embed, so the proxy remains a single self-contained executable
// with no runtime file dependencies. Handler serves the bundle under any URL
// prefix; the client-side router owns everything below that prefix, so unknown
// paths fall back to index.html instead of a 404.
package ui

import (
	"bytes"
	"embed"
	"html"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// Mount paths the SPA is served from. The same bundle backs both routes.
const (
	// LogsMountPath hosts the live log viewer.
	LogsMountPath = "/logs"
	// ConfigMountPath hosts the configuration editor.
	ConfigMountPath = "/config"
)

// indexFile is the entry document served for the mount root and SPA routes.
const indexFile = "index.html"

//go:embed assets/*
var embedded embed.FS

// assets is the embedded tree rooted at the assets directory, so lookups use
// paths like "assets/index.js" rather than "assets/assets/index.js".
var assets fs.FS = mustSub()

// mustSub roots the embedded filesystem at the assets directory. The embed
// directive guarantees the directory exists, so a failure is a programming
// error rather than a runtime condition.
func mustSub() fs.FS {
	sub, err := fs.Sub(embedded, "assets")
	if err != nil {
		panic("ui: embedded assets are missing: " + err.Error())
	}
	return sub
}

// Handler returns an http.Handler serving the embedded SPA beneath mountPath.
//
// mountPath is an absolute URL prefix such as "/logs" or "/config" (a trailing
// slash is ignored). A request for the bare prefix is redirected to the
// trailing-slash form so the browser resolves the bundle's relative asset URLs
// from the mount root. Paths below the prefix that name an embedded file are
// served as-is; every other path is answered with index.html so the
// client-side router can render it.
//
// Only GET and HEAD are accepted. Every response is marked no-store so a newly
// built binary never pairs fresh HTML with a stale script from the browser
// cache.
func Handler(mountPath string) http.Handler {
	mount := normalizeMountPath(mountPath)
	return &spaHandler{
		mountPath: mount,
		index:     indexPage(mount),
	}
}

// spaHandler serves the embedded SPA for one mount prefix.
type spaHandler struct {
	mountPath string
	index     []byte
}

// ServeHTTP resolves one request against the embedded bundle.
func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	p := r.URL.Path
	if p == "" {
		p = "/"
	}

	if h.mountPath != "" {
		if p == h.mountPath {
			// Relative asset URLs in the bundle only resolve correctly from
			// the trailing-slash form of the prefix.
			http.Redirect(w, r, redirectTarget(h.mountPath+"/", r.URL.RawQuery), http.StatusMovedPermanently)
			return
		}
		if !strings.HasPrefix(p, h.mountPath+"/") {
			http.NotFound(w, r)
			return
		}
		p = strings.TrimPrefix(p, h.mountPath+"/")
	} else {
		p = strings.TrimPrefix(p, "/")
	}

	name := cleanAssetName(p)
	if name != indexFile {
		if data, err := fs.ReadFile(assets, name); err == nil {
			h.write(w, r, name, data)
			return
		}
	}

	// The mount root and unknown paths below the prefix: hand the request to
	// the client-side router with the shell document.
	h.write(w, r, indexFile, h.index)
}

// write emits one embedded asset, or the SPA shell, with the standard headers.
func (h *spaHandler) write(w http.ResponseWriter, r *http.Request, name string, data []byte) {
	hdr := w.Header()
	hdr.Set("Content-Type", contentType(name))
	hdr.Set("Content-Length", strconv.Itoa(len(data)))
	hdr.Set("Cache-Control", "no-store")
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(data)
}

// normalizeMountPath makes a mount prefix absolute and strips its trailing
// slash. An empty prefix or "/" serves the bundle from the root.
func normalizeMountPath(p string) string {
	if p == "" || p == "/" {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return strings.TrimSuffix(p, "/")
}

// cleanAssetName maps a path relative to the mount root to a name in the
// embedded assets tree. Cleaning against an absolute path collapses any ".."
// segments before they reach the filesystem view, so traversal cannot escape
// the assets root.
func cleanAssetName(p string) string {
	p = strings.TrimPrefix(path.Clean("/"+p), "/")
	if p == "" || p == "." {
		return indexFile
	}
	return p
}

// redirectTarget joins a query string to a redirect location.
func redirectTarget(location, rawQuery string) string {
	if rawQuery == "" {
		return location
	}
	return location + "?" + rawQuery
}

// indexPage loads index.html and injects a <base> element pointing at the mount
// root. The bundle is built with relative asset URLs (it is served under more
// than one prefix), and the base element keeps those URLs resolving correctly
// even for deeply nested client-side routes.
func indexPage(mountPath string) []byte {
	raw, err := fs.ReadFile(assets, indexFile)
	if err != nil {
		panic("ui: embedded " + indexFile + " is missing: " + err.Error())
	}
	if mountPath == "" {
		return raw
	}
	marker := []byte("<head>")
	i := bytes.Index(raw, marker)
	if i < 0 {
		return raw
	}
	base := []byte(`<base href="` + html.EscapeString(mountPath) + `/" />`)
	out := make([]byte, 0, len(raw)+len(base))
	out = append(out, raw[:i+len(marker)]...)
	out = append(out, base...)
	out = append(out, raw[i+len(marker):]...)
	return out
}

// contentType returns the MIME type for an embedded asset. The mapping is
// explicit rather than delegated to mime.TypeByExtension because that consults
// the host's MIME database, which on Windows can report misleading types for
// web assets.
func contentType(name string) string {
	switch path.Ext(name) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".json", ".map":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".ico":
		return "image/x-icon"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	default:
		return "application/octet-stream"
	}
}
