package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// PostgreSQL implements the Store interface using pgx (high-performance PostgreSQL driver).
type PostgreSQL struct {
	conn *pgx.Conn
}

// NewPostgreSQL opens a connection to PostgreSQL and returns a store.
func NewPostgreSQL(ctx context.Context, dsn string) (*PostgreSQL, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pgx.Connect failed: %w", err)
	}

	// Test the connection with a simple ping.
	if err := conn.Ping(ctx); err != nil {
		conn.Close(ctx)
		return nil, fmt.Errorf("pgx.Ping failed: %w", err)
	}

	return &PostgreSQL{conn: conn}, nil
}

// LoadConfigs loads all active rate-limit configurations for a tenant from the database.
// This query is run once at startup and every 30 seconds by the config-watcher goroutine.
//
// The query selects from rate_limit_configs where is_active=TRUE, ordered by client_id
// for deterministic ordering (helps with testing and debugging).
func (ps *PostgreSQL) LoadConfigs(ctx context.Context, tenantID string) ([]*ClientConfig, error) {
	query := `
		SELECT tenant_id, client_id, capacity, refill_rate
		FROM   rate_limit_configs
		WHERE  tenant_id = $1 AND is_active = TRUE
		ORDER BY client_id
	`

	rows, err := ps.conn.Query(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	var configs []*ClientConfig
	for rows.Next() {
		var cfg ClientConfig
		if err := rows.Scan(&cfg.TenantID, &cfg.ClientID, &cfg.Capacity, &cfg.RefillRate); err != nil {
			return nil, fmt.Errorf("scan failed: %w", err)
		}
		configs = append(configs, &cfg)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration failed: %w", err)
	}

	return configs, nil
}

// LoadBucketStates loads the persisted token counts and refill timestamps for all
// clients of a tenant. Used once at startup for state restoration.
func (ps *PostgreSQL) LoadBucketStates(ctx context.Context, tenantID string) ([]*BucketState, error) {
	query := `
		SELECT tenant_id, client_id, tokens, last_refill_at
		FROM   bucket_states
		WHERE  tenant_id = $1
		ORDER  BY client_id
	`

	rows, err := ps.conn.Query(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	var states []*BucketState
	for rows.Next() {
		var s BucketState
		if err := rows.Scan(&s.TenantID, &s.ClientID, &s.Tokens, &s.LastRefillAt); err != nil {
			return nil, fmt.Errorf("scan failed: %w", err)
		}
		states = append(states, &s)
	}

	return states, rows.Err()
}

// SaveBucketState upserts a batch of bucket states to the database in a single batch operation.
// This is called every 100ms by the write-behind flusher.
//
// The query uses PostgreSQL's ON CONFLICT ... DO UPDATE pattern to handle both inserts
// (first write for a client) and updates (subsequent flushes) without a separate branch in code.
// This is much faster than checking existence first.
//
// IMPORTANT: This is not on the hot path. It's called by a background goroutine.
// Latency here doesn't directly affect request latency, as long as the flusher doesn't
// fall behind (which would indicate a DB problem or load spike).
func (ps *PostgreSQL) SaveBucketState(ctx context.Context, states []*BucketState) error {
	if len(states) == 0 {
		return nil // no-op if empty
	}

	// Use a batch to send multiple UPSERTs in one round-trip.
	batch := &pgx.Batch{}

	for _, state := range states {
		query := `
			INSERT INTO bucket_states (tenant_id, client_id, tokens, last_refill_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (tenant_id, client_id)
			DO UPDATE SET
				tokens         = EXCLUDED.tokens,
				last_refill_at = EXCLUDED.last_refill_at,
				updated_at     = NOW()
		`
		batch.Queue(query, state.TenantID, state.ClientID, state.Tokens, state.LastRefillAt)
	}

	results := ps.conn.SendBatch(ctx, batch)
	defer results.Close()

	// Iterate through results to catch any errors from individual UPSERTs.
	for i := range states {
		if _, err := results.Exec(); err != nil {
			return fmt.Errorf("upsert %d failed: %w", i, err)
		}
	}

	return nil
}

// Health checks the database connection is alive.
func (ps *PostgreSQL) Health(ctx context.Context) error {
	return ps.conn.Ping(ctx)
}

// Close closes the database connection.
func (ps *PostgreSQL) Close() error {
	return ps.conn.Close(context.Background())
}

// Ensure PostgreSQL implements the Store interface at compile time.
var _ Store = (*PostgreSQL)(nil)

// PostgreSQL specific helpers for testing.

// Exec is a helper for running a single query without returning rows.
// Used in tests to set up or tear down state.
func (ps *PostgreSQL) Exec(ctx context.Context, query string, args ...interface{}) error {
	_, err := ps.conn.Exec(ctx, query, args...)
	return err
}

// QueryRow is a helper for running a single query and returning one row.
// Used in tests to verify state.
func (ps *PostgreSQL) QueryRow(ctx context.Context, query string, args ...interface{}) pgx.Row {
	return ps.conn.QueryRow(ctx, query, args...)
}

// Connection returns the underlying pgx.Conn for test fixtures that need direct access.
func (ps *PostgreSQL) Connection() *pgx.Conn {
	return ps.conn
}
