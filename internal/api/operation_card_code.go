package api

// operation_card_code.go — v0.10.1506 (plan-operation-card §2.3.2 + §4.1,
// R4): GET /api/endpoints/operation-card?…&code=1 dalı — kartın KOD yarısı.
// Rota yeni değil (operation_card.go aynı uç; ayrı önbellek anahtarı code
// bayrağını zaten taşıyordu), bu dosya yalnız `code` alanını kurar.
//
// DEPO KARARI buildOperationCodeContext ile AYNI: servis kataloğu pini
// fail-CLOSED (pinReadDecision — okunamayan pin konvansiyona düşmez, başka
// bir uygulamanın kodunu "kanıt" diye göstermemek için), yoksa ad
// konvansiyonu (devops.ResolveRepo). Sürüm = trace yarısının çoğunluk
// sürümü (tree.Version) → kod o sürümün commit'inden; çağrı yerleri
// tree.Children ile eşlenir. Trace yarısı İKİNCİ KEZ CH'ye gitmez: code=0
// anahtarının önbellek girdisi (cachedJSON; yoksa singleflight'lı tek
// üretim) okunur.
//
// GÖVDE TARAYICIYA GİTMEZ (karar 8, v0.10.1451; codeContextPayload
// sözleşmesi): operationCardCodeDTO yalnız konum (dosya:satır, DevOps
// linki), imza, DTO alan adı+tipi, çağrı yerinin alıcı.metot'u ve koddaki
// literal rota/topic taşır. devops.OperationCardCode.Windows (satır
// numaralı kaynak) bu tipe HİÇ kopyalanmaz — R5 anlatımı modele, yalnız
// localOnly profil üzerinden götürecek. Pin:
// TestOperationCardCodePayloadCarriesNoCodeBodies. R4 hiçbir modele bir şey
// göndermez; bu yüzden localOnly kapısı burada aranmaz (kod okunur ama
// yalnız şema düzeyi bilgi tarayıcıya çıkar — operatör kararı, plan §10
// soru 2).
//
// SINIRLAR devops katmanında: kart başına ≤4 Code Search + ≤8 dosya,
// toplam ≤15 sn (operationDeadline), sonuç 10 dk süreç-içi önbellek.
// Code Search kapalı / DevOps yok → dürüst `reason` satırı; kart trace
// yarısıyla yaşar (500 yok).

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/devops"
)

// operationCardCodeDTO — `code` alanı (FE: lib/types.ts OperationCardCode).
// Kod GÖVDESİ yok.
type operationCardCodeDTO struct {
	// Outcome — devops.CodeOutcome taksonomisi; Code Search kapalıyken
	// "search-off".
	Outcome   string                     `json:"outcome"`
	Reason    string                     `json:"reason,omitempty"`
	Repo      string                     `json:"repo,omitempty"`
	Branch    string                     `json:"branch,omitempty"`
	Source    string                     `json:"source,omitempty"` // pin | convention
	BrowseURL string                     `json:"browseUrl,omitempty"`
	Version   string                     `json:"version,omitempty"`
	Commit    string                     `json:"commit,omitempty"`
	Entry     *devops.OperationCardEntry `json:"entry"`
	Request   []devops.DTOField          `json:"request"`
	Response  []devops.DTOField          `json:"response"`
	CallSites []devops.CallSite          `json:"callSites"`
	Stats     opCardCodeStats            `json:"stats"`
	// Usages — v0.10.1509 "Bu metodu kim kullanıyor?" (ref: araması, yalnız
	// konum, "olası"); giriş metodu bulunamadıysa null.
	Usages *devops.MethodUsages `json:"usages"`
}

type opCardCodeStats struct {
	Fetched  int `json:"fetched"`
	Searches int `json:"searches"`
}

// opCardSearchOff — Code Search kapalı sınıfının tel adı.
const opCardSearchOff = "search-off"

