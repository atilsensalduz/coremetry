package api

// v0.10.1473 — takım cevabında "Son loglar (takım)" (copilot_team_logs.go).
// Takım / servis adları SENTETİK (team-alpha, orders-api).

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

// countingTeamLogs — TeamRecentLogs çağrılarını sayan sahte backend.
type countingTeamLogs struct {
	stubLogStore
	mu    sync.Mutex
	calls int
	last  logstore.TeamLogsQuery
	res   *logstore.TeamLogsResult
	err   error
	block bool // ctx bitene dek bekle (zaman aşımı yolu)
}

func (c *countingTeamLogs) TeamRecentLogs(ctx context.Context, q logstore.TeamLogsQuery) (*logstore.TeamLogsResult, error) {
	c.mu.Lock()
	c.calls++
	c.last = q
	c.mu.Unlock()
	if c.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return c.res, c.err
}

func (c *countingTeamLogs) n() int { c.mu.Lock(); defer c.mu.Unlock(); return c.calls }

// memESStore — ESManager kalıcılığı için bellek içi system_settings.
type memESStore struct{ raw []byte }

func (m *memESStore) GetLogstoreESSettingsRaw(context.Context) ([]byte, error) { return m.raw, nil }
func (m *memESStore) PutLogstoreESSettingsRaw(_ context.Context, b []byte) error {
	m.raw = b
	return nil
}

func teamLogsMgr(team string) *logstore.ESManager {
	return logstore.NewESManager(logstore.NewSwitchable(nil), nil, nil, logstore.ESSettings{
		Backend: "clickhouse", Fields: logstore.ESFieldMap{Team: team},
	})
}

func collectEmits() (func(string, any), *[]map[string]any) {
	var mu sync.Mutex
	var evs []map[string]any
	return func(kind string, p any) {
		mu.Lock()
		defer mu.Unlock()
		m, _ := p.(map[string]any)
		if m == nil {
			m = map[string]any{}
		}
		m["_kind"] = kind
		evs = append(evs, m)
	}, &evs
}

// ÇEKİRDEK PİN: takım alanı boş (ya da logsMgr yok) → okuma BAŞLAMAZ, sıfır
// TeamRecentLogs çağrısı, bölüm "" ve hiçbir ⚙ olayı yok (cevap bayt-bayt eski).
func TestTeamLogsDisabledZeroCalls(t *testing.T) {
	for name, mgr := range map[string]*logstore.ESManager{"logsMgr yok": nil, "alan boş": teamLogsMgr("")} {
		fake := &countingTeamLogs{}
		s := &Server{logs: fake, logsMgr: mgr, cache: newMemCache()}
		p := s.startTeamLogs(context.Background(), "team-alpha", []string{"team-alpha"}, time.Now())
		emit, evs := collectEmits()
		if blk := s.teamLogsBlock(emit, "team-alpha", p); blk != "" || p != nil {
			t.Errorf("%s: bölüm yazılmamalı: %q", name, blk)
		}
		time.Sleep(10 * time.Millisecond)
		if fake.n() != 0 || len(*evs) != 0 {
			t.Errorf("%s: çağrı=%d olay=%d, ikisi de 0 olmalı", name, fake.n(), len(*evs))
		}
	}
}

func okTeamLogsResult() *logstore.TeamLogsResult {
	return &logstore.TeamLogsResult{
		Fields: []string{"labels.team"}, Values: []string{"team-alpha"},
		ErrorCount: 42, Sampled: 2,
		Services: []logstore.PatternServiceHit{{Service: "orders-api", Count: 30}, {Service: "billing-api", Count: 12}},
		Patterns: []logstore.TeamLogPattern{{Template: "timeout calling payments id=<x>", Count: 2, Service: "orders-api"}},
		Lines:    []logstore.TeamLogLine{{TimestampNs: time.Date(2026, 10, 23, 11, 58, 0, 0, time.UTC).UnixNano(), Service: "orders-api", Line: "timeout calling payments id=12345", TraceID: "abc123"}},
	}
}

