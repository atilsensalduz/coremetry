package chstore

// v0.10.1454 — CoSRE guided "giden çağrı / outbound_call" (R1) okuyucusu.
//
// Operator-reported: servis sayfasında "/esb/external/rest/…/customer-kvkk/
// kısmı için grafikleri ortalamayı göster" → yol bir CLIENT span'inin
// url.full/url.path'i (ESB'ye giden çağrı), katalogda (operation_summary_5m /
// spanmetrics_1m) yok; serbest döngü 75K token harcayıp "veri bulunamadı"
// diyordu. Bu dosya o yolu ölçen sınırlı okumaları taşır:
//
//   - OutboundProbeMV — topology_edges_5m.top_labels (kenar başına topK(5)
//     "METHOD /şablon") üzerinde ucuz VAR/YOK + çağıran×host ön süzgeci.
//     Boş ≠ yok: seyrek yol en sık 5'te olmayabilir → ham sonda.
//   - OutboundExternalCallers — çağıran belirsizken ham taramanın çağıran
//     listesi (≤100, MV).
//   - OutboundProbeRaw / OutboundAggs / OutboundSeries — ham spans; yol ve
//     host ifadeleri external_paths.go'daki sabitlerin AYNISI (ikinci kriter
//     uydurma yok — dış düğümü üreten kriter, içini açan okumanın kriteri).
//
// Sınırlar (CLAUDE.md sert kural, /clickhouse-schema adım 4): service_name IN ?
// (spans ORDER BY ön eki) + time >= / < (DateTime64 → chDateTime64Arg) +
// PREWHERE kind/db_system/msg_system + LIMIT + max_execution_time. MV YOK
// (v0.9.1255 gerekçesi: url grain'i topology_edges_5m'i url kardinalitesiyle
// çarpar); pencereyi çağıran (api) ≤6 sa'e kırpar.

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// OutboundQuery — ham ölçüm okumasının girdisi.
type OutboundQuery struct {
	// PathLike — OutboundPathPattern çıktısı (ILIKE deseni). Alt yollar da
	// eşleşir (PathLike + "/%").
	PathLike string
	// Services — ÇAĞIRANLAR (zorunlu, ≤100): spans birincil anahtar ön eki.
	Services []string
	// PeerHost — "" = tüm dış hostlar.
	PeerHost string
	// Envs — deploy_env IN (?) ; boş = süzgeç yok.
	Envs     []string
	From, To time.Time
	// ByEnv — v0.10.1455 (R2): ≥2 ortam anıldı → satırlar / seriler
	// deploy_env başına (GROUP BY deploy_env). MV parent_env KULLANILMAZ
	// (ORDER BY dışı, eski satırlarda boş) — ortam kırılımı ham yoldan.
	ByEnv bool
}

// OutboundAllPaths — v0.10.1455 (R2): yolsuz (host-yalnız) ham okumanın
// deseni. Bu desende satırlar yol başına bölünmez (toplamlar LIMIT ile
// kırpılmasın); path sütunu outboundAllPathsLabel taşır.
const OutboundAllPaths = "%"

const outboundAllPathsLabel = "(tüm yollar)"

func (q OutboundQuery) hostOnly() bool { return q.PathLike == OutboundAllPaths }

// OutboundCaller — sonda satırı: çağıran servis × dış host.
type OutboundCaller struct {
	Service string `json:"service"`
	Host    string `json:"host"`
	Calls   uint64 `json:"calls"`
}

// OutboundAgg — tek (çağıran, host, yol grubu) için pencere ölçümü.
type OutboundAgg struct {
	Service   string  `json:"service"`
	Peer      string  `json:"peer"`
	Path      string  `json:"path"`
	Win       string  `json:"win"`           // cur | prev (tek pencerede hep cur)
	Env       string  `json:"env,omitempty"` // v0.10.1455 — ByEnv'de deploy_env
	Calls     uint64  `json:"calls"`
	Errors    uint64  `json:"errors"`
	AvgMs     float64 `json:"avgMs"`
	P95Ms     float64 `json:"p95Ms"`
	SlowTrace string  `json:"slowTrace,omitempty"`
	ErrTrace  string  `json:"errTrace,omitempty"`
}

