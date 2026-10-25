package notify

// digest.go — v0.10.1533 (Problem standardı R5; D7 "P3 özeti 09:00 ve 16:00
// yerel", calendar.timezone). enforce kipinde P3 bildirimleri anında gitmez
// (enforce.go → chstore.EnqueueDigest); bu döngü günün özet saatlerinde
// bekleyen kalemleri kanal / ekip başına TEK mesajda gönderir.
//
// Kim alır: P3'ün anında gideceği yerlerin aynısı — her etkin kanal (ciddiyet
// tabanı + eşleşme kuralları + tür süzgeci, MatchesProblem) ve ekip
// yönlendirmesi (tür + ciddiyet + katalog takımları). Webhook ve WhatsApp
// kanalları özet ALMAZ: webhook gövdesi operatörün tek-problem şablonu,
// WhatsApp mesaj tavanı bir listeyi taşımaz — ikisi notification_log'da
// "özet desteklenmiyor" diye görünür.
//
// Lider kilitli (cache.LeaderHolder "notify-digest"), dakikada bir bakar;
// slot kararı SAF (chstore.DigestSlotDue), son slot system_settings'te —
// yeniden başlatma / çok pod aynı özeti iki kez göndermez. Gönderim
// sonrası kalemler kuyruktan düşer; hiçbir kanala gidemeyen kalem de düşer
// (bir sonraki özete taşınması aynı "kimse almıyor" sonucunu tekrarlar,
// yönlendirme işaretiyle zaten görünür).

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net"
	"net/mail"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/tzdefault"
)

const digestLockKey = "coremetry:lock:notify-digest"

// RunDigestLoop — worker lideri; dakikada bir slot denetimi.
func (n *Notifier) RunDigestLoop(ctx context.Context, lock cache.Lock) {
	leader := cache.NewLeaderHolder(lock, digestLockKey, cache.LeaderTTL(time.Minute))
	leader.Start(ctx)
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if leader.IsLeader() {
				tctx, cancel := context.WithTimeout(ctx, 50*time.Second)
				n.digestTick(tctx, time.Now())
				cancel()
			}
		}
	}
}

// digestTick — slot geldiyse bekleyenleri gönder.
func (n *Notifier) digestTick(ctx context.Context, now time.Time) {
	if n == nil || n.store == nil {
		return
	}
	pol := chstore.CurrentNotifyPolicy()
	loc := n.shadowSens().Calendar.Location()
	if loc == nil {
		loc = tzdefault.Location()
	}
	last, err := n.store.ReadDigestLast(ctx)
	if err != nil {
		log.Printf("[notify] özet: son slot okunamadı: %v", err)
		return
	}
	slot, due := chstore.DigestSlotDue(pol.DigestTimes, last, now.In(loc))
	if !due {
		return
	}
	items, err := n.store.ListDigestPending(ctx)
	if err != nil {
		log.Printf("[notify] özet: kuyruk okunamadı: %v", err)
		return
	}
	// Slot işareti GÖNDERİMDEN ÖNCE: gönderim yarıda kalırsa aynı slot ikinci
	// kez denenmez (çift özet, kaçırılan özetten kötü); kalemler kuyruktan
	// yalnız gönderilince düşer → bir sonraki slota kalır.
	if err := n.store.WriteDigestLast(ctx, slot); err != nil {
		log.Printf("[notify] özet: slot işareti yazılamadı: %v — bu slot atlanıyor", err)
		return
	}
	if len(items) == 0 {
		return
	}
	n.sendDigest(ctx, slot, items)
	sigs := make([]string, len(items))
	for i, it := range items {
		sigs[i] = it.Signature
	}
	if err := n.store.ClearDigest(context.WithoutCancel(ctx), sigs); err != nil {
		log.Printf("[notify] özet: kuyruk temizlenemedi: %v", err)
	}
	log.Printf("[notify] P3 özeti gönderildi: slot %s, %d kalem", slot, len(items))
}

// digestGroup — bir hedefin kalemleri.
type digestGroup struct {
	ch    chstore.NotificationChannel
	items []chstore.DigestItem
}

