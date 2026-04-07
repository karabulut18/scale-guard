package store_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/karabulut18/scale-guard/internal/store"
)

// requireDocker skips the test if Docker is not available.
// Integration tests that spin up a real PostgreSQL container via testcontainers
// need Docker running. Unit tests (bucket, config, limiter) never need this.
func requireDocker(t *testing.T) {
	t.Helper()
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("Docker not available — skipping integration test")
	}
}

// setupTestDB starts a PostgreSQL container, runs migrations, and returns the DSN.
// The caller is responsible for calling Close() on the returned cleanup function.
func setupTestDB(t *testing.T, ctx context.Context) (string, func()) {
	req := testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     "test",
			"POSTGRES_PASSWORD": "test",
			"POSTGRES_DB":       "scale_guard",
		},
		WaitingFor: wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).
			WithStartupTimeout(30 * time.Second),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start postgres container: %v", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("failed to get container host: %v", err)
	}

	port, err := container.MappedPort(ctx, "5432")
	if err != nil {
		t.Fatalf("failed to get mapped port: %v", err)
	}

	dsn := "postgres://test:test@" + host + ":" + port.Port() + "/scale_guard"

	// Wait for the database to be accessible
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("failed to connect to test database: %v", err)
	}

	// Run migrations
	migrationsDir := filepath.Join("..", "..", "db", "migrations")
	migrationsDir, err = filepath.Abs(migrationsDir)
	if err != nil {
		t.Fatalf("failed to resolve migrations dir: %v", err)
	}

	// For now, manually run the schema (in a real project, use golang-migrate)
	// Read and execute migration files
	schema := `
		-- Migration 001: rate_limit_configs
		CREATE TABLE rate_limit_configs (
			id          BIGSERIAL       PRIMARY KEY,
			tenant_id   TEXT            NOT NULL,
			client_id   TEXT            NOT NULL,
			capacity    INTEGER         NOT NULL CHECK (capacity > 0),
			refill_rate NUMERIC(12, 4)  NOT NULL CHECK (refill_rate > 0),
			is_active   BOOLEAN         NOT NULL DEFAULT TRUE,
			created_at  TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
			updated_at  TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
			CONSTRAINT uq_rate_limit_configs_tenant_client
				UNIQUE (tenant_id, client_id)
		);

		CREATE INDEX idx_rlc_tenant_active
			ON rate_limit_configs (tenant_id)
			WHERE is_active = TRUE;

		CREATE OR REPLACE FUNCTION fn_set_updated_at()
		RETURNS TRIGGER LANGUAGE plpgsql AS $$
		BEGIN
			NEW.updated_at = NOW();
			RETURN NEW;
		END;
		$$;

		CREATE TRIGGER trg_rate_limit_configs_updated_at
			BEFORE UPDATE ON rate_limit_configs
			FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

		-- Migration 002: bucket_states
		CREATE TABLE bucket_states (
			id             BIGSERIAL       PRIMARY KEY,
			tenant_id      TEXT            NOT NULL,
			client_id      TEXT            NOT NULL,
			tokens         NUMERIC(12, 4)  NOT NULL,
			last_refill_at TIMESTAMPTZ     NOT NULL,
			updated_at     TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
			CONSTRAINT uq_bucket_states_tenant_client
				UNIQUE (tenant_id, client_id),
			CONSTRAINT fk_bucket_states_config
				FOREIGN KEY (tenant_id, client_id)
				REFERENCES rate_limit_configs (tenant_id, client_id)
				ON DELETE CASCADE
		);

		CREATE INDEX idx_bucket_states_tenant_client
			ON bucket_states (tenant_id, client_id);

		CREATE TRIGGER trg_bucket_states_updated_at
			BEFORE UPDATE ON bucket_states
			FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();
	`

	if _, err := conn.Exec(ctx, schema); err != nil {
		conn.Close(ctx)
		t.Fatalf("failed to run migrations: %v", err)
	}

	conn.Close(ctx)

	// Return DSN and cleanup function
	return dsn, func() {
		_ = container.Terminate(ctx)
	}
}

