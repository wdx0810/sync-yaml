package api

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

// RegisterStaticRoutes registers static file serving routes on the router.
// frontendFS should be the sub-filesystem pointing to the dist directory.
// - Requests matching /api/ are left for API handlers.
// - Requests matching static assets are served from the filesystem.
// - All other requests fall back to index.html for SPA routing support.
func RegisterStaticRoutes(router *mux.Router, frontendFS fs.FS) {
	fileServer := http.FileServer(http.FS(frontendFS))

	router.PathPrefix("/").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip API routes.
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}

		// Try to serve the file directly.
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		// Check if the file exists in the filesystem.
		if _, err := fs.Stat(frontendFS, path); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}

		// Fall back to index.html for SPA routing.
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}
