// Package worker is the delivery engine: a pool of goroutines that claim due
// deliveries from Postgres, POST the signed payload to the customer endpoint,
// record every attempt, and schedule retries or dead-letter.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/akshar27/hookrelay/internal/secretbox"
	"github.com/akshar27/hookrelay/internal/signing"
	"github.com/akshar27/hookrelay/internal/store"
	"github.com/akshar27/hookrelay/internal/store/db"
	"github.com/google/uuid"
)

const (
	snippetLimit  = 2 << 10 // 2 KB of the response body
	userAgent     = "HookRelay/0.1"
	reapInterval  = 10 * time.Second
	idlePoll      = 1 * time.Second
	busyPoll      = 50 * time.Millisecond
	leaseSlackSec = 30
)

// Notifier lets the worker kick the dispatcher when it emits a delivery.dead event.
type Notifier interface {
	Notify(eventID uuid.UUID)
}

// Pool is the worker pool.
type Pool struct {
	store    *store.Store
	secrets  *secretbox.Box
	notifier Notifier
	log      *slog.Logger
	client   *http.Client

	workers    int
	batchSize  int32
	instanceID string
	backoff    []time.Duration
	now        func() time.Time // overridable in tests
}

// Options configures a Pool. Zero values pick sensible defaults.
type Options struct {
	Workers   int
	BatchSize int32
	Client    *http.Client
	// Backoff overrides the retry schedule (tests use a zero/short table).
	Backoff []time.Duration
}

func New(st *store.Store, secrets *secretbox.Box, notifier Notifier, log *slog.Logger, opts Options) *Pool {
	if opts.Workers <= 0 {
		opts.Workers = 8
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 50
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		}
	}
	backoff := opts.Backoff
	if backoff == nil {
		backoff = defaultBackoff
	}
	host, _ := os.Hostname()
	return &Pool{
		store:      st,
		secrets:    secrets,
		notifier:   notifier,
		log:        log.With("component", "worker"),
		client:     client,
		workers:    opts.Workers,
		batchSize:  opts.BatchSize,
		instanceID: fmt.Sprintf("%s/%d", host, os.Getpid()),
		backoff:    backoff,
		now:        time.Now,
	}
}

// Run starts the workers and the reaper and blocks until ctx is cancelled.
func (p *Pool) Run(ctx context.Context) {
	done := make(chan struct{})
	for i := 0; i < p.workers; i++ {
		go func() {
			p.workerLoop(ctx)
			done <- struct{}{}
		}()
	}
	go p.reaperLoop(ctx)

	<-ctx.Done()
	for i := 0; i < p.workers; i++ {
		<-done
	}
}