// OutboundPoint — zaman serisi kovası (Bucket unix sn; Service "" = toplam).
type OutboundPoint struct {
	Bucket  int64   `json:"b"`
	Service string  `json:"s,omitempty"`
	Calls   uint64  `json:"c"`
	Errors  uint64  `json:"e"`
	AvgMs   float64 `json:"a"`
	P95Ms   float64 `json:"p"`
}

const (
	// outboundAggLimit — (çağıran, host, yol) × pencere satır tavanı.
	outboundAggLimit = 60
	// outboundSeriesLimit — ≤120 nokta × ≤3 çağıran.
	outboundSeriesLimit = 400
	// outboundProbeLimit — sonda satır tavanı.
	outboundProbeLimit = 20
	// OutboundCallerScanMax — çağıransız ham taramanın çağıran tavanı.
	OutboundCallerScanMax = 100
)

// OutboundPathPattern — SAF: anılan yolun ILIKE deseni. Yol önce
// normalizeExternalPath'ten geçer (ham "/orders/123" → "/orders/{id}", DB'deki
// grup adı); tam segment "..." / "…" / {id} / :id / * → '%', segmente yapışık
// elips ("ExternalX...") → "ExternalX%"; LIKE özel karakterleri (% _ \) kaçışlanır.
func OutboundPathPattern(path string) string {
	path = strings.ReplaceAll(path, "…", "...")
	p := normalizeExternalPath(path)
	segs := strings.Split(p, "/")
	for i, sg := range segs {
		switch {
		case sg == "":
		case sg == "..." || sg == "*" || sg == "**" || (strings.HasPrefix(sg, "{") && strings.HasSuffix(sg, "}")) || strings.HasPrefix(sg, ":"):
			segs[i] = "%"
		default:
			parts := strings.Split(sg, "...")
			for j, pt := range parts {
				parts[j] = likeEscape(pt)
			}
			segs[i] = strings.Join(parts, "%")
		}
	}
	out := strings.Join(segs, "/")
	for strings.Contains(out, "%%") || strings.Contains(out, "/%/%/") {
		out = strings.ReplaceAll(strings.ReplaceAll(out, "%%", "%"), "/%/%/", "/%/")
	}
	return out
}

func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// OutboundPathNeedle — SAF: MV ön süzgecinin aradığı parça = yolun SON
// belirleyici segmenti (≥3 karakter, elips / yer tutucu / sayı değil);
// glued elipsli segmentin elipsten önceki kısmı da sayılır. "" = ön süzgeç yok.
func OutboundPathNeedle(path string) string {
	path = strings.ReplaceAll(path, "…", "...")
	segs := strings.Split(path, "/")
	for i := len(segs) - 1; i >= 0; i-- {
		sg := segs[i]
		if j := strings.Index(sg, "..."); j >= 0 {
			sg = sg[:j]
		}
		if len(sg) < 3 || strings.HasPrefix(sg, "{") || strings.HasPrefix(sg, ":") || strings.Trim(sg, "0123456789") == "" || strings.Contains(sg, "*") {
			continue
		}
		return sg
	}
	return ""
}

// outboundPass — v0.10.1455: en iç katmanda (FROM spans) hesaplanıp dış
// katmanlara adıyla taşınan sütun (deploy_env, iş kodları).
type outboundPass struct{ name, expr string }

// outboundSQLOpts — outboundSQL'in tam girdisi (having / limitBy / pass R2).
type outboundSQLOpts struct {
	sel, group, having, order, limitBy string
	limit                              int
	withEnv, withPeer                  bool
	pass                               []outboundPass
}

// outboundSQL — SAF kurucu: ham client-span okumasının ortak gövdesi. İç
// katmanlar external_paths.go ile AYNI (URL → yol → normalize, peer host
// zinciri); dış katman sel/group/order/limit ile değişir. Bağ sırası:
// [sel içindeki ?'ler], [pass ifadelerinin ?'leri], services, from, to,
// [envs], pat, patSub, [peer].
func outboundSQL(sel, group, order string, limit int, withEnv, withPeer bool) string {
	return outboundSQLWith(outboundSQLOpts{sel: sel, group: group, order: order, limit: limit, withEnv: withEnv, withPeer: withPeer})
}