// Açık: bir çağrı, sonuç bölümü + kanıt çipi (/logs ön-süzgeçli); aynı pencerede
// ikinci okuma önbellekten (ES'e gitmez).
func TestTeamLogsEnabledReadsOnceThenCache(t *testing.T) {
	fake := &countingTeamLogs{res: okTeamLogsResult()}
	s := &Server{logs: fake, logsMgr: teamLogsMgr("labels.team"), cache: newMemCache()}
	now := time.Date(2026, 10, 23, 12, 0, 30, 0, time.UTC)
	raw, evs := collectEmits()
	emit := withStepIDs(raw) // üretimdeki gibi: adım kimliği → kanıt çipi
	blk := s.teamLogsBlock(emit, "team-alpha", s.startTeamLogs(context.Background(), "team-alpha", []string{"team-alpha"}, now))
	if fake.n() != 1 {
		t.Fatalf("tek çağrı bekleniyordu, %d", fake.n())
	}
	if !fake.last.From.Equal(now.Truncate(time.Minute).Add(-15*time.Minute)) || !fake.last.To.Equal(now.Truncate(time.Minute)) {
		t.Errorf("pencere dakikaya oturmalı (son 15 dk): %v %v", fake.last.From, fake.last.To)
	}
	for _, want := range []string{"42 hata logu", "orders-api (30)", "2× timeout calling payments id=<x>", "trace_id=abc123"} {
		if !strings.Contains(blk, want) {
			t.Errorf("bölüm %q taşımıyor:\n%s", want, blk)
		}
	}
	var sawEv bool
	for _, e := range *evs {
		if e["_kind"] == "step-result" && e["evidence"] != nil {
			sawEv = true
			b, _ := json.Marshal(e["evidence"])
			if !strings.Contains(string(b), `"source":"Son loglar (takım)"`) || !strings.Contains(string(b), `/logs?`) {
				t.Errorf("kanıt çipi: %s", b)
			}
		}
	}
	if !sawEv {
		t.Error("kanıt çipi yayılmadı")
	}
	// İkinci okuma: önbellek.
	_ = s.teamLogsBlock(func(string, any) {}, "team-alpha", s.startTeamLogs(context.Background(), "team-alpha", []string{"team-alpha"}, now.Add(10*time.Second)))
	if fake.n() != 1 {
		t.Errorf("60 s içinde aynı girdi önbellekten gelmeli, çağrı=%d", fake.n())
	}
}

// Zaman aşımı: backend bütçeyi aşarsa bölüm tek dürüst satır, hata önbelleğe
// YAZILMAZ; desteklenmeyen backend ve hata da tek satır.
func TestTeamLogsTimeoutAndErrors(t *testing.T) {
	from, to := teamLogsWindowAt(time.Now())
	c := newMemCache()
	slow := &countingTeamLogs{block: true}
	s := &Server{logs: slow, cache: c}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	o := s.readTeamLogs(ctx, "cfg", []string{"labels.team"}, []string{"team-alpha"}, from, to)
	if o.State != teamLogsTimedOut {
		t.Fatalf("zaman aşımı: %+v", o)
	}
	if len(c.m) != 0 {
		t.Error("başarısız okuma önbelleğe yazılmamalı")
	}
	txt := renderTeamLogsTR("team-alpha", o, time.Now())
	if !strings.Contains(txt, "OKUNAMADI") || !strings.Contains(txt, `"Log yok" DEME`) || strings.Count(txt, "\n") != 1 {
		t.Errorf("tek dürüst satır: %q", txt)
	}
	// Bekleme sınırı geçmiş, sonuç yok → timeout (bloklamaz).
	p := &teamLogsPending{ch: make(chan teamLogsOutcome), deadline: time.Now().Add(-time.Second), fields: []string{"labels.team"}}
	if got := p.wait(time.Now()); got.State != teamLogsTimedOut {
		t.Errorf("geçmiş deadline: %+v", got)
	}
	for _, cs := range []struct {
		err  error
		want string
	}{
		{logstore.ErrTeamLogsUnsupported, teamLogsUnsupported},
		{errors.New("ES team recent logs: 503"), teamLogsFailed},
	} {
		s := &Server{logs: &countingTeamLogs{err: cs.err}}
		if o := s.readTeamLogs(context.Background(), "cfg", []string{"f"}, []string{"v"}, from, to); o.State != cs.want {
			t.Errorf("%v → %s, want %s", cs.err, o.State, cs.want)
		}
	}
	if o := (&Server{logs: &countingTeamLogs{}}).readTeamLogs(context.Background(), "cfg", []string{"f"}, nil, from, to); o.State != teamLogsNoValues {
		t.Errorf("değersiz: %+v", o)
	}
}

