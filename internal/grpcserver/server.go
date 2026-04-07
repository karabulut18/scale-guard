// Package grpcserver implements the RateLimitService gRPC server.
//
// It is a thin adapter: it validates the incoming request, delegates
// to the Limiter, and maps the result back to a proto response.
// All rate-limiting logic lives in the limiter package — this package
// owns only the network boundary.
package grpcserver

import (
	"context"
	"fmt"
	"log"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/karabulut18/scale-guard/internal/limiter"
	pb "github.com/karabulut18/scale-guard/internal/pb"
)

// Server implements pb.RateLimitServiceServer.
type Server struct {
	pb.UnimplementedRateLimitServiceServer
	limiter *limiter.Limiter
}

// New creates a Server backed by the given Limiter.
func New(l *limiter.Limiter) *Server {
	return &Server{limiter: l}
}

// CheckRateLimit is the hot-path RPC. It consumes one token from the caller's
// bucket and returns whether the request is permitted.
//
// Validation is intentionally minimal — empty tenant/client is a client error
// (codes.InvalidArgument). Everything else delegates to the limiter.
func (s *Server) CheckRateLimit(ctx context.Context, req *pb.CheckRateLimitRequest) (*pb.CheckRateLimitResponse, error) {
	if req.TenantId == "" || req.ClientId == "" {
		return nil, status.Error(codes.InvalidArgument, "tenant_id and client_id are required")
	}

	allowed := s.limiter.Allow(ctx, req.TenantId, req.ClientId)

	resp := &pb.CheckRateLimitResponse{
		Allowed: allowed,
	}
	if !allowed {
		resp.Reason = fmt.Sprintf("rate limit exceeded for %s/%s", req.TenantId, req.ClientId)
	}

	return resp, nil
}

// ListenAndServe starts the gRPC server on the given address (e.g. ":50051").
// It blocks until the context is cancelled, then performs a graceful stop.
func ListenAndServe(ctx context.Context, addr string, srv *Server) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("net.Listen: %w", err)
	}
	return Serve(ctx, lis, srv)
}

// Serve starts the gRPC server on an already-bound listener.
// Used by tests to bind on port 0 (OS-assigned) and retrieve the address
// before the server starts accepting connections.
func Serve(ctx context.Context, lis net.Listener, srv *Server) error {
	grpcSrv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			loggingInterceptor,
		),
	)
	pb.RegisterRateLimitServiceServer(grpcSrv, srv)

	// Graceful shutdown: when ctx is cancelled, stop accepting new RPCs
	// and wait for in-flight ones to finish.
	go func() {
		<-ctx.Done()
		grpcSrv.GracefulStop()
	}()

	log.Printf("INFO: gRPC server listening on %s", lis.Addr())
	return grpcSrv.Serve(lis)
}

// loggingInterceptor logs denied requests. Allowed requests are not logged
// to avoid flooding logs at high throughput.
func loggingInterceptor(
	ctx context.Context,
	req any,
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	resp, err := handler(ctx, req)
	if err != nil {
		return resp, err
	}
	if r, ok := resp.(*pb.CheckRateLimitResponse); ok && !r.Allowed {
		log.Printf("DENY: %s", r.Reason)
	}
	return resp, nil
}
