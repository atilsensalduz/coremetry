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
	"strings"

	"github.com/cilcenk/coremetry/internal/devops"
)

// wantOperationCode — SAF: stack yolu sonucu + girdi → operasyon kodu yolu mu?
// Stack metni doluysa (frame'siz bile) DEĞİL: o yolun cevabı zaten verildi.
func wantOperationCode(in traceExplainInput, cc devops.CodeContext) bool {
	return strings.TrimSpace(in.Stack) == "" && cc.Empty() && cc.Outcome == devops.CodeNoStack &&
		devops.ValidOperationCode(strings.TrimSpace(in.MainOperation))
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
	return s.devops.FetchOperationCode(ctx, devops.OperationCodeRequest{
		Repo: res.Repo, Hint: res.Project, Service: service, Version: in.RootVersion,
		Operation: op, Others: len(in.OtherOperations),
	})
}

// operationLookupState — SAF: ön yüzün durum satırı için operasyon kodu
// aramasının hâli. "" = operasyon yolu koşmadı.
func operationLookupState(cc devops.CodeContext) string {
	switch {
	case cc.Source != devops.CodeSourceOperation:
		return ""
	case !cc.Empty():
		return "ok"
	case cc.Outcome == devops.CodeNoStack:
		return "off"
	}
	return "failed"
}
