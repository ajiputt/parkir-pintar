// Package db — pgx pool factory + helper migration runner.
package db

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Open membuat pool dengan DSN. Pool size + timeout di-config dari env / config.
//
// Note tentang search_path:
//
//	pgx (dan libpq standar) TIDAK mengenali `?search_path=...` sebagai query
//	parameter. Hanya `?options=-c search_path=...` yang standar. Untuk DX yang
//	lebih baik (DSN simpler), kita extract `search_path` dari query parameter
//	manual dan pasang sebagai RuntimeParams di startup message Postgres.
//
// Contoh DSN yang didukung:
//
//	postgres://user:pass@host:5432/db?sslmode=disable&search_path=reservation
//	postgres://user:pass@host:5432/db?sslmode=disable&search_path=reservation,public
func Open(ctx context.Context, dsn string, maxConns, maxIdle int) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: parse: %w", err)
	}

	// Extract search_path dari query parameter (custom convention kita).
	if searchPath := extractSearchPath(dsn); searchPath != "" {
		if cfg.ConnConfig.RuntimeParams == nil {
			cfg.ConnConfig.RuntimeParams = make(map[string]string)
		}
		cfg.ConnConfig.RuntimeParams["search_path"] = searchPath

		// Defense-in-depth: juga set via SET command saat connect baru.
		// Ini menjamin search_path benar-benar applied bahkan kalau RuntimeParams
		// di-override oleh server-side default.
		afterConnect := cfg.AfterConnect
		cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
			if afterConnect != nil {
				if err := afterConnect(ctx, conn); err != nil {
					return err
				}
			}
			_, err := conn.Exec(ctx, "SET search_path = "+quoteIdentList(searchPath))
			return err
		}
	}

	if maxConns > 0 {
		cfg.MaxConns = int32(maxConns)
	}
	if maxIdle > 0 {
		cfg.MinConns = int32(maxIdle)
	}
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: connect: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return pool, nil
}

// extractSearchPath — return value `search_path` query param dari DSN, atau "" kalau tidak ada.
func extractSearchPath(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return ""
	}
	return u.Query().Get("search_path")
}

// quoteIdentList — quote setiap schema name dalam list comma-separated.
// "reservation,public" → "\"reservation\",\"public\""
// Pakai untuk SET search_path yang aman dari SQL injection.
func quoteIdentList(s string) string {
	out := ""
	cur := ""
	flush := func() {
		if cur == "" {
			return
		}
		if out != "" {
			out += ","
		}
		// Escape double-quote di dalam identifier (rare tapi safe).
		safe := ""
		for _, r := range cur {
			if r == '"' {
				safe += `""`
			} else {
				safe += string(r)
			}
		}
		out += `"` + safe + `"`
		cur = ""
	}
	for _, r := range s {
		if r == ',' {
			flush()
			continue
		}
		// Skip whitespace antara koma & nama
		if r == ' ' || r == '\t' {
			continue
		}
		cur += string(r)
	}
	flush()
	return out
}

// HealthCheck — siap dipakai di /healthz endpoint.
func HealthCheck(ctx context.Context, pool *pgxpool.Pool) error {
	c, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return pool.Ping(c)
}
