// Package ui serves the embedded web UI for watching the proxy's log stream.
//
// The assets are compiled into the binary with go:embed, so the proxy remains a
// single self-contained executable with no runtime file dependencies. Handler
// serves the viewer's HTML, CSS and JavaScript under MountPath; the page itself
// opens an EventSource against the SSE endpoint implemented by the logging
// package.
package ui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// MountPath is the URL prefix under which the log viewer is served.
const MountPath = "/logs"

// indexFile is the asset served for the mount root.
const indexFile = "index.html"

//go:embed assets/*
var embedded embed.FS

// assets is the embedded tree rooted at the assets directory, so lookups use
// paths like "app.js" rather than "assets/app.js".
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

// Handler returns an http.Handler serving the embedded log viewer.
//
// MountPath (with or without a trailing slash) serves the viewer page; any
// other path is resolved against the embedded assets directory. The handler
// works both when mounted directly at MountPath and when mounted at "/" with
// request paths still carrying the MountPath prefix, which keeps routing in the
// caller's mux flexible.
//
// Only GET and HEAD are accepted. Every response is marked no-store so a newly
// built binary never pairs fresh HTML with a stale script from the browser
// cache.
func Handler() http.Handler {
	return http.HandlerFunc(serveAsset)
}

// serveAsset writes one embedded asset for a request.
func serveAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name, ok := assetName(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(assets, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	h := w.Header()
	h.Set("Content-Type", contentType(name))
	h.Set("Content-Length", strconv.Itoa(len(data)))
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(data)
}

// assetName maps a request path to a name in the embedded assets tree. It
// reports false for paths that cannot name an asset.
func assetName(p string) (string, bool) {
	if p == "" {
		p = "/"
	}
	switch {
	case p == MountPath, p == MountPath+"/":
		return indexFile, true
	case strings.HasPrefix(p, MountPath+"/"):
		p = strings.TrimPrefix(p, MountPath+"/")
	case strings.HasPrefix(p, "/"):
		// Mounted beneath a stripping mux: treat the path as already relative
		// to the mount root.
		p = strings.TrimPrefix(p, "/")
	default:
		return "", false
	}

	// Clean before touching the filesystem view so ".." segments cannot escape
	// the assets root. Cleaning against an absolute path collapses every
	// traversal attempt into a path under the root.
	p = strings.TrimPrefix(path.Clean("/"+p), "/")
	if p == "" || p == "." {
		return indexFile, true
	}
	return p, true
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
	case ".json":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".ico":
		return "image/x-icon"
	case ".png":
		return "image/png"
	default:
		return "application/octet-stream"
	}
}