// newOperationCardCodeDTO — boş ama nil-dilimsiz DTO (FE .map()).
func newOperationCardCodeDTO(outcome, reason string) *operationCardCodeDTO {
	return &operationCardCodeDTO{
		Outcome: outcome, Reason: reason,
		Request: []devops.DTOField{}, Response: []devops.DTOField{}, CallSites: []devops.CallSite{},
	}
}

// operationCardCodeFrom — SAF: devops sonucu → tel. Windows KOPYALANMAZ.
func operationCardCodeFrom(cc devops.OperationCardCode, source string) *operationCardCodeDTO {
	outcome := string(cc.Outcome)
	if cc.SearchOff {
		outcome = opCardSearchOff
	}
	d := newOperationCardCodeDTO(outcome, cc.Reason)
	d.Repo, d.Branch, d.Source, d.BrowseURL = cc.Repo, cc.Branch, source, cc.BrowseURL
	if cc.Revision != nil && cc.Revision.Verified {
		d.Version, d.Commit = cc.Revision.Version, cc.Revision.SHA
	}
	d.Entry = cc.Entry
	if cc.Request != nil {
		d.Request = cc.Request
	}
	if cc.Response != nil {
		d.Response = cc.Response
	}
	if cc.CallSites != nil {
		d.CallSites = cc.CallSites
	}
	d.Stats = opCardCodeStats{Fetched: cc.Stats.Fetched, Searches: cc.Searches}
	return d
}

// httpVerbs — giriş span adının yöntem öneki ("POST /orders/{id}").
var httpVerbs = map[string]bool{"GET": true, "POST": true, "PUT": true, "DELETE": true, "PATCH": true, "HEAD": true, "OPTIONS": true}

// operationCardCodeRequest — SAF: parametre + trace yarısı → devops isteği.
// Yöntem giriş span adından (yalnız bilinen fiil); çocuklar ağaçtan.
func operationCardCodeRequest(p operationCardParams, tree *chstore.OpCallTree, repo string, hint devops.ProjectHint) devops.OperationCardRequest {
	req := devops.OperationCardRequest{Repo: repo, Hint: hint, Service: p.Service, Entry: p.Entry, Route: p.Path}
	if tree == nil {
		return req
	}
	req.Version, req.Operation = tree.Version, tree.OperationCode
	if p.Entry == "http" {
		if f := strings.Fields(tree.Entry.Op); len(f) >= 2 && httpVerbs[strings.ToUpper(f[0])] {
			req.Method = strings.ToUpper(f[0])
		}
	}
	for _, c := range tree.Children {
		req.Children = append(req.Children, devops.CardChildHint{
			ParentKey: c.ParentKey, Kind: c.Kind, Protocol: c.Protocol, Target: c.Target, Op: c.Op,
		})
	}
	return req
}

// opCardMetaReader — katalog pini okuyucusu (*chstore.Store; testte sahte).
type opCardMetaReader interface {
	GetServiceMetadataStrict(ctx context.Context, service string) (*chstore.ServiceMetadata, error)
}

// opCardCallerReader — v0.10.1509: pencere geneli çağıran servisler
// (*chstore.Store.OperationWindowCallers, MV; testte sahte).
type opCardCallerReader interface {
	OperationWindowCallers(ctx context.Context, service, op string, from, to time.Time) ([]string, error)
}

// opCardUsageCallerCap — sıralama ipucu için depo çözülen çağıran tavanı.
const opCardUsageCallerCap = 10

