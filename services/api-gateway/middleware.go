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