func outboundSQLWith(o outboundSQLOpts) string {
	env, peer, having, limitBy := "", "", "", ""
	if o.withEnv {
		env = " AND deploy_env IN ?"
	}
	if o.withPeer {
		peer = " AND peer = ?"
	}
	if o.having != "" {
		having = " HAVING " + o.having
	}
	if o.limitBy != "" {
		limitBy = " " + o.limitBy
	}
	inner, outer := "", ""
	for _, p := range o.pass {
		inner += ", " + p.expr + " AS " + p.name
		outer += ", " + p.name
	}
	return `/* cosre:outbound_calls */
		SELECT ` + o.sel + `
		FROM (
		  SELECT service_name, time, duration, status_code, trace_id, peer` + outer + `,
		         if(length(g) > 1, substring(g, 1, length(g) - 1), '/') AS path
		  FROM (
		    SELECT service_name, time, duration, status_code, trace_id, peer` + outer + `, ` + externalPathNormalizeSQL("bp") + ` AS g
		    FROM (
		      SELECT service_name, time, duration, status_code, trace_id, ` + externalPeerHostSQL + ` AS peer` + outer + `,
		             if(position(u, '://') > 0, path(u), cutQueryStringAndFragment(u)) AS bp
		      FROM (
		        SELECT service_name, time, duration, status_code, trace_id, peer_service, attr_keys, attr_values, ` + externalPathURLSQL + ` AS u` + inner + `
		        FROM spans
		        PREWHERE kind = 'client' AND db_system = '' AND msg_system = ''
		        WHERE service_name IN ? AND time >= toDateTime64(?, 9, 'UTC') AND time < toDateTime64(?, 9, 'UTC')` + env + ` AND ` + externalPathURLSQL + ` != ''
		) ) ) )
		WHERE (path ILIKE ? OR path ILIKE ?)` + peer + `
		GROUP BY ` + o.group + having + `
		ORDER BY ` + o.order + limitBy + `
		LIMIT ` + strconv.Itoa(o.limit) + `
		SETTINGS max_execution_time = 8`
}

// outboundEnvPass — ByEnv: deploy_env dış katmanlara ob_env adıyla taşınır.
var outboundEnvPass = outboundPass{name: "ob_env", expr: "deploy_env"}

// outboundAggSel — cur/prev bölmeli (split) ya da tek pencere ölçüm sütunları.
func outboundAggSel(split bool) string { return outboundAggSelWith(split, false, false) }

// outboundAggSelWith — v0.10.1455: byEnv → env = ob_env (outboundEnvPass);
// allPaths → path sabit etiket (host-yalnız okumada yol başına bölünmez).
func outboundAggSelWith(split, byEnv, allPaths bool) string {
	win := `'cur'`
	if split {
		win = `if(time >= toDateTime64(?, 9, 'UTC'), 'cur', 'prev')`
	}
	env := `'' AS env`
	if byEnv {
		env = `ob_env AS env`
	}
	path := `path`
	if allPaths {
		path = `'` + outboundAllPathsLabel + `' AS path`
	}
	return env + `, service_name, peer, ` + path + `, ` + win + ` AS win,
		       count() AS calls, countIf(status_code = 'error') AS errors,
		       avg(duration) / 1e6 AS avg_ms, quantileTDigest(0.95)(duration) / 1e6 AS p95_ms,
		       argMax(trace_id, duration) AS slow_trace, anyIf(trace_id, status_code = 'error') AS err_trace`
}

// outboundSeriesSel — zaman serisi sütunları (perCaller: çağıran başına).
func outboundSeriesSel(perCaller bool) string {
	if perCaller {
		return outboundSeriesSelBy(OutboundSeriesByService)
	}
	return outboundSeriesSelBy(OutboundSeriesTotal)
}

// Zaman serisi kırılımı (OutboundPoint.Service grup etiketini taşır).
const (
	OutboundSeriesTotal     = ""
	OutboundSeriesByService = "service"
	OutboundSeriesByEnv     = "env" // v0.10.1455 — ob_env (outboundEnvPass)
)

func outboundSeriesSelBy(by string) string {
	svc := `'' AS svc`
	switch by {
	case OutboundSeriesByService:
		svc = `service_name AS svc`
	case OutboundSeriesByEnv:
		svc = `ob_env AS svc`
	}
	return `toInt64(toUnixTimestamp(toStartOfInterval(time, toIntervalSecond(?)))) AS b, ` + svc + `,
		       count() AS calls, countIf(status_code = 'error') AS errors,
		       avg(duration) / 1e6 AS avg_ms, quantileTDigest(0.95)(duration) / 1e6 AS p95_ms`
}