func (p *Pool) workerLoop(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		n, err := p.tick(ctx)
		if err != nil {
			p.log.Error("claim/deliver tick failed", "err", err)
		}
		wait := idlePoll
		if n == int(p.batchSize) {
			wait = busyPoll // keep pulling while the queue is deep
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// tick claims one batch and delivers each. Exported behaviour is also reachable
// via RunOnce for tests.
func (p *Pool) tick(ctx context.Context) (int, error) {
	ids, err := p.store.Q.ClaimDueDeliveries(ctx, db.ClaimDueDeliveriesParams{
		BatchSize:    p.batchSize,
		LeaseSeconds: leaseSlackSec + 30, // default; the pipeline uses the endpoint timeout too
		LockedBy:     &p.instanceID,
	})
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		p.deliver(ctx, id)
	}
	return len(ids), nil
}

// RunOnce claims and delivers a single batch. For tests.
func (p *Pool) RunOnce(ctx context.Context) (int, error) { return p.tick(ctx) }

func (p *Pool) deliver(ctx context.Context, deliveryID uuid.UUID) {
	d, err := p.store.Q.GetDeliveryDispatch(ctx, deliveryID)
	if err != nil {
		p.log.Error("load delivery failed", "delivery", deliveryID, "err", err)
		return
	}
	log := p.log.With("delivery", deliveryID, "endpoint", d.EndpointID, "attempt", d.AttemptCount)

	if d.EndpointStatus == "paused" {
		_ = p.store.Q.ReleaseDelivery(ctx, db.ReleaseDeliveryParams{
			ID: deliveryID, NextAttemptAt: p.now().Add(time.Minute),
		})
		return
	}

	secret, err := p.secrets.Open(d.SecretEnc)
	if err != nil {
		log.Error("cannot open endpoint secret", "err", err)
		p.failOrDead(ctx, d, nil, "connection_error", "secret unsealing failed")
		return
	}

	body := []byte(d.Payload)
	sig := signing.Sign(string(secret), deliveryID.String(), p.now(), body)

	timeout := time.Duration(d.TimeoutMs) * time.Millisecond
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, _ := http.NewRequestWithContext(reqCtx, http.MethodPost, d.Url, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Webhook-Id", sig.ID)
	req.Header.Set("Webhook-Timestamp", sig.Timestamp)
	req.Header.Set("Webhook-Signature", sig.Signature)

	start := p.now()
	resp, err := p.client.Do(req)
	duration := p.now().Sub(start)

	headers := redactedHeaders(req.Header)

	switch {
	case err != nil:
		outcome, reason := classifyErr(err)
		p.recordAttempt(ctx, d, headers, nil, "", duration, outcome, err.Error())
		p.failOrDead(ctx, d, nil, reason, err.Error())
		log.Warn("delivery error", "outcome", outcome, "err", err)

	default:
		snippet := readSnippet(resp.Body)
		_ = resp.Body.Close()
		code := int32(resp.StatusCode)

		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			p.recordAttempt(ctx, d, headers, &code, snippet, duration, "success", "")
			_ = p.store.Q.MarkDeliverySucceeded(ctx, db.MarkDeliverySucceededParams{
				ID: deliveryID, LastStatusCode: &code,
			})
			log.Info("delivered", "status", resp.StatusCode, "duration_ms", duration.Milliseconds())

		case resp.StatusCode == http.StatusTooManyRequests:
			p.recordAttempt(ctx, d, headers, &code, snippet, duration, "throttled", "")
			next := p.now().Add(retryAfter(resp.Header, backoffFor(p.backoff, d.AttemptCount)))
			_ = p.store.Q.MarkDeliveryFailed(ctx, db.MarkDeliveryFailedParams{
				ID: deliveryID, NextAttemptAt: next, LastStatusCode: &code,
				LastError: strptr("throttled (429)"),
			})

		case resp.StatusCode >= 400 && resp.StatusCode < 500:
			p.recordAttempt(ctx, d, headers, &code, snippet, duration, "http_error", "")
			p.failOrDead(ctx, d, &code, "http_error_4xx", fmt.Sprintf("endpoint returned %d", resp.StatusCode))

		default: // 3xx / 5xx
			p.recordAttempt(ctx, d, headers, &code, snippet, duration, "http_error", "")
			p.failOrDead(ctx, d, &code, "http_error", fmt.Sprintf("endpoint returned %d", resp.StatusCode))
		}
	}
}

// failOrDead schedules the next attempt, or dead-letters if the cap is reached.
func (p *Pool) failOrDead(ctx context.Context, d db.GetDeliveryDispatchRow, code *int32, reason, msg string) {
	limit := d.MaxAttempts
	if reason == "http_error_4xx" && d.Max4xxAttempts < limit {
		limit = d.Max4xxAttempts
	}

	if d.AttemptCount >= limit {
		_ = p.store.Q.MarkDeliveryDead(ctx, db.MarkDeliveryDeadParams{
			ID: d.DeliveryID, LastStatusCode: code, LastError: strptr(msg),
		})
		p.emitDead(ctx, d, code)
		p.log.Warn("delivery dead-lettered", "delivery", d.DeliveryID, "attempts", d.AttemptCount)
		return
	}

	next := p.now().Add(backoffFor(p.backoff, d.AttemptCount))
	_ = p.store.Q.MarkDeliveryFailed(ctx, db.MarkDeliveryFailedParams{
		ID: d.DeliveryID, NextAttemptAt: next, LastStatusCode: code, LastError: strptr(msg),
	})
}

// emitDead publishes a delivery.dead event so producers can alert on it.
func (p *Pool) emitDead(ctx context.Context, d db.GetDeliveryDispatchRow, code *int32) {
	payload, _ := json.Marshal(map[string]any{
		"delivery_id":      d.DeliveryID,
		"endpoint_id":      d.EndpointID,
		"event_id":         d.EventID,
		"original_type":    d.EventType,
		"last_status_code": code,
	})
	ev, err := p.store.Q.InsertEvent(ctx, db.InsertEventParams{
		ApiKeyID: d.ApiKeyID,
		Type:     "delivery.dead",
		Payload:  payload,
	})
	if err != nil {
		p.log.Error("could not emit delivery.dead event", "err", err)
		return
	}
	if p.notifier != nil {
		p.notifier.Notify(ev.ID)
	}
}

func (p *Pool) recordAttempt(ctx context.Context, d db.GetDeliveryDispatchRow, headers json.RawMessage,
	code *int32, snippet string, dur time.Duration, outcome, errStr string) {
	var errp *string
	if errStr != "" {
		errp = &errStr
	}
	var snip *string
	if snippet != "" {
		snip = &snippet
	}
	ms := int32(dur.Milliseconds())
	if err := p.store.Q.InsertAttempt(ctx, db.InsertAttemptParams{
		DeliveryID:      d.DeliveryID,
		N:               d.AttemptCount,
		RequestHeaders:  headers,
		StatusCode:      code,
		ResponseSnippet: snip,
		DurationMs:      ms,
		Outcome:         outcome,
		Error:           errp,
	}); err != nil {
		p.log.Error("insert attempt failed", "delivery", d.DeliveryID, "err", err)
	}
}

func (p *Pool) reaperLoop(ctx context.Context) {
	t := time.NewTicker(reapInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.ReapOnce(ctx)
		}
	}
}

