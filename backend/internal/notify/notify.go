// Package notify implements templates, the notification outbox with retry
// backoff (Dolibarr notify/notify_def + agenda reminders), signed webhook-out
// dispatch (webhook/zapier trigger fan-out), and the fixed-interval scheduler
// (llx_cronjob equivalent).
package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"
)

// Template renders channel messages (Dolibarr email templates).
type Template struct {
	ID       int64  `json:"id"`
	EntityID int64  `json:"entity_id"`
	Code     string `json:"code"`
	Channel  string `json:"channel"`
	Subject  string `json:"subject"`
	Body     string `json:"body"`
}

// OutboxItem is one queued delivery with attempt tracking.
type OutboxItem struct {
	ID         int64      `json:"id"`
	EntityID   int64      `json:"entity_id"`
	Channel    string     `json:"channel"`
	Recipient  string     `json:"recipient"`
	Subject    string     `json:"subject"`
	Body       string     `json:"body"`
	Attempts   int        `json:"attempts"`
	NextTryAt  time.Time  `json:"next_try_at"`
	SentAt     *time.Time `json:"sent_at"`
	Error      string     `json:"error"`
}

// MaxAttempts caps redelivery; backoff: 1m, 5m, 30m, 2h, then dead-letter (stays unsent).
func backoff(attempt int) time.Duration {
	steps := []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour}
	if attempt < 0 {
		return steps[0]
	}
	if attempt >= len(steps) {
		return -1
	}
	return steps[attempt]
}

// MaxAttempts is the delivery cap.
const MaxAttempts = 4

// NextTry computes the retry instant; ok=false means dead-lettered.
func NextTry(now time.Time, attempts int) (at time.Time, ok bool) {
	d := backoff(attempts)
	if d < 0 || attempts >= MaxAttempts {
		return time.Time{}, false
	}
	return now.Add(d), true
}

// Sender delivers one item (SMTP in production; fake in tests).
type Sender interface {
	Send(ctx context.Context, item OutboxItem) error
}

// Dispatcher drains due items with retry accounting.
type Dispatcher struct {
	Sender Sender
	Now    func() time.Time
}

func (d Dispatcher) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now().UTC()
}

// Dispatch attempts delivery; on failure it records attempts/next_try_at.
// Returns sent=true on success.
func (d Dispatcher) Dispatch(ctx context.Context, item *OutboxItem) (sent bool) {
	now := d.now()
	if err := d.Sender.Send(ctx, *item); err != nil {
		item.Attempts++
		item.Error = err.Error()
		if at, ok := NextTry(now, item.Attempts-1); ok && item.Attempts < MaxAttempts {
			item.NextTryAt = at
		}
		return false
	}
	item.SentAt = &now
	return true
}

// Webhook dispatches signed event POSTs (HMAC-SHA256 over the body).
type Webhook struct {
	ID      int64  `json:"id"`
	Subject string `json:"subject"` // prefix match on event subject
	URL     string `json:"url"`
	Secret  string `json:"-"`
	Enabled bool   `json:"enabled"`
}

// Sign computes the X-ForgeERP-Signature header value.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// Deliver POSTs payload to the webhook URL with signature + timeout.
func Deliver(ctx context.Context, client *http.Client, wh Webhook, payload any) (int, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, wh.URL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if wh.Secret != "" {
		req.Header.Set("X-ForgeERP-Signature", Sign(wh.Secret, body))
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return resp.StatusCode, errors.New("notify: webhook non-2xx")
	}
	return resp.StatusCode, nil
}

// Job is a fixed-interval scheduled task (Dolibarr llx_cronjob simplified to
// interval scheduling; cron expressions deferred to a later phase if needed).
type Job struct {
	ID         int64     `json:"id"`
	EntityID   int64     `json:"entity_id"`
	Code       string    `json:"code"`
	Interval   time.Duration `json:"-"`
	IntervalS  int64     `json:"interval_s"`
	NextRunAt  time.Time `json:"next_run_at"`
	Enabled    bool      `json:"enabled"`
}

// Due reports whether the job should run now.
func (j Job) Due(now time.Time) bool { return j.Enabled && !now.Before(j.NextRunAt) }

// Advance computes the next run after a completion.
func (j *Job) Advance(from time.Time) {
	j.NextRunAt = from.Add(j.Interval)
	j.IntervalS = int64(j.Interval / time.Second)
}

// Registry is an in-process job store for the monolith stage.
type Registry struct {
	mu   sync.Mutex
	jobs map[string]*Job
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry { return &Registry{jobs: map[string]*Job{}} }

// Register adds or replaces a job.
func (r *Registry) Register(j *Job) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if j.Interval == 0 && j.IntervalS > 0 {
		j.Interval = time.Duration(j.IntervalS) * time.Second
	}
	r.jobs[j.Code] = j
}

// Due returns codes due at now.
func (r *Registry) Due(now time.Time) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for code, j := range r.jobs {
		if j.Due(now) {
			out = append(out, code)
		}
	}
	return out
}
