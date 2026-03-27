package middleware

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"

	"github.com/aegisflow/aegisflow/pkg/types"
)

// AdminAuth protects admin endpoints with a bearer token.
// If adminToken is empty, a warning is logged and all admin data endpoints are blocked.
func AdminAuth(adminToken string) func(http.Handler) http.Handler {
	if adminToken == "" {
		log.Printf("WARNING: admin.token is not configured — admin data endpoints will reject all requests. Set admin.token in your config to enable admin access.")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth for health endpoint
			if r.URL.Path == "/health" {
				next.ServeHTTP(w, r)
				return
			}

			// Skip auth for dashboard (served publicly, data endpoints protected)
			if r.URL.Path == "/" || r.URL.Path == "/dashboard" {
				next.ServeHTTP(w, r)
				return
			}

			// Skip auth for metrics (Prometheus scraping)
			if r.URL.Path == "/metrics" {
				next.ServeHTTP(w, r)
				return
			}

			// Block all admin data endpoints if no token is configured
			if adminToken == "" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(types.NewErrorResponse(401, "authentication_error", "admin token not configured — set admin.token in config"))
				return
			}

			// Only accept token from Authorization header (never from URL query params)
			token := extractBearerToken(r)

			if subtle.ConstantTimeCompare([]byte(token), []byte(adminToken)) != 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(types.NewErrorResponse(401, "authentication_error", "invalid or missing admin token"))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if len(auth) > 7 && auth[:7] == "Bearer " {
		return auth[7:]
	}
	return ""
}
