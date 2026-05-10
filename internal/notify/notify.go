// Package notify dispatches Problem alerts to user-configured notification
// channels: email (SMTP), Slack/Mattermost (incoming-webhook compatible),
// generic webhook (raw JSON POST), and WhatsApp (via Twilio's Messages API).
//
// Two design decisions worth calling out:
//
//  1. SMTP credentials live in the system_settings ClickHouse table — not
//     in config.yaml — so the admin UI can rotate them without a restart.
//     Reads happen via the in-memory cache below; writes invalidate it.
//  2. Sending is fire-and-forget from the evaluator/anomaly tick. Failures
//     are logged but do not block or retry — alert spam from a flaky SMTP
//     is worse than a missed alert (which the operator notices anyway via
//     the Problems page).
package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const settingsKey = "smtp"

// SMTPSettings is the JSON shape we persist under system_settings["smtp"].
type SMTPSettings struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	From       string `json:"from"`
	FromName   string `json:"fromName"`
	StartTLS   bool   `json:"startTLS"`
	SkipVerify bool   `json:"skipVerify"`
}

func (s SMTPSettings) Configured() bool {
	return s.Host != "" && s.Port != 0 && s.From != ""
}

// EmailChannelConfig is the per-channel JSON for type=email.
type EmailChannelConfig struct {
	Recipients []string `json:"recipients"` // one or more; comma-split also supported in UI
}

// SlackChannelConfig powers both type=slack and type=mattermost — they
// accept the same incoming-webhook JSON shape.
type SlackChannelConfig struct {
	WebhookURL string `json:"webhookUrl"`
}

// WebhookChannelConfig is the generic JSON-POST channel; the body is
// the raw chstore.Problem so the receiver can route it however it likes.
type WebhookChannelConfig struct {
	URL string `json:"url"`
}

// WhatsAppChannelConfig wraps Twilio's WhatsApp messaging API.
//
// AccountSid + AuthToken are the standard Twilio API credentials.
// From is the sender number including the "whatsapp:" prefix and E.164
// formatting (e.g. "whatsapp:+14155238886" — the Twilio sandbox number).
// To is one or more recipient numbers, same format.
//
// Twilio is the de-facto standard for programmatic WhatsApp because it
// owns the relationship with Meta on the user's behalf. Meta's direct
// Cloud API works too but requires per-template approval, not viable
// for ad-hoc alert text.
type WhatsAppChannelConfig struct {
	AccountSid string   `json:"accountSid"`
	AuthToken  string   `json:"authToken"`
	From       string   `json:"from"`
	To         []string `json:"to"`
}

// EventPublisher is the minimum interface we need from the SSE
// broker. Defined here as an interface (rather than depending
// on internal/sse directly) so the notify package stays
// import-cycle-free — the broker can use chstore types if it
// ever wants to without circling back.
type EventPublisher interface {
	Publish(kind string, payload any)
}

// Notifier is the small surface the evaluator + anomaly worker call into.
// Construction is cheap; share one across the process.
type Notifier struct {
	store *chstore.Store

	mu       sync.RWMutex
	smtp     SMTPSettings
	smtpRead time.Time // last refresh — short TTL avoids hammering CH on every alert

	// bus is the optional SSE broker. When set, every problem-
	// open / problem-resolve / anomaly fire publishes a typed
	// event so connected browser tabs update in <1s instead of
	// waiting for the next poll. nil = behave as before
	// (poll-only).
	bus EventPublisher
}

func New(store *chstore.Store) *Notifier {
	return &Notifier{store: store}
}

// SetEventBus wires the SSE broker. Called once at startup; the
// notifier stores the reference and publishes Problem.* events
// from SendProblemAlert.
func (n *Notifier) SetEventBus(bus EventPublisher) {
	n.mu.Lock()
	n.bus = bus
	n.mu.Unlock()
}

// Publish surfaces the event bus to other workers (evaluator,
// anomaly detector) that already have a Notifier reference but
// shouldn't import the broker directly. Safe pass-through; nil
// bus = no-op.
func (n *Notifier) Publish(kind string, payload any) {
	n.mu.RLock()
	bus := n.bus
	n.mu.RUnlock()
	if bus != nil {
		bus.Publish(kind, payload)
	}
}

// SMTP returns the cached settings (read-through with 30s TTL). Safe for
// concurrent callers.
func (n *Notifier) SMTP(ctx context.Context) SMTPSettings {
	n.mu.RLock()
	if time.Since(n.smtpRead) < 30*time.Second {
		s := n.smtp
		n.mu.RUnlock()
		return s
	}
	n.mu.RUnlock()
	return n.refreshSMTP(ctx)
}