// sendDigest — kalemleri hedeflere dağıtır ve gönderir.
func (n *Notifier) sendDigest(ctx context.Context, slot string, items []chstore.DigestItem) {
	sort.SliceStable(items, func(i, j int) bool { return items[i].StartedAt < items[j].StartedAt })
	groups := map[string]*digestGroup{}
	order := []string{}
	add := func(key string, ch chstore.NotificationChannel, it chstore.DigestItem) {
		g, ok := groups[key]
		if !ok {
			g = &digestGroup{ch: ch}
			groups[key] = g
			order = append(order, key)
		}
		g.items = append(g.items, it)
	}
	tc, tcErr := n.store.GetTeamContacts(ctx)
	mdCache := map[string]*chstore.ServiceMetadata{}
	for _, it := range items {
		md, ok := mdCache[it.Service]
		if !ok {
			md, _ = n.store.GetServiceMetadata(ctx, it.Service)
			mdCache[it.Service] = md
		}
		chans, err := n.store.EnabledChannelsForSeverity(ctx, it.Severity)
		if err == nil {
			in := chstore.MatchInput{Service: it.Service, Metadata: md, Priority: it.Priority, Kind: it.Kind}
			for _, c := range chans {
				if c.MatchRules.MatchesProblem(in) {
					add("ch:"+c.ID, c, it)
				}
			}
		}
		if tcErr == nil && tc.KindAllows(it.Kind) && tc.SeverityAllows(it.Severity) {
			if to := resolveTeamRecipients(md, tc); len(to) > 0 {
				sort.Strings(to)
				cfg, _ := json.Marshal(EmailChannelConfig{Recipients: to})
				add("team:"+strings.Join(to, ","), chstore.NotificationChannel{Name: teamRoutingChannelName, Type: "email", Config: cfg}, it)
			}
		}
	}
	for _, k := range order {
		g := groups[k]
		err := n.sendDigestTo(ctx, slot, g.ch, g.items)
		n.recordDigest(ctx, slot, g.ch, len(g.items), err)
		if err != nil {
			log.Printf("[notify] özet %s (%s): %v", g.ch.Name, g.ch.Type, err)
		}
	}
}

// errDigestUnsupported — özet almayan kanal türü.
type errDigestUnsupported string

func (e errDigestUnsupported) Error() string { return "özet desteklenmiyor: " + string(e) }

func (n *Notifier) sendDigestTo(ctx context.Context, slot string, c chstore.NotificationChannel, items []chstore.DigestItem) error {
	title := digestTitle(slot, len(items))
	switch c.Type {
	case "email":
		return n.sendDigestEmail(ctx, c, title, n.digestText(items), n.digestHTML(title, items))
	case "slack", "mattermost":
		var sc SlackChannelConfig
		if err := json.Unmarshal(c.Config, &sc); err != nil || sc.WebhookURL == "" {
			return fmt.Errorf("slack config: %v", err)
		}
		return postJSON(ctx, sc.WebhookURL, map[string]any{"text": title,
			"attachments": []map[string]any{{"color": "#6b7280", "text": n.digestText(items), "footer": "Coremetry"}}})
	case "teams":
		var tcfg TeamsChannelConfig
		if err := json.Unmarshal(c.Config, &tcfg); err != nil || tcfg.WebhookURL == "" {
			return fmt.Errorf("teams config: %v", err)
		}
		return postJSON(ctx, tcfg.WebhookURL, map[string]any{"@type": "MessageCard", "@context": "https://schema.org/extensions",
			"summary": title, "title": title, "themeColor": "6b7280",
			"text": strings.ReplaceAll(html.EscapeString(n.digestText(items)), "\n", "<br>")})
	case "zoomchat":
		var zc ZoomChatChannelConfig
		if err := json.Unmarshal(c.Config, &zc); err != nil {
			return fmt.Errorf("zoom config: %w", err)
		}
		tok, err := n.zoomAccessToken(ctx, zc.AccountID, zc.ClientID, zc.ClientSecret, zc.OAuthBaseURL, zc.InsecureSkipVerify)
		if err != nil {
			return fmt.Errorf("zoom oauth: %w", err)
		}
		payload := map[string]any{"message": title + "\n" + n.digestText(items)}
		if zc.ChannelID != "" {
			payload["to_channel"] = zc.ChannelID
		} else {
			payload["to_contact"] = zc.ToContact
		}
		return postZoomChatMessage(ctx, tok, payload, zc.APIBaseURL, zc.InsecureSkipVerify)
	}
	return errDigestUnsupported(c.Type)
}

// digestTitle — SAF: "Coremetry · P3 özeti — 25.10 09:00 (7 kalem)".
func digestTitle(slot string, n int) string {
	when := slot
	if t, err := time.Parse("2006-01-02 15:04", slot); err == nil {
		when = t.Format("02.01 15:04")
	}
	return fmt.Sprintf("Coremetry · P3 özeti — %s (%d kalem)", when, n)
}

