package notify

// policy_shadow_test.go — v0.10.1523 (Problem standardı R1): gölge ölçüm
// gönderimi ASLA değiştirmez.

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// shadowTestNotifier — CH'siz: gölge bağımlılıkları sahte.
func shadowTestNotifier(rows *[]chstore.NotificationLog, mu *sync.Mutex) *Notifier {
	n := New(nil)
	n.shadow.sink = func(_ context.Context, e chstore.NotificationLog) error {
		mu.Lock()
		*rows = append(*rows, e)
		mu.Unlock()
		return nil
	}
	n.shadow.verdict = func(context.Context) ([]chstore.ProblemVerdict, error) { return nil, nil }
	n.shadow.traffic = func(context.Context, chstore.Problem, time.Time) *chstore.StdTraffic {
		return &chstore.StdTraffic{Calls5m: 9000, Errors5m: 750}
	}
	n.shadow.sens = func() chstore.AnomalySensitivityConfig { return chstore.AnomalySensitivityConfig{} }
	return n
}

func shadowProblem() chstore.Problem {
	resolved := int64(0)
	return chstore.Problem{
		ID: "p-1", RuleID: "anomaly:bff-web:error_rate", Service: "bff-web", Metric: "error_rate",
		Value: 3.2, Threshold: 0.4, Severity: "critical", Status: "open", Priority: "P1",
		StartedAt:   time.Now().Add(-10 * time.Minute).UnixNano(),
		Description: "Teknik hata (5xx) oranı sürekli yükseldi on bff-web — current 3.20% vs baseline 0.40% (eşik 0.80%, 2.5 hata/sn, 6 kova ≈ 30 dk boyunca eşiğin üstünde). " + chstore.REDEvidenceVM + ".",
		Clusters:    []string{"prod-a"}, ResolvedAt: &resolved,
		RecentDeploy: &chstore.RecentDeploy{Version: "1.2.3"},
	}
}

// TestShadowNeverChangesSends — üç katman:
//  1. davranış: gölge çağrılarıyla ve çağrılarsız iki Notifier'ın kanal
//     kararı dizisi (allowChannelSend) BAYT BAYT aynı; Problem değeri
//     değişmez; dedup defterlerine dokunulmaz;
//  2. kablo (AST): SendProblemAlert'te kanca çıplak ifade (dönüşü
//     kullanılmaz, if/return'e bağlanmaz), "problem değil" kapısından sonra,
//     ekip mailinden ve kanal döngüsünden önce;
//  3. kanca gövdesi işi goroutine'de yapar.
func TestShadowNeverChangesSends(t *testing.T) {
	var mu sync.Mutex
	var rows []chstore.NotificationLog
	withShadow := shadowTestNotifier(&rows, &mu)
	without := New(nil)

	p := shadowProblem()
	before, _ := json.Marshal(p)
	type call struct {
		ok bool
		n  int
	}
	var a, b []call
	for i := 0; i < 5; i++ {
		withShadow.shadowNotifyPolicy(context.Background(), p)
		ok, n2 := withShadow.allowChannelSend(p.ID, "ch-1", p.Status, p.Severity, p.StartedAt)
		a = append(a, call{ok, n2})
		ok, n2 = without.allowChannelSend(p.ID, "ch-1", p.Status, p.Severity, p.StartedAt)
		b = append(b, call{ok, n2})
	}
	withShadow.shadow.wg.Wait()
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Fatalf("kanal kararları ayrıştı:\n gölgeli  %s\n gölgesiz %s", ja, jb)
	}
	after, _ := json.Marshal(p)
	if string(before) != string(after) {
		t.Fatalf("gölge Problem'i değiştirdi:\n%s\n%s", before, after)
	}
	if len(withShadow.awaiting) != 0 {
		t.Fatal("gölge ekip-maili talep defterine dokundu")
	}
	// Gölge satırı yazıldı — tekrar memo'su sayesinde YALNIZ bir kez — ve
	// hiçbir dedup okuyucusunun göreceği türde değil.
	mu.Lock()
	defer mu.Unlock()
	if len(rows) != 1 {
		t.Fatalf("gölge satırı %d kez yazıldı, istenen 1", len(rows))
	}
	r := rows[0]
	if r.ChannelKind != chstore.NotifyShadowChannelKind || r.RelatedKind != chstore.NotifyShadowRelatedKind || r.OK || r.RelatedID != p.ID {
		t.Fatalf("gölge satırı: %+v", r)
	}
	if r.Error != chstore.NotifyShadowWouldSend {
		t.Fatalf("1518 rampası gölgede 'gönderilirdi' olmalı, %q", r.Error)
	}

	// 2. kablo — AST.
	src, err := os.ReadFile("notify.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "notify.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var body *ast.BlockStmt
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "SendProblemAlert" {
			body = fd.Body
		}
	}
	if body == nil {
		t.Fatal("SendProblemAlert bulunamadı")
	}
	hookAt, verdictAt, teamAt := -1, -1, -1
	for i, st := range body.List {
		text := string(src[fset.Position(st.Pos()).Offset:fset.Position(st.End()).Offset])
		if strings.Contains(text, "shadowNotifyPolicy") {
			es, ok := st.(*ast.ExprStmt)
			if !ok {
				t.Fatalf("kanca çıplak ifade değil: %s", text)
			}
			if _, ok := es.X.(*ast.CallExpr); !ok {
				t.Fatalf("kanca çağrı değil: %s", text)
			}
			if hookAt >= 0 {
				t.Fatal("kanca iki kez çağrılıyor")
			}
			hookAt = i
		}
		if strings.Contains(text, "verdictSilenced") && verdictAt < 0 {
			verdictAt = i
		}
		if strings.Contains(text, "sendTeamMail") && teamAt < 0 {
			teamAt = i
		}
	}
	if !(verdictAt >= 0 && hookAt > verdictAt && teamAt > hookAt) {
		t.Fatalf("kanca sırası: verdict=%d hook=%d team=%d", verdictAt, hookAt, teamAt)
	}

	// 3. kanca gövdesi: iş goroutine'de; dönüş tipi yok.
	ps, err := os.ReadFile("policy_shadow.go")
	if err != nil {
		t.Fatal(err)
	}
	hook := funcBody(t, string(ps), "func (n *Notifier) shadowNotifyPolicy(")
	if !strings.Contains(hook, "go func(p chstore.Problem)") || !strings.HasPrefix(hook, "func (n *Notifier) shadowNotifyPolicy(ctx context.Context, p chstore.Problem) {") {
		t.Fatalf("kanca imzası / goroutine:\n%s", hook)
	}
	// Exception P1 anonsu: kanca sendOne'dan önce, çıplak.
	ex, err := os.ReadFile("exception_notifier.go")
	if err != nil {
		t.Fatal(err)
	}
	mail := funcBody(t, string(ex), "func (e *ExceptionNotifier) sendExceptionMail(")
	if i, j := strings.Index(mail, "\te.n.shadowNotifyPolicy("), strings.Index(mail, "e.n.sendOne("); i < 0 || j < i {
		t.Fatal("exception anonsu gölge kancası sendOne'dan önce değil")
	}
}