// reapOnce flips leases that outlived their worker back to due, recording a
// worker_lost attempt so it counts toward max_attempts. For tests.
func (p *Pool) ReapOnce(ctx context.Context) int {
	stale, err := p.store.Q.ReapStaleDeliveries(ctx)
	if err != nil {
		p.log.Error("reaper query failed", "err", err)
		return 0
	}
	for _, s := range stale {
		_ = p.store.Q.InsertAttempt(ctx, db.InsertAttemptParams{
			DeliveryID: s.ID, N: s.AttemptCount, RequestHeaders: json.RawMessage(`{}`),
			DurationMs: 0, Outcome: "worker_lost", Error: strptr("worker lease expired"),
		})
	}
	if len(stale) > 0 {
		p.log.Warn("reaped stale deliveries", "count", len(stale))
	}
	return len(stale)
}

// --- helpers -------------------------------------------------------------

func classifyErr(err error) (outcome, reason string) {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout", "timeout"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout", "timeout"
	}
	return "connection_error", "connection_error"
}

func readSnippet(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, snippetLimit))
	return string(b)
}

func redactedHeaders(h http.Header) json.RawMessage {
	m := map[string]string{}
	for k := range h {
		if k == "Webhook-Signature" {
			m[k] = "v1,***"
			continue
		}
		m[k] = h.Get(k)
	}
	b, _ := json.Marshal(m)
	return b
}

func retryAfter(h http.Header, fallback time.Duration) time.Duration {
	if v := h.Get("Retry-After"); v != "" {
		if secs, err := time.ParseDuration(v + "s"); err == nil {
			if secs > time.Hour {
				secs = time.Hour
			}
			return secs
		}
	}
	return fallback
}

func strptr(s string) *string { return &s }
