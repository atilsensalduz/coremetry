package api

// copilot_code_operation.go — v0.10.1330 (operatör: trace explain "Kod
// incelemesi" stacktrace'siz trace'te "Kod okunamadı — kodsuz analiz. (bu
// kayıtta stacktrace yok)" diyordu). Stack YOKKEN trace'in ANA operasyonunun
// (v0.10.1329 pickMainOperation → traceExplainInput.MainOperation) kodu
// DevOps Code Search ile bulunur ve kod incelemesine giriş metodu + ilk
// yönetici çağrısı olarak verilir (devops.FetchOperationCode). Yalnız ana
// operasyon; diğerleri "+N diğer operasyon okunmadı". Stack yolu bayt bayt
// aynı: tetik yalnız stack metni BOŞKEN.

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/devops"
)

// wantOperationCode — SAF: stack yolu sonucu + girdi → operasyon kodu yolu mu?
// Stack metni doluysa (frame'siz bile) DEĞİL: o yolun cevabı zaten verildi.
//
// v0.10.1349 (operatör-bildirimli): geçerli MainOperation şartı KALKTI —
// hatasız trace'te kesin operasyon yokken de yola girilir; orada fonksiyon
// kodu adayları ya da dürüst "operasyon adı bulunamadı" cevabı üretilir.
func wantOperationCode(in traceExplainInput, cc devops.CodeContext) bool {
	return strings.TrimSpace(in.Stack) == "" && cc.Empty() && cc.Outcome == devops.CodeNoStack
}

// traceCodeContext — trace explain'in kod bağlamı: önce stack yolu
// (buildCodeContext, değişmedi), stack yoksa ana operasyonun kodu.
func (s *Server) traceCodeContext(ctx context.Context, in traceExplainInput) devops.CodeContext {
	cc := s.buildCodeContext(ctx, in.StackService, in.Stack, in.StackVersion)
	if wantOperationCode(in, cc) {
		cc = s.buildOperationCodeContext(ctx, in)
	}
	return cc
}

// buildOperationCodeContext — Code Search kapısı + kök servisin deposu
// (katalog pini fail-closed, yoksa ad konvansiyonu — buildCodeContext ile
// aynı karar) → devops.FetchOperationCode. Sayaç ARTMAZ: bu istek stack
// yolunda no-stack olarak zaten sayıldı.
func (s *Server) buildOperationCodeContext(ctx context.Context, in traceExplainInput) devops.CodeContext {
	op := strings.TrimSpace(in.MainOperation)
	out := devops.CodeContext{Source: devops.CodeSourceOperation, Outcome: devops.CodeNoStack, OperationOthers: len(in.OtherOperations)}
	if s.devops == nil || !s.devops.CodeSearchEnabled() {
		out.Reason = devops.OperationSearchOffReason
		return out
	}
	service := strings.TrimSpace(in.RootService)
	pin := ""
	if service != "" && s.store != nil {
		md, err := s.store.GetServiceMetadataStrict(ctx, service)
		mdRepo := ""
		if md != nil {
			mdRepo = md.Repository
		}
		p, abort := pinReadDecision(mdRepo, md != nil, err)
		if abort != "" {
			out.Outcome, out.Reason = devops.CodeCatalogError, abort
			return out
		}
		pin = p
	}
	res := devops.ResolveRepo(service, pin, s.devops.ResolveConfig())
	if res.Repo == "" {
		reason := res.Reason
		if reason == "" {
			reason = "servis için depo çözülemedi"
		}
		out.Outcome, out.Reason = devops.CodeRepoUnresolved, reason
		return out
	}
	if devops.ValidOperationCode(op) {
		return s.devops.FetchOperationCode(ctx, devops.OperationCodeRequest{
			Repo: res.Repo, Hint: res.Project, Service: service, Version: in.RootVersion,
			Operation: op, Others: len(in.OtherOperations), Source: in.OperationSource,
		})
	}
	// v0.10.1349 — kesin operasyon yok: fonksiyon kodu sözlüğünden ADAYLAR.
	tried, rest := operationCandidates(in.FunctionCodes, s.functionCodeOps(ctx, in.FunctionCodes), devops.OperationCandidateMax)
	if len(tried) == 0 {
		out.OperationOthers = 0
		out.Reason = devops.OperationNoneReason
		return out
	}
	unread := append(append([]string(nil), tried[1:]...), rest...)
	cc := s.devops.FetchOperationCodeCandidates(ctx, devops.OperationCodeRequest{
		Repo: res.Repo, Hint: res.Project, Service: service, Version: in.RootVersion,
		Others: len(unread), Source: devops.OperationSourceFunctionCode,
	}, tried)
	// Okunmayanlar: kodu okunan aday DIŞINDAKİ denenen adaylar + kalanlar.
	cc.OperationUnread = nil
	for _, c := range append(append([]string(nil), tried...), rest...) {
		if c != cc.Operation {
			cc.OperationUnread = append(cc.OperationUnread, c)
		}
	}
	if cc.Operation == "" {
		cc.OperationUnread = rest
	}
	return cc
}