func TestShadowOffModeWritesNothing(t *testing.T) {
	var mu sync.Mutex
	var rows []chstore.NotificationLog
	n := shadowTestNotifier(&rows, &mu)
	off := chstore.DefaultNotifyPolicy()
	off.Mode = chstore.NotifyModeOff
	chstore.SetNotifyPolicy(off)
	t.Cleanup(func() { chstore.SetNotifyPolicy(chstore.DefaultNotifyPolicy()) })
	n.shadowNotifyPolicy(context.Background(), shadowProblem())
	n.shadow.wg.Wait()
	if len(rows) != 0 {
		t.Fatalf("off kipinde satır yazıldı: %+v", rows)
	}
	// Çözülme / ack olayları ölçülmez (R4 yaşam döngüsü).
	chstore.SetNotifyPolicy(chstore.DefaultNotifyPolicy())
	p := shadowProblem()
	p.Status = "resolved"
	n.shadowNotifyPolicy(context.Background(), p)
	n.shadow.wg.Wait()
	if len(rows) != 0 {
		t.Fatal("resolved olayı ölçüldü")
	}
}

func TestShadowRowHold(t *testing.T) {
	p := shadowProblem()
	std := chstore.StandardizeProblem(p, chstore.StdFacts{})
	r := shadowRow(p, std, chstore.NotifyDecision{Hold: chstore.NotifyHoldDedupe, Priority: "P1", Confidence: "high"}, chstore.NotifyModeShadow)
	if r.Error != "hold:dedupe" || r.ChannelName != chstore.NotifyModeShadow || !strings.Contains(r.BodyPreview, "güven high") {
		t.Fatalf("satır: %+v", r)
	}
}

func TestShadowMemoKeyOncePerHour(t *testing.T) {
	var s policyShadow
	now := time.Now()
	if !s.claim("k", now) || s.claim("k", now.Add(30*time.Minute)) || !s.claim("k", now.Add(61*time.Minute)) {
		t.Fatal("memo saatte bir")
	}
	p := shadowProblem()
	q := p
	q.Priority = "P2"
	if shadowMemoKey(p) == shadowMemoKey(q) {
		t.Fatal("öncelik değişimi yeni ölçüm olmalı")
	}
}
