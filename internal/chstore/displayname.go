package chstore

import "strings"

// DisplaySpanName mirrors the frontend's displaySpanName helper so
// the multi-trace aggregator on the server bucket spans the same
// way the trace-detail UI labels them. Generic gRPC names ("grpc",
// "grpc command", bare rpc.method) and bare HTTP verbs get
// enriched from peer / route attributes; everything else passes
// through unchanged.
//
// Keep this in sync with frontend/src/lib/utils.ts:displaySpanName.
func DisplaySpanName(s *SpanRow) string {
	a := s.Attributes
	if a == nil {
		a = map[string]string{}
	}
	raw := strings.TrimSpace(s.Name)
	lc := strings.ToLower(raw)

	rpcService := a["rpc.service"]
	rpcMethod := a["rpc.method"]
	peer := a["peer.service"]

	serverAddr := firstNonEmpty3(a["server.address"], a["http.host"], a["net.peer.name"])
	urlPath := firstNonEmpty3(a["url.path"], a["http.target"], a["http.route"])

	httpVerbs := map[string]bool{
		"get": true, "post": true, "put": true, "delete": true,
		"patch": true, "head": true, "options": true,
	}
	if httpVerbs[lc] {
		if serverAddr != "" && urlPath != "" {
			return raw + " " + serverAddr + urlPath
		}
		if serverAddr != "" {
			return raw + " " + serverAddr
		}
		if urlPath != "" {
			return raw + " " + urlPath
		}
		return raw
	}

	generic := lc == "grpc" || lc == "grpc command" || lc == "rpc" || lc == "call" ||
		(rpcMethod != "" && lc == strings.ToLower(rpcMethod))
	if !generic {
		return raw
	}
	if rpcService != "" && rpcMethod != "" {
		if peer != "" && peer != rpcService {
			return peer + "/" + rpcService + "/" + rpcMethod
		}
		return rpcService + "/" + rpcMethod
	}
	if rpcMethod != "" && peer != "" {
		return peer + "." + rpcMethod
	}
	if peer != "" && s.ServiceName != "" {
		return s.ServiceName + " → " + peer
	}
	return raw
}

func firstNonEmpty3(a, b, c string) string {
	if a != "" {
		return a
	}
	if b != "" {
		return b
	}
	return c
}
