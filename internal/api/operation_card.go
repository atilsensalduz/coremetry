package api

// operation_card.go — v0.10.1491 (plan-operation-card §2.3.1, R3: operasyon
// kartının TRACE yarısı).
//
//   GET /api/endpoints/operation-card?service=&path=&sig=1&entry=http|rpc
//       &from=&to=&env=&cluster=&sample=slow|random[&code=1]
//
// Geliştiricinin "benim operasyonum neyi çağırıyor" sorusu: son trace'lerden
// (≤200, EndpointWhereTheTimeGoes şablonu) derinlik ≤3 çağrı ağacı —
// HTTP/gRPC/DB/Kafka hedefi, örneklem p95, hata %, satır başına ≤3 örnek
// trace — artı pencere geneli servis→servis çağrı sayısı (MV,
// topology_op_edges_5m) ve giriş span'lerinden iş kodları
// (FUNCTION_CODE/CHANNEL_CODE). Okuyucular internal/chstore/
// operation_calltree.go'da; neden MV değil, neden değer yok orada yazılı.
//
// NEDEN AYRI UÇ (downstream'e alan eklemek DEĞİL). /api/endpoints/downstream
// süre PAYI sorusunu cevaplıyor ve sözleşmesi (düz liste, ShareMs) FE'de iki
// yerde okunuyor; ağaç (ParentKey, derinlik, örnek trace) farklı bir şekil.
// Ayrıca R4 kod yarısı (?code=1) bu ucun ikinci bölümü olacak: kart tek
// payload, tek önbellek ailesi.
//
// api.go BÜYÜMEZ (TestApiGoDoesNotGrow): rota bu dosyada, kayıt init()'te
// registerRoutesExtra ile. Rol kapısı YOK — salt-okur drill-down,
// endpoints_detail.go duruşu; küresel auth middleware kimliksizi 401 yapar,
// viewer kartı GÖRÜR. serveCached 60 s (downstream kardeşiyle aynı: iki ham
// geçiş); anahtar TÜM girdileri taşır (operationCardKey, saf + testli).
//
// SINIRLAR. Pencere en çok 24 saat (aşan istek SON 24 saate kırpılır ve
// payload windowClamped der) — örneklem sorgusu ORDER BY duration DESC ile
// penceredeki her giriş span'ini tarar; 30 günlük bir pencere bu yüzeyin
// sorusu değil. path/service uzunluğu sınırlı (sınırsız ayrı önbellek
// girdisi basılmasın).
//
// BÖLÜM TOLERANSI. Ağaç okunamazsa tree:null + treeError (temiz mesaj; ham
// CH hatası loga, viewer'a değil), pencere sayısı okunamazsa windowEdges []
// + not. 500 yalnız iptalde (writeErr → 499).
//
// R4 YUVASI. `code` alanı bugün HEP null; kod yarısı (devops.FetchOperation-
// Card, Azure Code Search def:/ref:/class: filtreleri — plan sonundaki
// operatör kararı) ?code=1 dalında bu alana gelecek. Anahtar code bayrağını
// şimdiden taşır ki R4 açıldığında eski girdi yeni cevabı zehirlemesin.

import (
	"context"
	"fmt"
	"hash/fnv"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("operation-card", (*Server).registerOperationCardRoutes) }

func (s *Server) registerOperationCardRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/endpoints/operation-card", s.getOperationCard)
}

const (
	// operationCardMaxWindow — pencere tavanı (bkz. başlık SINIRLAR).
	operationCardMaxWindow = 24 * time.Hour
	// operationCardTTL — downstream / callers kardeşleriyle aynı.
	operationCardTTL    = 60 * time.Second
	opCardMaxServiceLen = 256
	opCardMaxPathLen    = 1024
)

// operationCardParams — doğrulanmış istek.
type operationCardParams struct {
	Service, Path string
	Sig           bool
	Entry         string // http | rpc
	Env, Cluster  string
	Sample        string // slow | random
	Code          bool   // R4 yuvası
	From, To      time.Time
	Clamped       bool
}