func TestRenderTeamLogsTRStates(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	base := teamLogsOutcome{Fields: []string{"labels.team"}, Values: []string{"team-alpha"}}
	for _, c := range []struct {
		name string
		o    func() teamLogsOutcome
		want []string
		ban  []string
	}{
		{"boş", func() teamLogsOutcome {
			o := base
			o.State, o.Res = teamLogsOK, &logstore.TeamLogsResult{}
			return o
		}, []string{"hata seviyesinde log yok", "labels.team = team-alpha"}, []string{"OKUNAMADI"}},
		{"kısmi", func() teamLogsOutcome {
			o := base
			r := okTeamLogsResult()
			r.Partial = true
			o.State, o.Res = teamLogsOK, r
			return o
		}, []string{"KISMİ", "2dk önce"}, nil},
		{"örneklemden servis", func() teamLogsOutcome {
			o := base
			r := okTeamLogsResult()
			r.ServicesFromSample = true
			o.State, o.Res = teamLogsOK, r
			return o
		}, []string{"2 satırlık örneklemden"}, nil},
		{"desteklenmiyor", func() teamLogsOutcome { o := base; o.State = teamLogsUnsupported; return o },
			[]string{"yalnız Elasticsearch", `"Log yok" DEME`}, nil},
		{"hata", func() teamLogsOutcome { o := base; o.State, o.Err = teamLogsFailed, "503"; return o },
			[]string{"OKUNAMADI (503)"}, nil},
	} {
		got := renderTeamLogsTR("team-alpha", c.o(), now)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %q yok:\n%s", c.name, w, got)
			}
		}
		for _, b := range c.ban {
			if strings.Contains(got, b) {
				t.Errorf("%s: %q olmamalı:\n%s", c.name, b, got)
			}
		}
	}
}

func TestTeamLogLabelValues(t *testing.T) {
	ta := chstore.TeamAliases{Aliases: map[string]string{"alpha-squad": "Team-Alpha"}}
	mds := map[string]chstore.ServiceMetadata{
		"orders-api":  {OwnerTeam: "Team-Alpha", OwnerTeamAuto: "team-alpha"},
		"billing-api": {SRETeam: "TEAM-ALPHA"},
		"search-api":  {OwnerTeam: "team-beta"},
	}
	got := teamLogLabelValues(ta, mds, "Team-Alpha")
	want := []string{"Team-Alpha", "TEAM-ALPHA", "alpha-squad", "team-alpha"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v want %v", got, want)
	}
	if teamLogLabelValues(ta, mds, " ") != nil {
		t.Error("boş takım → nil")
	}
	many := map[string]chstore.ServiceMetadata{}
	for _, v := range []string{"tA", "Ta", "TA", "ta"} { // katlama tek takım
		many["s-"+v] = chstore.ServiceMetadata{OwnerTeam: v}
	}
	if got := teamLogLabelValues(chstore.TeamAliases{}, many, "ta"); len(got) != 4 || got[0] != "ta" {
		t.Errorf("çağıranın yazımı önce: %v", got)
	}
}

func TestTeamLogsKeyHashesAllInputs(t *testing.T) {
	from, to := teamLogsWindowAt(time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC))
	base := teamLogsKey("cfg", []string{"f"}, []string{"a", "b"}, from, to)
	if teamLogsKey("cfg", []string{"f"}, []string{"b", "a"}, from, to) != base {
		t.Error("değer sırası anahtarı değiştirmemeli")
	}
	for name, k := range map[string]string{
		"cfg":      teamLogsKey("cfg2", []string{"f"}, []string{"a", "b"}, from, to),
		"alan":     teamLogsKey("cfg", []string{"g"}, []string{"a", "b"}, from, to),
		"değer":    teamLogsKey("cfg", []string{"f"}, []string{"a", "c"}, from, to),
		"sınır":    teamLogsKey("cfg", []string{"f"}, []string{"ab"}, from, to),
		"dakika":   teamLogsKey("cfg", []string{"f"}, []string{"a", "b"}, from.Add(time.Minute), to.Add(time.Minute)),
		"genişlik": teamLogsKey("cfg", []string{"f"}, []string{"a", "b"}, from.Add(-time.Minute), to),
	} {
		if k == base {
			t.Errorf("%s değişti ama anahtar aynı", name)
		}
	}
}

func TestTeamLogsHref(t *testing.T) {
	h := teamLogsHref([]string{"kubernetes.labels.team-x"}, []string{`team "alpha"`}, 1000, 2000)
	u, err := url.Parse(h)
	if err != nil || u.Path != "/logs" {
		t.Fatalf("href: %q %v", h, err)
	}
	q := u.Query()
	if q.Get("q") != `kubernetes.labels.team-x:"team \"alpha\""` || q.Get("severity") != "17" || q.Get("range") != "custom:1000-2000" {
		t.Errorf("parametreler: %v", q)
	}
	if got := teamLogsQueryString([]string{"a", "b"}, []string{"x"}); got != `(a:"x" OR b:"x")` {
		t.Errorf("çoklu alan: %s", got)
	}
	if teamLogsHref(nil, []string{"x"}, 1, 2) != "" || teamLogsHref([]string{"a"}, nil, 1, 2) != "" {
		t.Error("eksik girdi → bağlantı yok")
	}
}

