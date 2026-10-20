package otlp

// grpc_metrics.go — v0.10.1422 (operatör: "Coremetry api, worker ve
// ingester için yap"). OTLP/gRPC alıcısının RPC süresi semconv
// rpc.server.duration olarak (rpc.system/service/method/grpc.status_code).
//
// KANCA, içe aktarma değil: bu paket selfobs'a metrik yazmak için bağlanmaz
// (selfobs.go başlığındaki self-loop kuralının ruhu — alıcı yolu kendi
// telemetri SDK'sını çağırmaz); main.go gözlemciyi SetRPCServerObserver ile
// takar. Takılı değilken interceptor tek bir atomik okuma maliyetindedir.
// Etiketler sınırlı: FullMethod yalnız kayıtlı üç servisin Export'u (bilinmeyen
// yöntem interceptor'a hiç gelmez — grpc Unimplemented'ı önce döner).

import (
	"context"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// RPCServerObserver — fullMethod "/pkg.Service/Method", code gRPC durum kodu.
type RPCServerObserver func(fullMethod string, code uint32, d time.Duration)

var rpcObserver atomic.Pointer[RPCServerObserver]

// SetRPCServerObserver — nil = çöz.
func SetRPCServerObserver(fn RPCServerObserver) {
	if fn == nil {
		rpcObserver.Store(nil)
		return
	}
	rpcObserver.Store(&fn)
}

func rpcObserveUnary(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	fn := rpcObserver.Load()
	if fn == nil {
		return handler(ctx, req)
	}
	start := time.Now()
	resp, err := handler(ctx, req)
	(*fn)(info.FullMethod, uint32(status.Code(err)), time.Since(start))
	return resp, err
}
