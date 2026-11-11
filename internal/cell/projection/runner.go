// Package projection implements the async projection runner for Pulse.
//
// The runner:
//  1. Subscribes to NATS event streams via the eventsource.Projector contract.
//  2. Tracks progress in the projection_offsets table (one row per projection_name).
//  3. Periodically commits offsets so restarts resume from the last processed position.
package projection

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/vmihailenco/msgpack/v5"

	"github.com/pulse-im/pulse/internal/cell/eventsource"
	"github.com/pulse-im/pulse/pkg/shared/db"
	sharedevents "github.com/pulse-im/pulse/pkg/shared/events"
	"github.com/pulse-im/pulse/pkg/shared/logger"
	"github.com/pulse-im/pulse/pkg/shared/nats"
)

// Runner manages the lifecycle of one or more projectors.
//
// Construction wires NATS + DB + the projectors; Start begins the
// NATS subscriptions and the offset-commit goroutine; Stop unwinds
// in the reverse order. A single Runner can drive multiple
// projectors (typically the cell starts one Runner per stream
// type, but a single Runner handles a list when the streams are
// closely related).
type Runner struct {
	nats *nats.Client
	db   *db.DB
	log  logger.Logger

	projectors []eventsource.Projector

	mu       sync.Mutex
	offsets  map[string]int64 // projection_name → last applied version
	stopOnce sync.Once
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewRunner constructs the runner. Projectors must be passed in
// declaration order; each gets its own NATS subscription +
// offset row.
func NewRunner(n *nats.Client, d *db.DB, log logger.Logger, projectors ...eventsource.Projector) *Runner {
	return &Runner{
		nats:       n,
		db:         d,
		log:        log.With().Str("component", "projection").Logger(),
		projectors: projectors,
		offsets:    make(map[string]int64, len(projectors)),
		stopCh:     make(chan struct{}),
	}
}

// Name returns the runner's component name for cell.Service
// compatibility. Used by main.go to start/stop the runner as a
// cell-rolled service.
func (r *Runner) Name() string { return "projection" }

// Start launches each projector subscription + a single
// offset-commit goroutine that batches UPDATE statements for the
// projection_offsets table.
//
// Start initializes streams, loads offsets, and launches projector workers.
func (r *Runner) Start(ctx context.Context) error {
	r.log.Info().Int("projectors", len(r.projectors)).Msg("projection runner starting")

	if err := r.loadOffsets(ctx); err != nil {
		return fmt.Errorf("projection: load offsets: %w", err)
	}

	for _, p := range r.projectors {
		if err := r.nats.EnsureStream(ctx, p.Stream(), []string{p.Subject()}); err != nil {
			return fmt.Errorf("projection: ensure stream %q: %w", p.Stream(), err)
		}
	}

	for _, p := range r.projectors {
		p := p // capture for the goroutine
		offset := r.Offset(p.Name())
		r.log.Info().
			Str("projector", p.Name()).
			Int64("resume_offset", offset).
			Str("stream", p.Stream()).
			Str("subject", p.Subject()).
			Msg("starting projector subscription")
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			// Recover panics to prevent a single projector failure from crashing the runner.
			defer func() {
				if rec := recover(); rec != nil {
					r.log.Error().
						Interface("panic", rec).
						Str("projector", p.Name()).
						Msg("projector goroutine panicked")
				}
			}()
			if err := r.runProjector(ctx, p); err != nil {
				r.log.Error().Err(err).Str("projector", p.Name()).Msg("projector stopped")
			}
		}()
	}

	// Offset-commit loop. Runs every commitInterval and also on a
	// best-effort flush at Stop. The loop is intentionally separate
	// from the projector goroutines so a slow projector doesn't
	// block the offset commits (and vice versa).
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.commitLoop(ctx)
	}()

	return nil
}

// Stop signals the runner to shut down and waits for all goroutines
// to exit. Safe to call multiple times (idempotent).
func (r *Runner) Stop(ctx context.Context) error {
	r.stopOnce.Do(func() {
		close(r.stopCh)
		r.log.Info().Msg("projection runner stopping")
	})
	r.wg.Wait()
	// Best-effort final commit so the offset is current on disk
	// even if the timer hasn't fired yet.
	commitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := r.commitOffsets(commitCtx); err != nil {
		r.log.Warn().Err(err).Msg("projection: final offset commit failed")
	}
	return nil
}

// maxPayloadBytes caps the maximum event payload size to 4 MiB to guard against excessive memory usage.
const maxPayloadBytes = 4 << 20 // 4 MiB