// parseOperationCardParams — SAF: sorgu → parametre ya da 400 mesajı.
func parseOperationCardParams(q map[string][]string, from, to time.Time) (operationCardParams, string) {
	get := func(k string) string {
		if v := q[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	p := operationCardParams{
		Service: strings.TrimSpace(get("service")),
		Path:    get("path"),
		Sig:     get("sig") == "1",
		Env:     strings.TrimSpace(get("env")),
		Cluster: strings.TrimSpace(get("cluster")),
		Code:    get("code") == "1",
	}
	if p.Service == "" || p.Path == "" {
		return p, "service ve path parametreleri zorunlu"
	}
	if len(p.Service) > opCardMaxServiceLen || len(p.Path) > opCardMaxPathLen ||
		len(p.Env) > opCardMaxServiceLen || len(p.Cluster) > opCardMaxServiceLen {
		return p, "parametre çok uzun"
	}
	switch e := get("entry"); e {
	case "", "http":
		p.Entry = "http"
	case "rpc":
		p.Entry = "rpc"
	default:
		return p, "entry yalnız http ya da rpc olabilir"
	}
	switch sm := get("sample"); sm {
	case "", "slow":
		p.Sample = "slow"
	case chstore.EndpointSampleRandom:
		p.Sample = chstore.EndpointSampleRandom
	default:
		return p, "sample yalnız slow ya da random olabilir"
	}
	if !to.After(from) {
		return p, "from, to'dan önce olmalı"
	}
	if to.Sub(from) > operationCardMaxWindow {
		from = to.Add(-operationCardMaxWindow)
		p.Clamped = true
	}
	p.From, p.To = from, to
	return p, ""
}

// operationCardKey — SAF: hash-ALL-inputs (v0.5.187). Serbest metin
// alanları (servis, yol, env, cluster) NUL ayraçlı tek FNV özetine girer ki
// alan sınırı sahte bir yolla taklit edilemesin; bayraklar ve dakikaya
// oturmuş pencere düz yazılır.
func operationCardKey(p operationCardParams) string {
	h := fnv.New64a()
	for _, f := range []string{p.Service, p.Path, p.Env, p.Cluster} {
		h.Write([]byte(f))
		h.Write([]byte{0})
	}
	return fmt.Sprintf("opcard:v1:d=%x:sig=%v:e=%s:smp=%s:code=%v:from=%d:to=%d",
		h.Sum64(), p.Sig, p.Entry, p.Sample, p.Code,
		pivotMinuteBucket(p.From), pivotMinuteBucket(p.To))
}

// operationCardPayload — kartın sözleşmesi (FE: lib/types.ts
// OperationCardPayload).
type operationCardPayload struct {
	Service       string `json:"service"`
	Path          string `json:"path"`
	Sig           bool   `json:"sig"`
	Entry         string `json:"entry"`
	FromNs        int64  `json:"fromNs"`
	ToNs          int64  `json:"toNs"`
	WindowClamped bool   `json:"windowClamped,omitempty"`
	// Tree — trace yarısı; okunamazsa null + TreeError.
	Tree      *chstore.OpCallTree `json:"tree"`
	TreeError string              `json:"treeError,omitempty"`
	// WindowEdgesNote — pencere sayısı neden yok (şekil kipi / okuma hatası).
	WindowEdgesNote string `json:"windowEdgesNote,omitempty"`
	// Code — R4 yuvası (kod yarısı). R3'te her zaman null.
	Code any `json:"code"`
}

// operationCardReader — kartın iki okuyucusu (*chstore.Store; testte sahte).
type operationCardReader interface {
	OperationCallTree(ctx context.Context, q chstore.OpCallTreeQuery) (*chstore.OpCallTree, error)
	OperationWindowEdges(ctx context.Context, service, parentOp string, from, to time.Time) ([]chstore.OpWindowEdge, error)
}

// buildOperationCard — kartı kurar, bölüm toleransıyla. İptal hatası
// yukarı döner (önbelleğe yazılmasın, writeErr 499); diğer hatalar bölüm
// notuna düşer.
func buildOperationCard(ctx context.Context, rd operationCardReader, p operationCardParams) (*operationCardPayload, error) {
	out := &operationCardPayload{
		Service: p.Service, Path: p.Path, Sig: p.Sig, Entry: p.Entry,
		FromNs: p.From.UnixNano(), ToNs: p.To.UnixNano(), WindowClamped: p.Clamped,
	}
	entry := chstore.EntryHTTP
	if p.Entry == "rpc" {
		entry = chstore.EntryRPC
	}
	sample := ""
	if p.Sample == chstore.EndpointSampleRandom {
		sample = chstore.EndpointSampleRandom
	}
	tree, err := rd.OperationCallTree(ctx, chstore.OpCallTreeQuery{
		EndpointDetailQuery: chstore.EndpointDetailQuery{
			Service: p.Service, Path: p.Path, BySignature: p.Sig,
			From: p.From, To: p.To, Env: p.Env, Cluster: p.Cluster, Sample: sample,
		},
		Entry: entry,
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		log.Printf("[opcard] call tree %s %q: %v", p.Service, p.Path, err)
		out.TreeError = "Çağrı ağacı okunamadı (örneklem sorgusu başarısız) — sayfanın diğer bölümleri geçerli."
		return out, nil
	}
	if tree == nil {
		tree = &chstore.OpCallTree{}
	}
	if tree.Children == nil {
		tree.Children = []chstore.OpCallEdge{}
	}
	if tree.Codes == nil {
		tree.Codes = []chstore.OpBizCode{}
	}
	tree.WindowEdges = []chstore.OpWindowEdge{}
	out.Tree = tree

	switch {
	case p.Sig:
		// topology_op_edges_5m span ADIna göre tutuluyor; şekil (/orders/:id)
		// hiçbir ada eşit değil — sıfır satırı "çağrı yok" diye göstermek yanlış olurdu.
		out.WindowEdgesNote = "Şekil kipinde pencere geneli çağrı sayısı yok (MV span adıyla tutuluyor)."
	case tree.Entry.Op == "":
		// Örneklem boş: üst operasyon adı bilinmiyor, MV sorgulanmaz.
	default:
		edges, werr := rd.OperationWindowEdges(ctx, p.Service, tree.Entry.Op, p.From, p.To)
		if werr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			log.Printf("[opcard] window edges %s %q: %v", p.Service, tree.Entry.Op, werr)
			out.WindowEdgesNote = "Pencere geneli çağrı sayısı okunamadı."
		} else if edges != nil {
			tree.WindowEdges = edges
		}
	}
	return out, nil
}

func (s *Server) getOperationCard(w http.ResponseWriter, r *http.Request) {
	from, to := parseFromTo(r, time.Hour)
	p, bad := parseOperationCardParams(r.URL.Query(), from, to)
	if bad != "" {
		writeJSONError(w, http.StatusBadRequest, bad)
		return
	}
	s.serveCached(w, r, operationCardKey(p), operationCardTTL, func(ctx context.Context) (any, error) {
		// Verilen ctx kullanılır (v0.8.319: istek bağlamı SWR tazelemesini öldürür).
		return buildOperationCard(ctx, s.store, p)
	})
}
