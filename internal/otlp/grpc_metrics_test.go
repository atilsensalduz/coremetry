package otlp

// v0.10.1422 — operatör: "Coremetry api, worker ve ingester için yap".
// OTLP/gRPC interceptor kancası: takılıyken FullMethod + durum kodu + süre
// gözlemciye gider; takılı değilken handler'a dokunmadan geçer.

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRPCObserveUnary(t *testing.T) {
	t.Cleanup(func() { SetRPCServerObserver(nil) })
	info := &grpc.UnaryServerInfo{FullMethod: "/opentelemetry.proto.collector.trace.v1.TraceService/Export"}
	fail := func(context.Context, any) (any, error) { return nil, status.Error(codes.ResourceExhausted, "full") }
	ok := func(context.Context, any) (any, error) { return "r", nil }

	SetRPCServerObserver(nil)
	if r, err := rpcObserveUnary(context.Background(), nil, info, ok); r != "r" || err != nil {
		t.Fatal("no observer: handler result must pass through")
	}
	var gotM string
	var gotC uint32
	var gotD time.Duration = -1
	SetRPCServerObserver(func(m string, c uint32, d time.Duration) { gotM, gotC, gotD = m, c, d })
	if _, err := rpcObserveUnary(context.Background(), nil, info, fail); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("err = %v", err)
	}
	if gotM != info.FullMethod || gotC != uint32(codes.ResourceExhausted) || gotD < 0 {
		t.Fatalf("observed (%q,%d,%v)", gotM, gotC, gotD)
	}
	_, _ = rpcObserveUnary(context.Background(), nil, info, ok)
	if gotC != uint32(codes.OK) {
		t.Fatalf("ok code = %d", gotC)
	}
}