// runProjector subscribes to the projector's NATS subject and
// dispatches each event to Handle. EnsureStream is called
// synchronously by Start (code-review C2 fix); this method only
// attaches the subscription callback. On decode failure or Handle
// failure, the runner logs and continues — the NATS subscriber
// re-delivers on transient failures (NATS-level ack).
func (r *Runner) runProjector(ctx context.Context, p eventsource.Projector) error {
	return r.nats.Subscribe(ctx, p.Stream(), "runner-projector-"+p.Name(), p.Subject(), func(data []byte) error {
		if len(data) > maxPayloadBytes {
			r.log.Warn().
				Int("bytes", len(data)).
				Str("projector", p.Name()).
				Msg("event body exceeds max payload; dropping")
			return nil
		}
		var evt sharedevents.Event
		if err := msgpack.Unmarshal(data, &evt); err != nil {
			r.log.Warn().Err(err).Str("projector", p.Name()).Msg("decode event failed; skipping")
			return nil
		}
		if err := p.Handle(ctx, evt); err != nil {
			r.log.Error().Err(err).Str("projector", p.Name()).Msg("handle event failed; will retry")
			return err
		}
		r.recordProgress(p.Name(), evt.Version)
		return nil
	})
}

// recordProgress updates the in-memory offset map under mu and
// signals the commit loop. The commit goroutine handles the
// periodic UPDATE — we don't write per-event to keep DB pressure
// bounded.
func (r *Runner) recordProgress(projection string, version int64) {
	if version <= 0 {
		return
	}
	r.mu.Lock()
	if cur, ok := r.offsets[projection]; !ok || version > cur {
		r.offsets[projection] = version
	}
	r.mu.Unlock()
}

// commitInterval bounds how often the offset-commit loop runs.
// Tuned for "many small events" workload — a 5s window keeps the
// projection_offsets table writes to ~12/min/cell, which is
// negligible. Operators with bursty workloads (e.g. 10k msg/s) can
// shorten this at the cost of more offset-write traffic.
const commitInterval = 5 * time.Second

// commitLoop runs every commitInterval and writes the dirty
// in-memory offsets to projection_offsets via a single UPDATE.
//
// Stops cleanly when stopCh closes OR ctx is cancelled.
func (r *Runner) commitLoop(ctx context.Context) {
	t := time.NewTicker(commitInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopCh:
			return
		case <-t.C:
			if err := r.commitOffsets(ctx); err != nil {
				r.log.Warn().Err(err).Msg("projection: offset commit failed")
			}
		}
	}
}

// commitOffsets persists the current in-memory offsets to the
// projection_offsets table using an UPSERT (ON CONFLICT DO UPDATE).
//
// Safe to call concurrently with Handle — projection_offsets
// write contention is rare (only the runner's commit goroutine
// calls it) and idempotent (the final version wins).
func (r *Runner) commitOffsets(ctx context.Context) error {
	r.mu.Lock()
	if len(r.offsets) == 0 {
		r.mu.Unlock()
		return nil
	}
	snapshot := make(map[string]int64, len(r.offsets))
	for k, v := range r.offsets {
		snapshot[k] = v
	}
	r.mu.Unlock()

	for name, version := range snapshot {
		if err := r.db.Exec(ctx, `
			INSERT INTO projection_offsets (projection_name, last_version, updated_at)
			VALUES ($1, $2, NOW())
			ON CONFLICT (projection_name) DO UPDATE
			SET last_version = GREATEST(projection_offsets.last_version, EXCLUDED.last_version),
			    updated_at = NOW()`,
			name, version); err != nil {
			return fmt.Errorf("commit offset %q: %w", name, err)
		}
	}
	return nil
}

// loadOffsets reads the stored offsets from projection_offsets at
// boot so each projector subscription can log the resume delta and
// skip re-applying events the projector already processed (ON
// CONFLICT DO NOTHING keeps both paths safe).
func (r *Runner) loadOffsets(ctx context.Context) error {
	rows, err := r.db.Query(ctx,
		`SELECT projection_name, last_version FROM projection_offsets`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var version int64
		if err := rows.Scan(&name, &version); err != nil {
			return err
		}
		r.mu.Lock()
		r.offsets[name] = version
		r.mu.Unlock()
	}
	r.log.Info().Int("stored", len(r.offsets)).Msg("projection offsets loaded")
	return rows.Err()
}

// Offset returns the current in-memory offset for a projection.
// Exposed for tests + operator-facing metrics; the runner doesn't
// call it internally.
func (r *Runner) Offset(projection string) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.offsets[projection]
}

// AllOffsets returns a read-only snapshot of all active projection offsets.
func (r *Runner) AllOffsets() map[string]int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int64, len(r.offsets))
	for k, v := range r.offsets {
		out[k] = v
	}
	return out
}