func (n *Notifier) refreshSMTP(ctx context.Context) SMTPSettings {
	n.mu.Lock()
	defer n.mu.Unlock()
	raw, err := n.store.GetSetting(ctx, settingsKey)
	if err != nil {
		log.Printf("[notify] read smtp settings: %v", err)
		return n.smtp // keep last good copy
	}
	if len(raw) == 0 {
		n.smtp = SMTPSettings{}
		n.smtpRead = time.Now()
		return n.smtp
	}
	var s SMTPSettings
	if err := json.Unmarshal(raw, &s); err != nil {
		log.Printf("[notify] decode smtp settings: %v", err)
		return n.smtp
	}
	n.smtp = s
	n.smtpRead = time.Now()
	return n.smtp
}

// SaveSMTP persists new settings and busts the in-memory cache.
func (n *Notifier) SaveSMTP(ctx context.Context, s SMTPSettings) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := n.store.PutSetting(ctx, settingsKey, raw); err != nil {
		return err
	}
	n.mu.Lock()
	n.smtp = s
	n.smtpRead = time.Now()
	n.mu.Unlock()
	return nil
}

// SendProblemAlert fans out a problem to every channel that wants this
// severity. Errors are logged per-channel; partial failures don't abort
// the rest.
//
// Also fires an SSE event so the browser-side React Query
// caches invalidate immediately rather than waiting for the
// next poll. Kind is "problem.open" / "problem.resolve" so the
// client can decide what to invalidate (open events bump the
// sidebar badge; resolve events also do, plus drop a row from
// the open list).
func (n *Notifier) SendProblemAlert(ctx context.Context, p chstore.Problem) {
	// Enrich with the runbook URL — pulled from the firing
	// alert rule (preferred) or the service catalog metadata
	// (fallback). Done here, not at problem creation, so an
	// operator who edits a runbook URL after a rule fires
	// still sees the new link in the very next notification
	// (e.g. the resolved-event message).
	if p.RunbookURL == "" {
		enriched := n.store.EnrichProblemsWithRunbooks(ctx, []chstore.Problem{p})
		if len(enriched) > 0 {
			p = enriched[0]
		}
	}
	switch p.Status {
	case "open":
		n.Publish("problem.open", p)
	case "resolved":
		n.Publish("problem.resolve", p)
	}
	channels, err := n.store.EnabledChannelsForSeverity(ctx, p.Severity)
	if err != nil {
		log.Printf("[notify] load channels: %v", err)
		return
	}
	if len(channels) == 0 {
		return
	}
	for _, c := range channels {
		if err := n.sendOne(ctx, c, p); err != nil {
			log.Printf("[notify] %s (%s): %v", c.Name, c.Type, err)
		}
	}
}

// SendTest dispatches a synthetic Problem to a single channel — used by
// the "Send test" button on the settings UI so admins can verify config
// without waiting for a real incident.
func (n *Notifier) SendTest(ctx context.Context, c chstore.NotificationChannel) error {
	test := chstore.Problem{
		ID:          "test",
		RuleID:      "test",
		RuleName:    "Test alert from Coremetry",
		Severity:    "warning",
		Service:     "coremetry",
		Metric:      "test",
		Value:       42,
		Threshold:   10,
		Status:      "open",
		Description: "This is a test notification — your channel is configured correctly.",
		StartedAt:   time.Now().UnixNano(),
	}
	return n.sendOne(ctx, c, test)
}

func (n *Notifier) sendOne(ctx context.Context, c chstore.NotificationChannel, p chstore.Problem) error {
	switch c.Type {
	case "email":
		return n.sendEmail(ctx, c, p)
	case "slack", "mattermost":
		// Mattermost ships an incoming-webhook endpoint that consumes
		// the same JSON Slack does, so one impl covers both. We keep
		// them as distinct channel types so the UI can label them
		// correctly and operators see at a glance which is which.
		return n.sendSlack(ctx, c, p)
	case "webhook":
		return n.sendWebhook(ctx, c, p)
	case "whatsapp":
		return n.sendWhatsApp(ctx, c, p)
	}
	return fmt.Errorf("unknown channel type: %s", c.Type)
}

// ── Email backend ───────────────────────────────────────────────────────────

