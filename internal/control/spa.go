package control

import (
	"io/fs"
	"net/http"
	"strings"

	"aegis/web"

	"github.com/go-chi/chi/v5"
)

// RegisterSPARoutes registers static asset serving and SPA fallback routes for the dashboard.
func RegisterSPARoutes(r chi.Router) {
	distSub, err := fs.Sub(web.DashboardDist, "dashboard/dist")
	if err != nil {
		r.Get("/dashboard/*", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Dashboard distribution assets unavailable", http.StatusNotFound)
		})
		return
	}

	fileServer := http.FileServer(http.FS(distSub))

	// Redirect root / and /dashboard to /dashboard/index.html
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dashboard/index.html", http.StatusMovedPermanently)
	})
	r.Get("/dashboard", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dashboard/index.html", http.StatusMovedPermanently)
	})

	// Static asset handler with SPA client-side fallback
	r.Get("/dashboard/*", func(w http.ResponseWriter, r *http.Request) {
		subPath := strings.TrimPrefix(r.URL.Path, "/dashboard/")
		if subPath == "" {
			subPath = "index.html"
		}

		// Try opening the requested file in the dist sub-filesystem
		f, err := distSub.Open(subPath)
		if err == nil {
			_ = f.Close()
			http.StripPrefix("/dashboard", fileServer).ServeHTTP(w, r)
			return
		}

		// Fallback to index.html for client-side routing
		indexFile, err := distSub.Open("index.html")
		if err == nil {
			_ = indexFile.Close()
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/dashboard/index.html"
			http.StripPrefix("/dashboard", fileServer).ServeHTTP(w, r2)
			return
		}

		http.Error(w, "Dashboard index.html not found", http.StatusNotFound)
	})
}
