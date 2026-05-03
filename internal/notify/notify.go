// Package notify dispatches Problem alerts to user-configured notification
// channels (email today; slack/webhook are stubs for the next iteration).
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
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cenk/qmetry/internal/chstore"
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

// Notifier is the small surface the evaluator + anomaly worker call into.
// Construction is cheap; share one across the process.
type Notifier struct {
	store *chstore.Store

	mu       sync.RWMutex
	smtp     SMTPSettings
	smtpRead time.Time // last refresh — short TTL avoids hammering CH on every alert
}

func New(store *chstore.Store) *Notifier {
	return &Notifier{store: store}
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
func (n *Notifier) SendProblemAlert(ctx context.Context, p chstore.Problem) {
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
		Service:     "qmetry",
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
	case "slack", "webhook":
		return errors.New("channel type not implemented yet")
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
