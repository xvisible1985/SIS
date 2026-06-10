//go:build !linux

package main

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// StartSystemHealthMonitor is a no-op on non-Linux platforms.
func StartSystemHealthMonitor(_ context.Context, _ *pgxpool.Pool) {}

// LatestSystemHealth returns a zero snapshot on non-Linux platforms.
func LatestSystemHealth() SystemHealthSnapshot { return SystemHealthSnapshot{} }