// TestLoadConfigs: LoadConfigs should return all active configs for a tenant.
func TestLoadConfigs(t *testing.T) {
	requireDocker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	dsn, cleanup := setupTestDB(t, ctx)
	defer cleanup()

	ps, err := store.NewPostgreSQL(ctx, dsn)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer ps.Close()

	// Insert test data
	err = ps.Exec(ctx, `
		INSERT INTO rate_limit_configs (tenant_id, client_id, capacity, refill_rate)
		VALUES
			('tenant_a', 'client_1', 100, 10.0),
			('tenant_a', 'client_2', 200, 20.0),
			('tenant_b', 'client_1', 50, 5.0)
	`)
	if err != nil {
		t.Fatalf("failed to insert test data: %v", err)
	}

	// Load configs for tenant_a
	configs, err := ps.LoadConfigs(ctx, "tenant_a")
	if err != nil {
		t.Fatalf("LoadConfigs failed: %v", err)
	}

	if len(configs) != 2 {
		t.Fatalf("expected 2 configs for tenant_a, got %d", len(configs))
	}

	// Verify the configs (ordered by client_id)
	if configs[0].ClientID != "client_1" || configs[0].Capacity != 100 || configs[0].RefillRate != 10.0 {
		t.Errorf("config 0 mismatch: %+v", configs[0])
	}
	if configs[1].ClientID != "client_2" || configs[1].Capacity != 200 || configs[1].RefillRate != 20.0 {
		t.Errorf("config 1 mismatch: %+v", configs[1])
	}
}

// TestSaveBucketState: SaveBucketState should upsert states correctly.
func TestSaveBucketState(t *testing.T) {
	requireDocker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	dsn, cleanup := setupTestDB(t, ctx)
	defer cleanup()

	ps, err := store.NewPostgreSQL(ctx, dsn)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer ps.Close()

	// Insert a rate_limit_config (foreign key requirement)
	err = ps.Exec(ctx, `
		INSERT INTO rate_limit_configs (tenant_id, client_id, capacity, refill_rate)
		VALUES ('tenant_x', 'client_x', 100, 10.0)
	`)
	if err != nil {
		t.Fatalf("failed to insert config: %v", err)
	}

	now := time.Now()
	states := []*store.BucketState{
		{TenantID: "tenant_x", ClientID: "client_x", Tokens: 50.5, LastRefillAt: now},
	}

	// First save (INSERT)
	err = ps.SaveBucketState(ctx, states)
	if err != nil {
		t.Fatalf("SaveBucketState (insert) failed: %v", err)
	}

	// Verify the insert
	var tokens float64
	var lastRefillAt time.Time
	err = ps.QueryRow(ctx, `
		SELECT tokens, last_refill_at
		FROM bucket_states
		WHERE tenant_id = $1 AND client_id = $2
	`, "tenant_x", "client_x").Scan(&tokens, &lastRefillAt)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if tokens != 50.5 {
		t.Errorf("expected tokens=50.5, got %v", tokens)
	}

	// Second save (UPDATE)
	states[0].Tokens = 25.3
	states[0].LastRefillAt = now.Add(100 * time.Millisecond)
	err = ps.SaveBucketState(ctx, states)
	if err != nil {
		t.Fatalf("SaveBucketState (update) failed: %v", err)
	}

	// Verify the update
	err = ps.QueryRow(ctx, `
		SELECT tokens, last_refill_at
		FROM bucket_states
		WHERE tenant_id = $1 AND client_id = $2
	`, "tenant_x", "client_x").Scan(&tokens, &lastRefillAt)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if tokens != 25.3 {
		t.Errorf("expected tokens=25.3 after update, got %v", tokens)
	}
}

// TestHealth: Health should return nil if the database is healthy.
func TestHealth(t *testing.T) {
	requireDocker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	dsn, cleanup := setupTestDB(t, ctx)
	defer cleanup()

	ps, err := store.NewPostgreSQL(ctx, dsn)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer ps.Close()

	err = ps.Health(ctx)
	if err != nil {
		t.Fatalf("Health check failed: %v", err)
	}
}
