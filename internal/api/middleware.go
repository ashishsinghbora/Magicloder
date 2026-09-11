package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/ashishsinghbora/magicloder/internal/config"
)

// AuthMiddleware enforces bearer token authentication if configured.
func AuthMiddleware(cfg *config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Health and version are public
		if r.URL.Path == "/api/v1/health" || r.URL.Path == "/api/v1/version" {
			next.ServeHTTP(w, r)
			return
		}

		if cfg != nil && cfg.AuthToken != "" {
			authHeader := r.Header.Get("Authorization")
			expected := "Bearer " + cfg.AuthToken
			if authHeader == "" || authHeader != expected {
				writeError(w, http.StatusUnauthorized, "missing or invalid authorization token")
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// CORSMiddleware handles CORS requests safely.
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			// Allow localhost / 127.0.0.1 / tauri origins
			if strings.HasPrefix(origin, "http://localhost") ||
				strings.HasPrefix(origin, "http://127.0.0.1") ||
				strings.HasPrefix(origin, "tauri://") ||
				strings.HasPrefix(origin, "https://tauri.localhost") {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Tus-Resumable, Upload-Length, Upload-Metadata, Upload-Offset")
			}
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// RecoverMiddleware prevents panics in handlers.
func RecoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				writeError(w, http.StatusInternalServerError, fmt.Sprintf("internal server panic: %v", rec))
			}
		}()
		next.ServeHTTP(w, r)
	})
}
