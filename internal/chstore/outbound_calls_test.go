package chstore

// v0.10.1454 — outbound_call (R1) okuyucusunun SAF pinleri: yol deseni,
// MV ön süzgeç parçası, SQL sınırları ve external_paths.go ile kriter tekliği.

import (
	"strings"
	"testing"
)

func TestOutboundPathPattern(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"tam segment elips", "/esb/external/.../customer-kvkk", "/esb/external/%/customer-kvkk"},
		{"unicode elips", "/esb/external/…/customer-kvkk", "/esb/external/%/customer-kvkk"},
		{"yapışık elips", "/esb/ExternalX.../customer-kvkk", "/esb/ExternalX%/customer-kvkk"},
		{"yapışık unicode elips", "/esb/ExternalX…/customer-kvkk", "/esb/ExternalX%/customer-kvkk"},
		{"sondaki eğik çizgi", "/esb/external/rest/customer-kvkk/", "/esb/external/rest/customer-kvkk"},
		{"yer tutucu", "/orders/{id}/items", "/orders/%/items"},
		{"iki nokta yer tutucu", "/orders/:id/items", "/orders/%/items"},
		{"ham sayı normalize olur", "/orders/12345/items", "/orders/%/items"},
		{"joker", "/esb/*/x", "/esb/%/x"},
		{"alt çizgi kaçışlanır", "/esb/get_customer", `/esb/get\_customer`},
		{"yüzde kaçışlanır", "/esb/a%b", `/esb/a\%b`},
		{"ardışık joker tek %", "/esb/.../.../x", "/esb/%/x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := OutboundPathPattern(c.in); got != c.want {
				t.Errorf("OutboundPathPattern(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestOutboundPathNeedle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/esb/external/.../customer-kvkk", "customer-kvkk"},
		{"/esb/external/customer-kvkk/{id}", "customer-kvkk"},
		{"/esb/ExternalX...", "ExternalX"},
		{"/esb/ExternalX…", "ExternalX"},
		{"/orders/12345", "orders"},
		{"/a/b", ""},
		{"/x/*", ""},
	}
	for _, c := range cases {
		if got := OutboundPathNeedle(c.in); got != c.want {
			t.Errorf("OutboundPathNeedle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestOutboundSQLBounded — her ham okuma: FROM spans + PREWHERE client +
// service_name IN ön eki + DateTime64 sınırları + LIMIT + max_execution_time;
// URL / host / normalize ifadeleri external_paths.go sabitleriyle AYNI dize.
func TestOutboundSQLBounded(t *testing.T) {
	variants := map[string]string{
		"agg-split":   outboundSQL(outboundAggSel(true), "service_name, peer, path, win", "calls DESC", outboundAggLimit, true, true),
		"agg-single":  outboundSQL(outboundAggSel(false), "service_name, peer, path, win", "calls DESC", outboundAggLimit, false, false),
		"series":      outboundSQL(outboundSeriesSel(true), "b, svc", "b ASC", outboundSeriesLimit, false, true),
		"probe-raw":   outboundSQL("service_name, peer, count() AS calls", "service_name, peer", "calls DESC", outboundProbeLimit, true, false),
		"series-flat": outboundSQL(outboundSeriesSel(false), "b, svc", "b ASC", outboundSeriesLimit, true, false),
	}
	must := []string{
		"FROM spans",
		"PREWHERE kind = 'client' AND db_system = '' AND msg_system = ''",
		"WHERE service_name IN ? AND time >= toDateTime64(?, 9, 'UTC') AND time < toDateTime64(?, 9, 'UTC')",
		"LIMIT ",
		"SETTINGS max_execution_time = 8",
		externalPathURLSQL,
		externalPeerHostSQL + " AS peer",
		externalPathNormalizeSQL("bp"),
		"(path ILIKE ? OR path ILIKE ?)",
		"quantileTDigest(0.95)(duration)",
	}
	for name, sql := range variants {
		for _, m := range must {
			if name == "probe-raw" && strings.HasPrefix(m, "quantileTDigest") {
				continue
			}
			if !strings.Contains(sql, m) {
				t.Errorf("%s: %q yok", name, m)
			}
		}
		if strings.Contains(sql, " quantile(") {
			t.Errorf("%s: ham quantile() yasak", name)
		}
		// audit CHECK 6: LIMIT, FROM spans'ten sonraki 10 satır içinde.
		lines := strings.Split(sql, "\n")
		at := -1
		for i, l := range lines {
			if strings.Contains(l, "FROM spans") {
				at = i
			}
		}
		ok := false
		for i := at + 1; i < len(lines) && i <= at+10; i++ {
			if strings.Contains(lines[i], "LIMIT") {
				ok = true
			}
		}
		if !ok {
			t.Errorf("%s: LIMIT FROM spans'ten sonraki 10 satırda değil (audit CHECK 6)", name)
		}
	}
	if s := outboundSQL(outboundAggSel(false), "g", "o", 5, true, true); !strings.Contains(s, "AND deploy_env IN ?") || !strings.Contains(s, "AND peer = ?") {
		t.Error("env / peer süzgeci eklenmedi")
	}
	if s := outboundSQL(outboundAggSel(false), "g", "o", 5, false, false); strings.Contains(s, "deploy_env IN") || strings.Contains(s, "peer = ?") {
		t.Error("boş env / peer süzgeç eklememeli")
	}
	if !strings.Contains(outboundAggSel(true), "if(time >= toDateTime64(?, 9, 'UTC'), 'cur', 'prev')") {
		t.Error("cur/prev bölmesi DateTime64 bağı kullanmalı")
	}
}

// TestOutboundMVSQLBounded — MV okumaları: zaman sınırı + LIMIT + tavan, FROM spans YOK.
func TestOutboundMVSQLBounded(t *testing.T) {
	for name, sql := range map[string]string{
		"probe":     outboundProbeMVSQL(false),
		"probe-svc": outboundProbeMVSQL(true),
		"callers":   outboundExternalCallersSQL(),
	} {
		for _, m := range []string{"FROM topology_edges_5m FINAL", "node_kind = 'external'", "time_bucket >= ? AND time_bucket < ?", "LIMIT ", "SETTINGS max_execution_time = 5"} {
			if !strings.Contains(sql, m) {
				t.Errorf("%s: %q yok", name, m)
			}
		}
		if strings.Contains(sql, "FROM spans") {
			t.Errorf("%s: MV okuması ham spans'e gitmemeli", name)
		}
	}
	if !strings.Contains(outboundProbeMVSQL(true), "parent_service = ?") || strings.Contains(outboundProbeMVSQL(false), "parent_service = ?") {
		t.Error("servis süzgeci yalnız withSvc'de")
	}
	if !strings.Contains(outboundExternalCallersSQL(), "LIMIT 100") {
		t.Error("çağıran taraması ≤100")
	}
}

func TestOutboundTailArgs(t *testing.T) {
	q := OutboundQuery{PathLike: "/a/%", Services: []string{"orders-api"}, PeerHost: "esbprod.example.internal", Envs: []string{"prod"}}
	got := outboundTailArgs(q, q.From)
	if len(got) != 7 {
		t.Fatalf("bağ sayısı %d, want 7 (services, from, to, envs, pat, patSub, peer)", len(got))
	}
	if got[4] != "/a/%" || got[5] != "/a/%/%" || got[6] != "esbprod.example.internal" {
		t.Errorf("bağ sırası: %v", got)
	}
	if _, ok := got[1].(string); !ok {
		t.Error("DateTime64 bağı chDateTime64Arg dizesi olmalı")
	}
	q.Envs, q.PeerHost = nil, ""
	if n := len(outboundTailArgs(q, q.From)); n != 5 {
		t.Errorf("env/peer'siz bağ sayısı %d, want 5", n)
	}
}
