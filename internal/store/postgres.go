package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/karabulut18/scale-guard/internal/db"
)

// PostgreSQL implements the Store interface using sqlc-generated queries over pgx/v5.
//
// The architecture is two layers:
//   - internal/db  — sqlc-generated, owns the raw SQL strings and scanning logic.
//     Never touch these files manually.
//   - internal/store — this file. Adapts sqlc's generated types to the Store
//     interface types used by the rest of the application.
type PostgreSQL struct {
	conn    *pgx.Conn
	queries *db.Queries
}

// NewPostgreSQL opens a connection to PostgreSQL and returns a store.
func NewPostgreSQL(ctx context.Context, dsn string) (*PostgreSQL, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pgx.Connect: %w", err)
	}
	if err := conn.Ping(ctx); err != nil {
		conn.Close(ctx)
		return nil, fmt.Errorf("pgx.Ping: %w", err)
	}
	return &PostgreSQL{
		conn:    conn,
		queries: db.New(conn),
	}, nil
}

// LoadConfigs loads all active rate-limit configurations for a tenant.
func (ps *PostgreSQL) LoadConfigs(ctx context.Context, tenantID string) ([]*ClientConfig, error) {
	rows, err := ps.queries.LoadConfigs(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("LoadConfigs: %w", err)
	}

	configs := make([]*ClientConfig, len(rows))
	for i, r := range rows {
		configs[i] = &ClientConfig{
			TenantID:   r.TenantID,
			ClientID:   r.ClientID,
			Capacity:   r.Capacity,
			RefillRate: r.RefillRate,
		}
	}
	return configs, nil
}

// LoadBucketStates loads persisted token counts for all clients of a tenant.
func (ps *PostgreSQL) LoadBucketStates(ctx context.Context, tenantID string) ([]*BucketState, error) {
	rows, err := ps.queries.LoadBucketStates(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("LoadBucketStates: %w", err)
	}

	states := make([]*BucketState, len(rows))
	for i, r := range rows {
		states[i] = &BucketState{
			TenantID:     r.TenantID,
			ClientID:     r.ClientID,
			Tokens:       r.Tokens,
			LastRefillAt: r.LastRefillAt.Time, // pgtype.Timestamptz → time.Time
		}
	}
	return states, nil
}

// SaveBucketState upserts a batch of bucket states using sqlc's generated batch API.
// The entire batch is sent to PostgreSQL in a single round-trip.
func (ps *PostgreSQL) SaveBucketState(ctx context.Context, states []*BucketState) error {
	if len(states) == 0 {
		return nil
	}

	params := make([]db.UpsertBucketStateParams, len(states))
	for i, s := range states {
		params[i] = db.UpsertBucketStateParams{
			TenantID:     s.TenantID,
			ClientID:     s.ClientID,
			Tokens:       s.Tokens,
			LastRefillAt: timestamptz(s.LastRefillAt),
		}
	}

	var batchErr error
	results := ps.queries.UpsertBucketState(ctx, params)
	results.Exec(func(i int, err error) {
		if err != nil && batchErr == nil {
			batchErr = fmt.Errorf("upsert[%d] %s/%s: %w", i, states[i].TenantID, states[i].ClientID, err)
		}
	})

	return batchErr
}

// Health checks the database connection is alive.
func (ps *PostgreSQL) Health(ctx context.Context) error {
	return ps.conn.Ping(ctx)
}

// Close closes the underlying connection.
func (ps *PostgreSQL) Close() error {
	return ps.conn.Close(context.Background())
}

// Compile-time proof that PostgreSQL satisfies the Store interface.
var _ Store = (*PostgreSQL)(nil)

// timestamptz converts time.Time to the pgtype.Timestamptz that sqlc expects
// for TIMESTAMPTZ parameters in the generated batch API.
func timestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// ── Test helpers ───────────────────────────────────────────────────────────
// These are exported only for use by integration tests in this package.

func (ps *PostgreSQL) Exec(ctx context.Context, query string, args ...any) error {
	_, err := ps.conn.Exec(ctx, query, args...)
	return err
}

func (ps *PostgreSQL) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	return ps.conn.QueryRow(ctx, query, args...)
}
