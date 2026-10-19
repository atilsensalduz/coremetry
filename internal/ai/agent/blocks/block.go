package blocks

import "fmt"

// block.go — v0.10.541 (CoSRE v2 Faz 3.3a): BLOK PROTOKOLÜ. Cevap düz metin
// yerine tipli bloklar taşır; SSE `event: block` çerçevesi:
//
//	{ id, type, seq, final, payload }
//
// Model YALNIZ metin üretir; yapısal bloklar (chart/link/trace_list/table/
// action/evidence) deterministik — tool sonucundan ya da sunucu link
// kurucusundan (v0.10.47 chart-origin kararının genellemesi). Eski
// `delta/answer` çerçeveleri bir sürüm boyunca PARALEL yayımlanır: eski
// istemci ve arşiv (chatPersist yalnız metin saklar) fence'i görmeye devam
// eder, yeni istemci tipli bloğu tercih eder.
const (
	TypeText      = "text"
	TypeTable     = "table"
	TypeChart     = "chart"
	TypeTraceList = "trace_list"
	TypeLink      = "link"
	TypeAction    = "action"
	TypeEvidence  = "evidence"
	// TypeOperation — operasyon adı ön-getirmesinin kartı (api/chat_operation_prefetch.go).
	TypeOperation = "operation"
	// TypeServiceReport — v0.10.1337: guided servis raporunun kartı
	// (api/copilot_service_report.go): hüküm, operasyonlar, teknik hatalar,
	// trace kırılımları + o trace'lerin logları, link çipleri.
	TypeServiceReport = "service_report"
	// TypeTopology — v0.10.1388: CoSRE servis topolojisi / örnek trace
	// topolojisi grafiği (api/copilot_service_topology.go; FE ChatTopologyCard).
	TypeTopology = "topology"
)

// Sequencer — bir turn içinde blok kimliği/sırası. SAF: emit'e verilecek
// gövdeyi döner; çağıran `emit("block", …)` ile yayımlar (withStepIDs'in
// adım sayacından bağımsız — çip kimlikleri `i`, bloklar `seq`).
type Sequencer struct{ n int }

func (s *Sequencer) Next(typ string, payload any) map[string]any {
	s.n++
	return map[string]any{
		"id": fmt.Sprintf("b%d", s.n), "type": typ, "seq": s.n, "final": true, "payload": payload,
	}
}

// Count — yayımlanan blok sayısı (testler + cevap künyesi).
func (s *Sequencer) Count() int { return s.n }