// fnCodeDictReader — v0.10.1349: fonksiyon kodu → Oracle operasyon çiftleri
// (chstore.Store.OracleOperationsForFunctionCodes). Test dikişi.
type fnCodeDictReader interface {
	OracleOperationsForFunctionCodes(ctx context.Context, codes, codeSources []string, from, to time.Time, perCode int) ([]chstore.OracleFnOp, error)
}

// fnCodeDictSeams — *Server → fnCodeDictReader (yalnız testler yazar). Server
// struct'ına alan EKLENMEZ: api.go büyümez (TestApiGoDoesNotGrow).
var fnCodeDictSeams sync.Map

// fnCodeDict — sözlük okuyucusu: test dikişi > s.store (Oracle kaynağı açıksa);
// yoksa nil (aday yok).
func (s *Server) fnCodeDict() fnCodeDictReader {
	if v, ok := fnCodeDictSeams.Load(s); ok {
		return v.(fnCodeDictReader)
	}
	if !s.oracleOpsEnabled() {
		return nil
	}
	return s.store
}

const (
	// fnCodeDictTimeout — sözlük sorgusunun kendi bütçesi (explain bekliyor).
	fnCodeDictTimeout = 3 * time.Second
	// fnCodeDictPerCode — kod başına operasyon (aday 2 + okunmayan ≤3).
	fnCodeDictPerCode = 5
	// operationCandidateRestMax — okunmayan aday listesi tavanı.
	operationCandidateRestMax = 3
)

// functionCodeOps — trace'in fonksiyon kodları için sözlük çiftleri; çipin
// penceresi (son 7 gün), 3 sn. Hata / kapalı / kod yok → nil (sessiz).
func (s *Server) functionCodeOps(ctx context.Context, codes []string) []chstore.OracleFnOp {
	d := s.fnCodeDict()
	if d == nil || len(codes) == 0 {
		return nil
	}
	dctx, cancel := context.WithTimeout(ctx, fnCodeDictTimeout)
	defer cancel()
	now := time.Now()
	pairs, err := d.OracleOperationsForFunctionCodes(dctx, codes, s.oracleOpScope().CodeSources, now.Add(-oracleOpSearchWindow), now, fnCodeDictPerCode)
	if err != nil {
		return nil
	}
	return pairs
}

// operationCandidates — SAF, tablo-testli (v0.10.1349): fonksiyon kodları
// (kanıt sırasıyla, kök kodu önce) + sözlük çiftleri → denenecek adaylar
// (≤max) ve okunmayan kalanlar (≤3). Her kodun operasyonları satır sayısına
// göre (çok → az, eşitlikte ad); kodlar arasında sırayla örülür (kök kodun
// 1. adayı, 2. kodun 1. adayı, …, kök kodun 2. adayı …). Tekil;
// ValidOperationCode'dan geçmeyen ad atılır.
func operationCandidates(fnCodes []string, pairs []chstore.OracleFnOp, max int) (tried, rest []string) {
	if max <= 0 {
		max = devops.OperationCandidateMax
	}
	byCode := map[string][]chstore.OracleFnOp{}
	for _, p := range pairs {
		op := strings.TrimSpace(p.Operation)
		if !devops.ValidOperationCode(op) {
			continue
		}
		p.Operation = op
		byCode[strings.TrimSpace(p.Code)] = append(byCode[strings.TrimSpace(p.Code)], p)
	}
	var lists [][]string
	seenCode := map[string]bool{}
	for _, c := range fnCodes {
		c = strings.TrimSpace(c)
		if c == "" || seenCode[c] {
			continue
		}
		seenCode[c] = true
		ops := byCode[c]
		sort.SliceStable(ops, func(i, j int) bool {
			if ops[i].Rows != ops[j].Rows {
				return ops[i].Rows > ops[j].Rows
			}
			return ops[i].Operation < ops[j].Operation
		})
		names := make([]string, 0, len(ops))
		for _, o := range ops {
			names = append(names, o.Operation)
		}
		lists = append(lists, names)
	}
	var all []string
	seen := map[string]bool{}
	for round := 0; ; round++ {
		more := false
		for _, l := range lists {
			if round >= len(l) {
				continue
			}
			more = true
			if op := l[round]; !seen[op] {
				seen[op] = true
				all = append(all, op)
			}
		}
		if !more || len(all) >= max+operationCandidateRestMax {
			break
		}
	}
	if len(all) > max+operationCandidateRestMax {
		all = all[:max+operationCandidateRestMax]
	}
	if len(all) <= max {
		return all, nil
	}
	return all[:max], all[max:]
}

// operationLookupState — SAF: ön yüzün durum satırı için operasyon kodu
// aramasının hâli. "" = operasyon yolu koşmadı. v0.10.1349: "none" = ne
// kesin operasyon ne fonksiyon kodu adayı var (arama hiç koşmadı).
func operationLookupState(cc devops.CodeContext) string {
	switch {
	case cc.Source != devops.CodeSourceOperation:
		return ""
	case !cc.Empty():
		return "ok"
	case cc.Outcome == devops.CodeNoStack && cc.Reason == devops.OperationNoneReason:
		return "none"
	case cc.Outcome == devops.CodeNoStack:
		return "off"
	}
	return "failed"
}