// Ayar tur turu: PUT gövdesi team'i taşır (işaretçi: yok = koru, "" = kapat),
// yanlış yazım 400; kayıt → blob → yeniden yükleme → Snapshot aynı değer.
// Denetim kaydı Snapshot.Fields'ı yazar (team dahil).
func TestTeamFieldSettingsRoundTrip(t *testing.T) {
	cur := logstore.ESSettings{Backend: "clickhouse", Fields: logstore.ESFieldMap{Team: "labels.team"}}
	merge := func(body string) (logstore.ESSettings, string) {
		var in esSettingsInput
		if err := json.Unmarshal([]byte(body), &in); err != nil {
			t.Fatal(err)
		}
		return mergeESSettings(in, cur)
	}
	if got, bad := merge(`{"backend":"clickhouse","fields":{"traceId":"trace.id"}}`); bad != "" || got.Fields.Team != "labels.team" {
		t.Errorf("anahtar yok → koru: %+v %q", got.Fields, bad)
	}
	if got, _ := merge(`{"backend":"clickhouse","fields":{"team":""}}`); got.Fields.Team != "" {
		t.Errorf(`"" → kapat: %+v`, got.Fields)
	}
	if got, _ := merge(`{"backend":"clickhouse","fields":{"team":" kubernetes.labels.team-x.keyword , labels.owner "}}`); got.Fields.Team != "kubernetes.labels.team-x,labels.owner" {
		t.Errorf("kanonik biçim: %q", got.Fields.Team)
	}
	if _, bad := merge(`{"backend":"clickhouse","fields":{"team":"labels.*"}}`); bad == "" {
		t.Error("wildcard 400 olmalı")
	}

	st := &memESStore{}
	mgr := teamLogsMgr("")
	cfg, _ := merge(`{"backend":"clickhouse","fields":{"team":"kubernetes.labels.team-x"}}`)
	if err := mgr.SavePersisted(context.Background(), st, cfg); err != nil {
		t.Fatal(err)
	}
	fresh := teamLogsMgr("")
	if err := fresh.LoadPersisted(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if got := fresh.Snapshot().Fields.Team; got != "kubernetes.labels.team-x" {
		t.Fatalf("yeniden yükleme: %q", got)
	}
	if f := (&Server{logsMgr: fresh}).teamLogFields(); len(f) != 1 || f[0] != "kubernetes.labels.team-x" {
		t.Errorf("teamLogFields: %v", f)
	}
	b, _ := json.Marshal(map[string]any{"fields": fresh.Snapshot().Fields})
	if !strings.Contains(string(b), `"team":"kubernetes.labels.team-x"`) {
		t.Errorf("denetim ayrıntısı team taşımalı: %s", b)
	}
	src, err := os.ReadFile("logstore_es_handlers.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `"fields":             snap.Fields`) || !strings.Contains(string(src), `s.audit(r, "settings.logstore.update"`) {
		t.Error("PUT denetimi alan haritasını (team dahil) yazmalı")
	}
}

// Bundle kablosu: okuma RED'den ÖNCE başlar (paralel), bölüm takım özeti /
// genel bakıştan SONRA yazılır.
func TestTeamServicesBundleWiresTeamLogs(t *testing.T) {
	src, err := os.ReadFile("copilot_guided.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	i := strings.Index(s, "func (s *Server) guidedTeamServicesBundle(")
	if i < 0 {
		t.Fatal("bundle yok")
	}
	body := s[i:]
	body = body[:strings.Index(body, "\n}\n")]
	start := strings.Index(body, "s.startTeamLogs(ctx, team")
	red := strings.Index(body, "mcptools.ReadTeamServicesRED(")
	ov := strings.Index(body, "s.teamOverviewBlock(")
	blk := strings.Index(body, "s.teamLogsBlock(emit, team, teamLogs)")
	if start < 0 || red < 0 || ov < 0 || blk < 0 || !(start < red && ov < blk) {
		t.Fatalf("sıra: start=%d red=%d overview=%d block=%d", start, red, ov, blk)
	}
}