// outboundTailArgs — services/from/to/[envs]/pat/patSub/[peer] bağları.
func outboundTailArgs(q OutboundQuery, from time.Time) []any {
	args := []any{q.Services, chDateTime64Arg(from), chDateTime64Arg(q.To)}
	if len(q.Envs) > 0 {
		args = append(args, q.Envs)
	}
	args = append(args, q.PathLike, q.PathLike+"/%")
	if q.PeerHost != "" {
		args = append(args, q.PeerHost)
	}
	return args
}

func (q OutboundQuery) valid() bool {
	return q.PathLike != "" && len(q.Services) > 0 && q.To.After(q.From)
}

// OutboundAggs — pencere ölçümü. prevFrom sıfır değilse TEK geçişte
// [prevFrom, To) okunur ve satırlar cur (time >= From) / prev olarak bölünür.
// v0.10.1455: q.ByEnv → satırlar deploy_env başına (Env dolu); host-yalnız
// desende (OutboundAllPaths) yol başına bölünmez.
func (s *Store) OutboundAggs(ctx context.Context, q OutboundQuery, prevFrom time.Time) ([]OutboundAgg, error) {
	if !q.valid() {
		return []OutboundAgg{}, nil
	}
	split := !prevFrom.IsZero() && prevFrom.Before(q.From)
	from := q.From
	var args []any
	if split {
		args = append(args, chDateTime64Arg(q.From))
		from = prevFrom
	}
	args = append(args, outboundTailArgs(q, from)...)
	o := outboundSQLOpts{sel: outboundAggSelWith(split, q.ByEnv, q.hostOnly()), group: "env, service_name, peer, path, win",
		order: "calls DESC", limit: outboundAggLimit, withEnv: len(q.Envs) > 0, withPeer: q.PeerHost != ""}
	if q.ByEnv {
		o.pass = []outboundPass{outboundEnvPass}
	}
	rows, err := s.conn.Query(ctx, outboundSQLWith(o), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OutboundAgg{}
	for rows.Next() {
		var r OutboundAgg
		if err := rows.Scan(&r.Env, &r.Service, &r.Peer, &r.Path, &r.Win, &r.Calls, &r.Errors, &r.AvgMs, &r.P95Ms, &r.SlowTrace, &r.ErrTrace); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// OutboundSeries — grafik kovaları (stepS saniye; perCaller çağıran başına).
// v0.10.1455: q.ByEnv → ortam başına (perCaller'ı ezer).
func (s *Store) OutboundSeries(ctx context.Context, q OutboundQuery, stepS int64, perCaller bool) ([]OutboundPoint, error) {
	if !q.valid() {
		return []OutboundPoint{}, nil
	}
	if stepS < 60 {
		stepS = 60
	}
	by := OutboundSeriesTotal
	if perCaller {
		by = OutboundSeriesByService
	}
	o := outboundSQLOpts{group: "b, svc", order: "b ASC", limit: outboundSeriesLimit, withEnv: len(q.Envs) > 0, withPeer: q.PeerHost != ""}
	if q.ByEnv {
		by, o.pass = OutboundSeriesByEnv, []outboundPass{outboundEnvPass}
	}
	o.sel = outboundSeriesSelBy(by)
	args := append([]any{stepS}, outboundTailArgs(q, q.From)...)
	rows, err := s.conn.Query(ctx, outboundSQLWith(o), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OutboundPoint{}
	for rows.Next() {
		var p OutboundPoint
		if err := rows.Scan(&p.Bucket, &p.Service, &p.Calls, &p.Errors, &p.AvgMs, &p.P95Ms); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ─── v0.10.1455 (R2) — iş kodu kırılımı ───────────────────────────────

// OutboundCode — function_code / channel_code değeri başına giden çağrı.
type OutboundCode struct {
	Kind   string  `json:"kind"` // function_code | channel_code
	Code   string  `json:"code"`
	Calls  uint64  `json:"calls"`
	Errors uint64  `json:"errors"`
	AvgMs  float64 `json:"avgMs"`
}

// outboundCodeTopN — tür başına en çok çağrılan kod sayısı.
const outboundCodeTopN = 5

// outboundCodeKeys — iş kodu anahtarları (businessDimExpr terfi kolonu /
// iki yazım çözümü — v0.9.624 dersi: prod küçük harf yazar).
var outboundCodeKeys = []string{"function_code", "channel_code"}

// outboundCodesSQL — SAF: iş kodu top-5 (tür başına). Kod ifadeleri en iç
// katmanda (attr dizileri / terfi kolon orada) hesaplanır, dışarı ob_fc /
// ob_cc adıyla taşınır; boş kod elenir (HAVING), LIMIT 5 BY kind.
func outboundCodesSQL(withEnv, withPeer bool) (string, []any) {
	var pass []outboundPass
	var args []any
	for i, k := range outboundCodeKeys {
		expr, a := businessDimExpr(k)
		pass = append(pass, outboundPass{name: []string{"ob_fc", "ob_cc"}[i], expr: expr})
		args = append(args, a...)
	}
	return outboundSQLWith(outboundSQLOpts{
		sel: `tupleElement(kv, 1) AS kind, tupleElement(kv, 2) AS code,
		       count() AS calls, countIf(status_code = 'error') AS errors, avg(duration) / 1e6 AS avg_ms`,
		group: "kind, code", having: "code != ''", order: "kind ASC, calls DESC",
		limitBy: "LIMIT " + strconv.Itoa(outboundCodeTopN) + " BY kind", limit: 2 * outboundCodeTopN,
		withEnv: withEnv, withPeer: withPeer,
		pass: append(pass, outboundPass{name: "kv", expr: "arrayJoin([tuple('function_code', toString(ob_fc)), tuple('channel_code', toString(ob_cc))])"}),
	}), args
}

// OutboundCodes — v0.10.1455: aynı süzgeçte function_code / channel_code
// top-5. Boş sonuç = bu çağrılar iş kodu taşımıyor (bölüm yazılmaz).
func (s *Store) OutboundCodes(ctx context.Context, q OutboundQuery) ([]OutboundCode, error) {
	if !q.valid() {
		return []OutboundCode{}, nil
	}
	sql, passArgs := outboundCodesSQL(len(q.Envs) > 0, q.PeerHost != "")
	rows, err := s.conn.Query(ctx, sql, append(passArgs, outboundTailArgs(q, q.From)...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OutboundCode{}
	for rows.Next() {
		var c OutboundCode
		if err := rows.Scan(&c.Kind, &c.Code, &c.Calls, &c.Errors, &c.AvgMs); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ─── v0.10.1455 (R2) — host-yalnız özet, MV-first ─────────────────────

// OutboundHostCallers — tek dış host'un çağıranları (topology_edges_5m;
// GetExternalHostDetail'in çağıran okumasının AYNISI: externalCallersSQL,
// enstrümante servis dışlaması dahil). p99 kova maksimumu (muhafazakâr).
func (s *Store) OutboundHostCallers(ctx context.Context, host string, from, to time.Time) ([]ExternalCaller, error) {
	out := []ExternalCaller{}
	if host == "" {
		return out, nil
	}
	b := from.Truncate(5 * time.Minute)
	rows, err := s.conn.Query(ctx, externalCallersSQL(), b, to, host, b, to, externalHostExclusionCandidates(host))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c ExternalCaller
		var avgMs *float64
		if err := rows.Scan(&c.Service, &c.Calls, &c.Errors, &avgMs, &c.P99Ms, &c.TopLabels); err != nil {
			return nil, err
		}
		c.AvgMs = safeF(avgMs)
		if c.Calls > 0 {
			c.ErrorRate = float64(c.Errors) * 100 / float64(c.Calls)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// outboundHostTrendSQL — SAF: externalTrendSQL; caller doluysa tek
// çağıranın kenarı (bağ host'tan hemen sonra). Metin externalTrendSQL'in
// kendisinden türetilir — ikinci trend kriteri yok.
func outboundHostTrendSQL(withCaller bool) string {
	if !withCaller {
		return externalTrendSQL()
	}
	return strings.Replace(externalTrendSQL(), outboundTrendHostPred, outboundTrendHostPred+" AND parent_service = ?", 1)
}

const outboundTrendHostPred = "AND child_node = concat('ext:', ?)"

// OutboundHostTrend — 5 dk kovalı RED trendi (tüm çağıranlar ya da tek çağıran).
func (s *Store) OutboundHostTrend(ctx context.Context, host, caller string, from, to time.Time) ([]ExternalTrendPoint, error) {
	out := []ExternalTrendPoint{}
	if host == "" {
		return out, nil
	}
	b := from.Truncate(5 * time.Minute)
	args := []any{b, to, host}
	if caller != "" {
		args = append(args, caller)
	}
	args = append(args, b, to, externalHostExclusionCandidates(host))
	rows, err := s.conn.Query(ctx, outboundHostTrendSQL(caller != ""), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p ExternalTrendPoint
		var bucket uint32
		var avgMs *float64
		if err := rows.Scan(&bucket, &p.Calls, &p.Errors, &avgMs, &p.P99Ms); err != nil {
			return nil, err
		}
		p.Bucket = int64(bucket)
		p.AvgMs = safeF(avgMs)
		out = append(out, p)
	}
	return out, rows.Err()
}

// OutboundProbeRaw — ham sonda: yol bu çağıranlarda var mı (çağıran × host).
func (s *Store) OutboundProbeRaw(ctx context.Context, q OutboundQuery) ([]OutboundCaller, error) {
	if !q.valid() {
		return []OutboundCaller{}, nil
	}
	sql := outboundSQL("service_name, peer, count() AS calls", "service_name, peer", "calls DESC", outboundProbeLimit, len(q.Envs) > 0, q.PeerHost != "") // ByEnv yok: sonda VAR/YOK
	return s.scanOutboundCallers(ctx, sql, outboundTailArgs(q, q.From)...)
}

// outboundProbeMVSQL — SAF: MV ön süzgeci. withSvc → parent_service = ?.
// time_bucket DateTime: bağ time.Time (external.go emsali), DateTime64 dizesi DEĞİL.
func outboundProbeMVSQL(withSvc bool) string {
	svc := ""
	if withSvc {
		svc = "\n\t\t  AND parent_service = ?"
	}
	return `/* cosre:outbound_probe */
		SELECT toString(parent_service) AS svc, substring(child_node, 5) AS host, sum(calls) AS c
		FROM topology_edges_5m FINAL
		WHERE node_kind = 'external' AND time_bucket >= ? AND time_bucket < ?
		  AND arrayExists(l -> positionCaseInsensitiveUTF8(l, ?) > 0, top_labels)` + svc + `
		GROUP BY svc, host
		ORDER BY c DESC
		LIMIT 20
		SETTINGS max_execution_time = 5`
}

// OutboundProbeMV — needle top_labels'ta geçen dış kenarlar (çağıran × host).
func (s *Store) OutboundProbeMV(ctx context.Context, needle, service string, from, to time.Time) ([]OutboundCaller, error) {
	if needle == "" {
		return []OutboundCaller{}, nil
	}
	args := []any{from.Truncate(5 * time.Minute), to, needle}
	if service != "" {
		args = append(args, service)
	}
	return s.scanOutboundCallers(ctx, outboundProbeMVSQL(service != ""), args...)
}

// outboundExternalCallersSQL — SAF: dış host çağıran servisler (en yoğun ≤100).
func outboundExternalCallersSQL() string {
	return `/* cosre:outbound_probe */
		SELECT toString(parent_service) AS svc
		FROM topology_edges_5m FINAL
		WHERE node_kind = 'external' AND time_bucket >= ? AND time_bucket < ?
		GROUP BY svc
		ORDER BY sum(calls) DESC
		LIMIT 100
		SETTINGS max_execution_time = 5`
}

// OutboundExternalCallers — çağıransız ham taramanın çağıran listesi.
func (s *Store) OutboundExternalCallers(ctx context.Context, from, to time.Time) ([]string, error) {
	rows, err := s.conn.Query(ctx, outboundExternalCallersSQL(), from.Truncate(5*time.Minute), to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		if v != "" {
			out = append(out, v)
		}
	}
	return out, rows.Err()
}

func (s *Store) scanOutboundCallers(ctx context.Context, sql string, args ...any) ([]OutboundCaller, error) {
	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OutboundCaller{}
	for rows.Next() {
		var c OutboundCaller
		if err := rows.Scan(&c.Service, &c.Host, &c.Calls); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