func (n *Notifier) sendEmail(ctx context.Context, c chstore.NotificationChannel, p chstore.Problem) error {
	smtpCfg := n.SMTP(ctx)
	if !smtpCfg.Configured() {
		return errors.New("SMTP is not configured — set host/port/from in Settings")
	}
	var ec EmailChannelConfig
	if err := json.Unmarshal(c.Config, &ec); err != nil {
		return fmt.Errorf("decode email config: %w", err)
	}
	if len(ec.Recipients) == 0 {
		return errors.New("channel has no recipients")
	}

	subject := fmt.Sprintf("[%s] %s — %s", strings.ToUpper(p.Severity), p.Service, p.RuleName)
	body := buildEmailBody(p)

	from := smtpCfg.From
	fromHeader := from
	if smtpCfg.FromName != "" {
		fromHeader = fmt.Sprintf("%s <%s>", smtpCfg.FromName, from)
	}
	msg := strings.Builder{}
	msg.WriteString("From: " + fromHeader + "\r\n")
	msg.WriteString("To: " + strings.Join(ec.Recipients, ", ") + "\r\n")
	msg.WriteString("Subject: " + subject + "\r\n")
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	msg.WriteString("\r\n")
	msg.WriteString(body)

	addr := net.JoinHostPort(smtpCfg.Host, strconv.Itoa(smtpCfg.Port))
	return sendSMTP(addr, smtpCfg, from, ec.Recipients, []byte(msg.String()))
}

func buildEmailBody(p chstore.Problem) string {
	t := time.Unix(0, p.StartedAt).UTC().Format(time.RFC3339)
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", p.Description)
	fmt.Fprintf(&b, "Service:    %s\n", p.Service)
	fmt.Fprintf(&b, "Rule:       %s\n", p.RuleName)
	fmt.Fprintf(&b, "Severity:   %s\n", strings.ToUpper(p.Severity))
	fmt.Fprintf(&b, "Metric:     %s\n", p.Metric)
	fmt.Fprintf(&b, "Value:      %.2f (threshold %.2f)\n", p.Value, p.Threshold)
	fmt.Fprintf(&b, "Started at: %s\n", t)
	if p.RunbookURL != "" {
		fmt.Fprintf(&b, "Runbook:    %s\n", p.RunbookURL)
	}
	return b.String()
}