// buildOperationCardCode — kod yarısı (bkz. dosya başı). meta nil = katalog
// yok (konvansiyon); callers nil = çağıran ipucu yok. İptal hatası yukarı
// döner (önbelleğe yazılmasın).
func buildOperationCardCode(ctx context.Context, dv *devops.Service, meta opCardMetaReader, callers opCardCallerReader, p operationCardParams, tree *chstore.OpCallTree) (*operationCardCodeDTO, error) {
	if dv == nil || !dv.Configured() {
		return newOperationCardCodeDTO(string(devops.CodeUnconfigured), "kod entegrasyonu yapılandırılmamış (Ayarlar → Kod entegrasyonu) — kart trace yarısıyla geçerli"), nil
	}
	if !dv.CodeSearchEnabled() {
		// İstek atılmadan: FetchOperationCard da aynı kapıyı tutar.
		return newOperationCardCodeDTO(opCardSearchOff, devops.OperationCardSearchOffReason), nil
	}
	pin := ""
	if meta != nil {
		md, err := meta.GetServiceMetadataStrict(ctx, p.Service)
		if err != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		mdRepo := ""
		if md != nil {
			mdRepo = md.Repository
		}
		pp, abort := pinReadDecision(mdRepo, md != nil, err)
		if abort != "" {
			return newOperationCardCodeDTO(string(devops.CodeCatalogError), abort), nil
		}
		pin = pp
	}
	res := devops.ResolveRepo(p.Service, pin, dv.ResolveConfig())
	if res.Repo == "" {
		reason := res.Reason
		if reason == "" {
			reason = "servis için depo çözülemedi"
		}
		d := newOperationCardCodeDTO(string(devops.CodeRepoUnresolved), reason)
		d.Source = res.Source
		return d, nil
	}
	cc := dv.FetchOperationCard(ctx, operationCardCodeRequest(p, tree, res.Repo, res.Project))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	d := operationCardCodeFrom(cc, res.Source)
	if cc.Entry != nil {
		u := dv.FetchMethodUsages(ctx, methodUsageRequest(ctx, dv, callers, p, tree, cc))
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		d.Usages = &u
	}
	return d, nil
}

// methodUsageRequest — giriş metodunun adı + sınıfı + trace'te ÇAĞIRAN
// servislerin depoları (ad konvansiyonu; yalnız sıralama ipucu, okuma yok —
// pin aramak çağıran başına bir CH okuması olurdu). Çağıran okunamazsa ipucu
// yok, bölüm yine çalışır.
func methodUsageRequest(ctx context.Context, dv *devops.Service, callers opCardCallerReader, p operationCardParams, tree *chstore.OpCallTree, cc devops.OperationCardCode) devops.MethodUsageRequest {
	_, _, name, _ := devops.MethodSignatureTypes(cc.Entry.Signature)
	base := cc.Entry.Path[strings.LastIndexByte(cc.Entry.Path, '/')+1:]
	req := devops.MethodUsageRequest{Method: name, Class: strings.TrimSuffix(base, ".java"), EntryRepo: cc.Repo, EntryPath: cc.Entry.Path}
	if callers == nil || tree == nil || tree.Entry.Op == "" || p.Sig {
		return req
	}
	svcs, err := callers.OperationWindowCallers(ctx, p.Service, tree.Entry.Op, p.From, p.To)
	if err != nil {
		return req
	}
	rc := dv.ResolveConfig()
	for i, svc := range svcs {
		if i >= opCardUsageCallerCap {
			break
		}
		if r := devops.ResolveRepo(svc, "", rc); r.Repo != "" {
			req.CallerRepos = append(req.CallerRepos, r.Repo)
		}
	}
	return req
}

// operationCardWithCode — code=1: trace yarısı code=0 anahtarının önbellek
// girdisinden (yoksa tek üretim), üstüne kod yarısı.
func (s *Server) operationCardWithCode(ctx context.Context, p operationCardParams) (any, error) {
	base := p
	base.Code = false
	body, _, err := s.cachedJSON(ctx, operationCardKey(base), operationCardTTL, false, func(c context.Context) (any, error) {
		return buildOperationCard(c, s.store, base)
	})
	if err != nil {
		return nil, err
	}
	var out operationCardPayload
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	var meta opCardMetaReader
	var callers opCardCallerReader
	if s.store != nil {
		meta, callers = s.store, s.store
	}
	code, err := buildOperationCardCode(ctx, s.devops, meta, callers, p, out.Tree)
	if err != nil {
		return nil, err
	}
	out.Code = code
	return &out, nil
}
