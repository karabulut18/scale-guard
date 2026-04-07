package grpcserver_test

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/karabulut18/scale-guard/internal/config"
	"github.com/karabulut18/scale-guard/internal/grpcserver"
	"github.com/karabulut18/scale-guard/internal/limiter"
	pb "github.com/karabulut18/scale-guard/internal/pb"
	"github.com/karabulut18/scale-guard/internal/store"
)

// ── Test infrastructure ────────────────────────────────────────────────────

// startTestServer spins up a real in-process gRPC server backed by a mock store.
// Returns a connected client and a cleanup function.
func startTestServer(t *testing.T, configs []*store.ClientConfig) (pb.RateLimitServiceClient, func()) {
	t.Helper()

	ms := &mockStore{configs: configs}
	cfg := &config.Config{
		FlushInterval:   50 * time.Millisecond,
		RefreshInterval: 1 * time.Hour,
		InstanceID:      "test",
	}

	ctx, cancel := context.WithCancel(context.Background())

	l := limiter.New(cfg, ms, []string{"t1"})
	l.Start(ctx)

	srv := grpcserver.New(l)

	lis, err := net.Listen("tcp", "127.0.0.1:0") // port 0 = OS picks a free port
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}

	// Serve takes the already-bound listener — no double-bind.
	go grpcserver.Serve(ctx, lis, srv) //nolint:errcheck

	conn, err := grpc.NewClient(
		lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}

	client := pb.NewRateLimitServiceClient(conn)

	cleanup := func() {
		conn.Close()
		cancel()
	}
	return client, cleanup
}

// mockStore — same as in limiter_test, duplicated intentionally to keep
// each package's tests self-contained (no shared test helpers across packages).
type mockStore struct {
	configs []*store.ClientConfig
}

func (m *mockStore) LoadConfigs(_ context.Context, tenantID string) ([]*store.ClientConfig, error) {
	var result []*store.ClientConfig
	for _, c := range m.configs {
		if c.TenantID == tenantID {
			result = append(result, c)
		}
	}
	return result, nil
}
func (m *mockStore) LoadBucketStates(_ context.Context, _ string) ([]*store.BucketState, error) {
	return nil, nil
}
func (m *mockStore) SaveBucketState(_ context.Context, _ []*store.BucketState) error { return nil }
func (m *mockStore) Health(_ context.Context) error                                   { return nil }
func (m *mockStore) Close() error                                                     { return nil }

// ── Tests ──────────────────────────────────────────────────────────────────

// TestCheckRateLimit_Allowed: requests within capacity return allowed=true.
func TestCheckRateLimit_Allowed(t *testing.T) {
	client, cleanup := startTestServer(t, []*store.ClientConfig{
		{TenantID: "t1", ClientID: "c1", Capacity: 10, RefillRate: 1.0},
	})
	defer cleanup()

	resp, err := client.CheckRateLimit(context.Background(), &pb.CheckRateLimitRequest{
		TenantId: "t1",
		ClientId: "c1",
	})
	if err != nil {
		t.Fatalf("CheckRateLimit: %v", err)
	}
	if !resp.Allowed {
		t.Fatalf("expected allowed=true, got false (reason: %s)", resp.Reason)
	}
}

// TestCheckRateLimit_Denied: exhausted bucket returns allowed=false with a reason.
func TestCheckRateLimit_Denied(t *testing.T) {
	client, cleanup := startTestServer(t, []*store.ClientConfig{
		{TenantID: "t1", ClientID: "c1", Capacity: 2, RefillRate: 0.01},
	})
	defer cleanup()

	ctx := context.Background()
	req := &pb.CheckRateLimitRequest{TenantId: "t1", ClientId: "c1"}

	client.CheckRateLimit(ctx, req) //nolint:errcheck
	client.CheckRateLimit(ctx, req) //nolint:errcheck

	resp, err := client.CheckRateLimit(ctx, req)
	if err != nil {
		t.Fatalf("CheckRateLimit: %v", err)
	}
	if resp.Allowed {
		t.Fatal("expected allowed=false after bucket exhausted")
	}
	if resp.Reason == "" {
		t.Error("expected non-empty reason when denied")
	}
}

// TestCheckRateLimit_MissingTenantID: empty tenant_id returns InvalidArgument.
func TestCheckRateLimit_MissingTenantID(t *testing.T) {
	client, cleanup := startTestServer(t, nil)
	defer cleanup()

	_, err := client.CheckRateLimit(context.Background(), &pb.CheckRateLimitRequest{
		TenantId: "",
		ClientId: "c1",
	})
	if err == nil {
		t.Fatal("expected error for empty tenant_id")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", status.Code(err))
	}
}

// TestCheckRateLimit_MissingClientID: empty client_id returns InvalidArgument.
func TestCheckRateLimit_MissingClientID(t *testing.T) {
	client, cleanup := startTestServer(t, nil)
	defer cleanup()

	_, err := client.CheckRateLimit(context.Background(), &pb.CheckRateLimitRequest{
		TenantId: "t1",
		ClientId: "",
	})
	if err == nil {
		t.Fatal("expected error for empty client_id")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", status.Code(err))
	}
}

// TestCheckRateLimit_FailOpen: unknown client is allowed (fail-open policy).
func TestCheckRateLimit_FailOpen(t *testing.T) {
	client, cleanup := startTestServer(t, nil) // no configs
	defer cleanup()

	resp, err := client.CheckRateLimit(context.Background(), &pb.CheckRateLimitRequest{
		TenantId: "t1",
		ClientId: "unknown",
	})
	if err != nil {
		t.Fatalf("CheckRateLimit: %v", err)
	}
	if !resp.Allowed {
		t.Fatal("expected allowed=true for unknown client (fail-open)")
	}
}
