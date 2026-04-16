// services/api-gateway/server.go
package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Server holds shared dependencies for all HTTP handlers.
type Server struct {
	pool      *pgxpool.Pool
	rdb       *redis.Client
	jwtSecret []byte
}

// NewServer creates a Server.
func NewServer(pool *pgxpool.Pool, rdb *redis.Client, jwtSecret string) *Server {
	return &Server{pool: pool, rdb: rdb, jwtSecret: []byte(jwtSecret)}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// newUUID generates a random UUID v4.
func newUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