// digestLine — SAF: tek kalem satırı.
func digestLine(it chstore.DigestItem, now time.Time, loc *time.Location) string {
	conf := confLabelTR[it.Confidence]
	parts := []string{it.Headline}
	if it.Impact != "" {
		parts = append(parts, it.Impact)
	}
	if conf != "" {
		parts = append(parts, "güven "+conf)
	}
	parts = append(parts, trSinceLine(it.StartedAt, now, loc))
	line := "• " + strings.Join(parts, " · ")
	if it.Count > 1 {
		line += " (×" + strconv.Itoa(it.Count) + ")"
	}
	return line
}

func (n *Notifier) digestText(items []chstore.DigestItem) string {
	now, loc := time.Now(), tzdefault.Location()
	var b strings.Builder
	b.WriteString("Anında bildirilmeyen P3 problemleri (görünüm önceliği P3; politika: özet).\n\n")
	for _, it := range items {
		b.WriteString(digestLine(it, now, loc))
		if u := n.problemURL(it.ProblemID); u != "" {
			b.WriteString("\n  " + u)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (n *Notifier) digestHTML(title string, items []chstore.DigestItem) string {
	esc := html.EscapeString
	now, loc := time.Now(), tzdefault.Location()
	const font = `font-family:Segoe UI,Roboto,Helvetica,Arial,sans-serif`
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html lang="tr"><body style="margin:0;padding:0" bgcolor="#f3f4f6">`)
	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" bgcolor="#f3f4f6"><tr><td align="center" style="padding:24px 16px">`)
	b.WriteString(`<table role="presentation" width="600" cellpadding="0" cellspacing="0" border="0" bgcolor="#ffffff" style="border:1px solid #e5e7eb"><tr><td style="padding:20px">`)
	b.WriteString(`<table role="presentation" cellpadding="0" cellspacing="0" border="0" width="100%"><tr><td style="` + font + `;font-size:16px;font-weight:600;color:#111827">` + esc(title) + `</td></tr>`)
	b.WriteString(`<tr><td style="` + font + `;padding:6px 0 10px;font-size:12px;color:#6b7280">Anında bildirilmeyen P3 problemleri (görünüm önceliği P3; politika: özet).</td></tr>`)
	for _, it := range items {
		line := esc(strings.TrimPrefix(digestLine(it, now, loc), "• "))
		if u := n.problemURL(it.ProblemID); u != "" {
			line = `<a href="` + esc(u) + `" style="color:#2563eb">` + line + `</a>`
		}
		b.WriteString(`<tr><td style="` + font + `;padding:5px 0;font-size:13px;color:#111827;border-top:1px solid #f3f4f6">` + line + `</td></tr>`)
	}
	b.WriteString(`</table></td></tr></table></td></tr></table></body></html>`)
	return b.String()
}

// sendDigestEmail — özet e-postası (aynı SMTP yolu, konu başlığı yok — özet
// kendi başına bir mesajdır).
func (n *Notifier) sendDigestEmail(ctx context.Context, c chstore.NotificationChannel, subject, plain, htm string) error {
	smtpCfg := n.SMTP(ctx)
	if !smtpCfg.Configured() {
		return fmt.Errorf("SMTP is not configured")
	}
	var ec EmailChannelConfig
	if err := json.Unmarshal(c.Config, &ec); err != nil || len(ec.Recipients) == 0 {
		return fmt.Errorf("email config: no recipients")
	}
	fromHeader := smtpCfg.From
	if smtpCfg.FromName != "" {
		fromHeader = (&mail.Address{Name: smtpCfg.FromName, Address: smtpCfg.From}).String()
	}
	msg, err := composeAltEmail(fromHeader, ec.Recipients, subject, plain, htm)
	if err != nil {
		return err
	}
	return sendSMTP(net.JoinHostPort(smtpCfg.Host, strconv.Itoa(smtpCfg.Port)), smtpCfg, smtpCfg.From, ec.Recipients, msg)
}

// recordDigest — notification_log satırı (related_kind "digest", related_id slot).
func (n *Notifier) recordDigest(ctx context.Context, slot string, c chstore.NotificationChannel, count int, sendErr error) {
	entry := chstore.NotificationLog{
		ChannelKind: c.Type, ChannelName: c.Name, Target: channelTarget(c),
		Subject:     digestTitle(slot, count),
		BodyPreview: fmt.Sprintf("%d P3 kalemi", count),
		RelatedKind: "digest", RelatedID: slot, OK: sendErr == nil,
	}
	if sendErr != nil {
		entry.Error = preview(sendErr.Error(), 500)
	}
	lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := n.store.InsertNotificationLog(lctx, entry); err != nil {
		log.Printf("[notify] özet log: %v", err)
	}
}
