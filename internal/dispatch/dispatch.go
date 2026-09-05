// Package dispatch turns newly-ingested events into delivery rows: one per
// enabled endpoint whose filter matches the event type.
package dispatch

import (
	"context"
	"log/slog"
	"time"

	"github.com/akshar27/hookrelay/internal/store"
	"github.com/akshar27/hookrelay/internal/store/db"
	"github.com/google/uuid"
)

// Dispatcher consumes event ids from an in-process channel and periodically
// sweeps for any it missed (channel full, or a restart between ingest and
// fan-out). Fan-out is idempotent, so processing an event twice is harmless.
type Dispatcher struct {
	store         *store.Store
	log           *slog.Logger
	ch            chan uuid.UUID
	sweepInterval time.Duration
	sweepBatch    int32
}

func New(st *store.Store, log *slog.Logger) *Dispatcher {
	return &Dispatcher{
		store:         st,
		log:           log.With("component", "dispatcher"),
		ch:            make(chan uuid.UUID, 1024),
		sweepInterval: 30 * time.Second,
		sweepBatch:    500,
	}
}

// Notify signals that an event is ready to fan out. Non-blocking: if the buffer
// is full the periodic sweep will pick the event up.
func (d *Dispatcher) Notify(eventID uuid.UUID) {
	select {
	case d.ch <- eventID:
	default:
		d.log.Warn("dispatch channel full, deferring to sweep", "event", eventID)
	}
}

// Run blocks until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(d.sweepInterval)
	defer ticker.Stop()
	d.sweep(ctx) // catch anything left from a previous run

	for {
		select {
		case <-ctx.Done():
			return
		case id := <-d.ch:
			d.processLogged(ctx, id)
		case <-ticker.C:
			d.sweep(ctx)
		}
	}
}

func (d *Dispatcher) sweep(ctx context.Context) {
	ids, err := d.store.Q.ListUnfannedEventIDs(ctx, d.sweepBatch)
	if err != nil {
		d.log.Error("sweep query failed", "err", err)
		return
	}
	for _, id := range ids {
		d.processLogged(ctx, id)
	}
}

func (d *Dispatcher) processLogged(ctx context.Context, id uuid.UUID) {
	if err := d.ProcessEvent(ctx, id); err != nil {
		d.log.Error("fan-out failed", "event", id, "err", err)
	}
}

// ProcessEvent fans one event out and marks it done, in a single transaction.
// Safe to call more than once for the same event. Exported for tests.
func (d *Dispatcher) ProcessEvent(ctx context.Context, eventID uuid.UUID) error {
	tx, err := d.store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := d.store.Q.WithTx(tx)

	ev, err := q.GetEvent(ctx, eventID)
	if err != nil {
		return err
	}
	if ev.FannedOut {
		return nil
	}

	n, err := q.FanOutEvent(ctx, db.FanOutEventParams{EventID: eventID, EventType: ev.Type})
	if err != nil {
		return err
	}
	if err := q.MarkEventFannedOut(ctx, eventID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	d.log.Info("fanned out", "event", eventID, "type", ev.Type, "deliveries", n)
	return nil
}
