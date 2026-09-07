package notify

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// SMTPConfig carries mail sending settings (env FERP_SMTP_*). Defaults target
// the compose mailpit service; production sets a real relay + credentials.
type SMTPConfig struct {
	Host     string
	Port     int
	From     string
	Username string
	Password string
	UseTLS   bool
	Timeout  time.Duration
}

// LoadSMTPConfig reads FERP_SMTP_* env with mailpit defaults.
func LoadSMTPConfig(get func(key, def string) string) SMTPConfig {
	port := 1025
	if v := get("FERP_SMTP_PORT", "1025"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			port = n
		}
	}
	return SMTPConfig{
		Host:     get("FERP_SMTP_HOST", "localhost"),
		Port:     port,
		From:     get("FERP_SMTP_FROM", "forgeerp@localhost"),
		Username: get("FERP_SMTP_USER", ""),
		Password: get("FERP_SMTP_PASS", ""),
		UseTLS:   get("FERP_SMTP_TLS", "0") == "1",
		Timeout:  10 * time.Second,
	}
}

// Mail is one plain-text message (Dolibarr CMailFile text path; HTML follows).
type Mail struct {
	To      []string
	Subject string
	Body    string
}

// Validate checks mail invariants.
func (m Mail) Validate() error {
	if len(m.To) == 0 {
		return errors.New("notify: no recipients")
	}
	for _, to := range m.To {
		if !strings.Contains(to, "@") {
			return fmt.Errorf("notify: bad recipient %q", to)
		}
	}
	if strings.TrimSpace(m.Subject) == "" {
		return errors.New("notify: empty subject")
	}
	return nil
}

// SMTPSender delivers mail over SMTP (stdlib only) and implements the
// outbox Sender interface for the email channel (Dolibarr CMailFile path).
type SMTPSender struct {
	cfg SMTPConfig
}

// NewSMTPSender builds a sender from config.
func NewSMTPSender(cfg SMTPConfig) *SMTPSender { return &SMTPSender{cfg: cfg} }

// Addr returns host:port.
func (s *SMTPSender) Addr() string { return fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port) }

// Send implements Sender for outbox email items (recipient → To).
func (s *SMTPSender) Send(ctx context.Context, item OutboxItem) error {
	if item.Channel != "" && item.Channel != "email" {
		return fmt.Errorf("notify: smtp sender cannot handle channel %q", item.Channel)
	}
	return s.SendMail(ctx, Mail{To: []string{item.Recipient}, Subject: item.Subject, Body: item.Body})
}

// SendMail delivers one message, honouring ctx cancellation via dial timeout.
func (s *SMTPSender) SendMail(ctx context.Context, m Mail) error {
	if err := m.Validate(); err != nil {
		return err
	}
	if s.cfg.From == "" {
		return errors.New("notify: empty sender")
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "From: %s\r\n", s.cfg.From)
	fmt.Fprintf(&sb, "To: %s\r\n", strings.Join(m.To, ", "))
	fmt.Fprintf(&sb, "Subject: %s\r\n", m.Subject)
	sb.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
	sb.WriteString(m.Body)

	dialer := &net.Dialer{Timeout: s.cfg.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", s.Addr())
	if err != nil {
		return fmt.Errorf("notify: dial smtp: %w", err)
	}
	host, _, _ := net.SplitHostPort(s.Addr())
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("notify: smtp client: %w", err)
	}
	defer func() { _ = c.Quit() }()
	if s.cfg.UseTLS {
		tlsCfg := &tls.Config{ServerName: host} //nolint:gosec // opt-in relay TLS
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("notify: starttls: %w", err)
		}
	}
	if s.cfg.Username != "" {
		auth := smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, host)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("notify: auth: %w", err)
		}
	}
	if err := c.Mail(s.cfg.From); err != nil {
		return fmt.Errorf("notify: mail from: %w", err)
	}
	for _, to := range m.To {
		if err := c.Rcpt(to); err != nil {
			return fmt.Errorf("notify: rcpt %s: %w", to, err)
		}
	}
	wc, err := c.Data()
	if err != nil {
		return fmt.Errorf("notify: data: %w", err)
	}
	if _, err := fmt.Fprint(wc, sb.String()); err != nil {
		_ = wc.Close()
		return fmt.Errorf("notify: write: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("notify: commit: %w", err)
	}
	return nil
}
