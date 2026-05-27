// services/api-gateway/middleware.go
package main

import (
	"context"
	"net/http"
	"strings"

	"sis/pkg/auth"
)

type contextKey string

const ctxUserID contextKey = "userID"

// RequireAuth validates a Bearer JWT and stores the userID in context.
func (s *Server) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "missing or invalid authorization header")
			return
		}
		tokenStr := strings.TrimPrefix(header, "Bearer ")
		userID, err := auth.ValidateToken(tokenStr, string(s.jwtSecret))
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}
		ctx := context.WithValue(r.Context(), ctxUserID, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// UserIDFromCtx extracts the authenticated user ID from context.
func UserIDFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(ctxUserID).(string)
	return v
}

// RequireBotSecret validates that the request carries the shared bot secret.
// Used to protect internal bot-to-gateway endpoints.
func (s *Server) RequireBotSecret(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if s.botSecret == "" || !strings.HasPrefix(header, "Bearer ") || strings.TrimPrefix(header, "Bearer ") != s.botSecret {
			writeError(w, http.StatusUnauthorized, "invalid bot secret")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAdmin checks that the authenticated user has role='admin' in the DB.
// Must be used after RequireAuth.
func (s *Server) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := UserIDFromCtx(r.Context())
		if userID == "" {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		var role string
		if err := s.pool.QueryRow(r.Context(),
			`SELECT role FROM users WHERE id = $1`, userID,
		).Scan(&role); err != nil {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		if role != "admin" {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		next.ServeHTTP(w, r)
	})
}