// sendSMTP is split out so it can be swapped in tests + handles the
// STARTTLS dance manually because net/smtp's SendMail can't do explicit
// TLS-or-not toggling cleanly with arbitrary verify settings.
func sendSMTP(addr string, cfg SMTPSettings, from string, to []string, msg []byte) error {
	c, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer c.Close()

	if cfg.StartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("server does not advertise STARTTLS")
		}
		tlsCfg := &tls.Config{ServerName: cfg.Host, InsecureSkipVerify: cfg.SkipVerify}
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}
	if cfg.Username != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			return errors.New("server does not advertise AUTH but username is set")
		}
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("mail from: %w", err)
	}
	for _, r := range to {
		if err := c.Rcpt(r); err != nil {
			return fmt.Errorf("rcpt to %s: %w", r, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close body: %w", err)
	}
	return c.Quit()
}

// ── Slack / Mattermost backend ──────────────────────────────────────────────
//
// Both consume the same incoming-webhook JSON. Use a coloured attachment
// keyed off the problem severity so the message renders with a clear
// status stripe in the channel — same convention Grafana / Prometheus /
// PagerDuty alerts use.
func (n *Notifier) sendSlack(ctx context.Context, c chstore.NotificationChannel, p chstore.Problem) error {
	var sc SlackChannelConfig
	if err := json.Unmarshal(c.Config, &sc); err != nil {
		return fmt.Errorf("decode slack config: %w", err)
	}
	if sc.WebhookURL == "" {
		return errors.New("channel has no webhook URL")
	}
	color := severityColor(p.Severity)
	t := time.Unix(0, p.StartedAt).UTC().Format(time.RFC3339)
	fields := []map[string]any{
		{"title": "Service",   "value": p.Service,                                       "short": true},
		{"title": "Severity",  "value": strings.ToUpper(p.Severity),                    "short": true},
		{"title": "Metric",    "value": p.Metric,                                        "short": true},
		{"title": "Value",     "value": fmt.Sprintf("%.2f (threshold %.2f)", p.Value, p.Threshold), "short": true},
		{"title": "Started",   "value": t,                                               "short": false},
	}
	// Runbook link as a clickable Slack mrkdwn field — the
	// oncall on mobile lands on the playbook in one tap.
	if p.RunbookURL != "" {
		fields = append(fields, map[string]any{
			"title": "Runbook",
			"value": fmt.Sprintf("<%s|Open runbook ↗>", p.RunbookURL),
			"short": false,
		})
	}
	body := map[string]any{
		"text": fmt.Sprintf("[%s] %s — %s", strings.ToUpper(p.Severity), p.Service, p.RuleName),
		"attachments": []map[string]any{{
			"color":  color,
			"text":   p.Description,
			"fields": fields,
			"footer": "Coremetry",
		}},
	}
	return postJSON(ctx, sc.WebhookURL, body)
}

// ── Generic webhook backend ─────────────────────────────────────────────────
//
// Posts the raw chstore.Problem JSON so the receiver can route however it
// wants (PagerDuty Events API, n8n, custom Lambda, etc.).
func (n *Notifier) sendWebhook(ctx context.Context, c chstore.NotificationChannel, p chstore.Problem) error {
	var wc WebhookChannelConfig
	if err := json.Unmarshal(c.Config, &wc); err != nil {
		return fmt.Errorf("decode webhook config: %w", err)
	}
	if wc.URL == "" {
		return errors.New("channel has no URL")
	}
	return postJSON(ctx, wc.URL, p)
}

// ── WhatsApp backend (Twilio Messages API) ──────────────────────────────────
//
// Twilio is the de-facto standard for programmatic WhatsApp. The
// Messages API is form-encoded, basic-auth'd with the Account SID +
// Auth Token. Sender + recipient numbers must carry the "whatsapp:"
// prefix and be E.164-formatted.
//
// Multi-recipient channels send N independent requests — Twilio's API
// is one message per call.
func (n *Notifier) sendWhatsApp(ctx context.Context, c chstore.NotificationChannel, p chstore.Problem) error {
	var wc WhatsAppChannelConfig
	if err := json.Unmarshal(c.Config, &wc); err != nil {
		return fmt.Errorf("decode whatsapp config: %w", err)
	}
	if wc.AccountSid == "" || wc.AuthToken == "" {
		return errors.New("twilio credentials (accountSid + authToken) are required")
	}
	if wc.From == "" {
		return errors.New("twilio whatsapp 'from' is required (e.g. whatsapp:+14155238886)")
	}
	if len(wc.To) == 0 {
		return errors.New("channel has no recipients")
	}
	endpoint := fmt.Sprintf("https://api.twilio.com/2010-04-01/Accounts/%s/Messages.json",
		url.PathEscape(wc.AccountSid))
	bodyText := buildWhatsAppText(p)

	cli := &http.Client{Timeout: 10 * time.Second}
	for _, to := range wc.To {
		form := url.Values{}
		form.Set("From", normaliseWhatsAppAddr(wc.From))
		form.Set("To", normaliseWhatsAppAddr(to))
		form.Set("Body", bodyText)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
			strings.NewReader(form.Encode()))
		if err != nil {
			return fmt.Errorf("build twilio request: %w", err)
		}
		req.SetBasicAuth(wc.AccountSid, wc.AuthToken)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := cli.Do(req)
		if err != nil {
			return fmt.Errorf("twilio post (%s): %w", to, err)
		}
		// Twilio returns 201 Created on success, 4xx with a JSON
		// {message: "..."} on rejection (bad number, unsubscribed,
		// throttled, etc.). Surface that to the operator instead of
		// the bare HTTP code.
		if resp.StatusCode >= 300 {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			return fmt.Errorf("twilio %d for %s: %s", resp.StatusCode, to, strings.TrimSpace(string(b)))
		}
		resp.Body.Close()
	}
	return nil
}

// normaliseWhatsAppAddr lets operators paste either "+14155238886" or
// "whatsapp:+14155238886" into the form. Twilio requires the prefix.
func normaliseWhatsAppAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if strings.HasPrefix(addr, "whatsapp:") {
		return addr
	}
	return "whatsapp:" + addr
}

func buildWhatsAppText(p chstore.Problem) string {
	t := time.Unix(0, p.StartedAt).UTC().Format("15:04 MST")
	out := fmt.Sprintf(
		"*[%s]* %s — %s\n%s\nValue: %.2f (threshold %.2f)\nStarted: %s",
		strings.ToUpper(p.Severity), p.Service, p.RuleName,
		p.Description, p.Value, p.Threshold, t,
	)
	if p.RunbookURL != "" {
		out += "\nRunbook: " + p.RunbookURL
	}
	return out
}

// ── Shared HTTP-POST helper ─────────────────────────────────────────────────

func postJSON(ctx context.Context, endpoint string, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	cli := &http.Client{Timeout: 10 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

func severityColor(sev string) string {
	switch strings.ToLower(sev) {
	case "critical":
		return "#ff5252"
	case "warning":
		return "#f59f00"
	default:
		return "#3b82f6"
	}
}
