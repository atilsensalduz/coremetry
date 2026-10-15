package chstore

// v0.10.1196 (operatör-bildirimi, cm-get-service "Where the time goes"):
// iç span'ler düzleşir, dış çağrı hedefiyle adlanır, backends yalnız route
// altındaki aşağı akış içinden, doğrudan DB iki listede birden çıkmaz.

import "testing"

func TestFoldEndpointEdges(t *testing.T) {
	entry := map[string]struct{}{"E": {}}
	spans := []walkSpan{
		{sid: "R", svc: "gateway", durMs: 900},                                    // kök (çağıran)
		{sid: "E", pid: "R", svc: "cm-get-service", durMs: 800},                   // route
		{sid: "I", pid: "E", svc: "cm-get-service", kind: "internal", durMs: 700}, // iç sarmalayıcı
		{sid: "S3", pid: "I", svc: "cm-get-service", kind: "client", rpcSvc: "S3", peer: "10.1.2.3", durMs: 600},
		{sid: "RD", pid: "E", svc: "cm-get-service", kind: "client", dbSys: "redis", durMs: 2},
		{sid: "A", pid: "I", svc: "account", kind: "server", durMs: 50},
		{sid: "ORA", pid: "A", svc: "account", kind: "client", dbSys: "oracle", durMs: 40},
		{sid: "X", pid: "R", svc: "gateway", kind: "client", dbSys: "db2", durMs: 30}, // route DIŞI kardeş dal
	}
	down, callers, backends, childMs := foldEndpointEdges("cm-get-service", spans, entry)
	if down["cm-get-service"] != nil {
		t.Error("iç span servis adıyla kenar olmamalı")
	}
	if a := down["S3 (10.1.2.3)"]; a == nil || a.kind != "external" || a.sumMs != 600 {
		t.Errorf("S3 hedefiyle adlanmalı: %+v", down)
	}
	if down["redis"] == nil || down["account"] == nil || len(down) != 3 {
		t.Errorf("kenarlar: %+v", down)
	}
	if childMs != 652 {
		t.Errorf("childMs %v", childMs)
	}
	if backends["oracle"] == nil || backends["redis"] != nil || backends["db2"] != nil {
		t.Errorf("backends yalnız aşağı akış içi (oracle): %+v", backends)
	}
	if callers["gateway"] == nil {
		t.Error("kök çağıran")
	}
}

func TestEdgeNameFallbacks(t *testing.T) {
	if n, k := edgeName(walkSpan{svc: "a", kind: "client", peer: "s3.amazonaws.com"}, "a"); n != "s3.amazonaws.com" || k != "external" {
		t.Errorf("%s %s", n, k)
	}
	if n, _ := edgeName(walkSpan{svc: "a", kind: "client"}, "a"); n != "external (adres yok)" {
		t.Error(n)
	}
	if n, k := edgeName(walkSpan{svc: "a", msgSys: "kafka", kind: "producer"}, "a"); n != "kafka" || k != "messaging" {
		t.Errorf("%s %s", n, k)
	}
}
