package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const scorerReconcileInterval = 2 * time.Minute

// ─────────────────────────────────────────────────────────────────────────────
// Scorer configuration
// ─────────────────────────────────────────────────────────────────────────────

// ScorerConfig holds configuration for the background BM25 scorer.
type ScorerConfig struct {
	// MinOverlapToScore is the overlap_score threshold below which candidates
	// are not enriched. Must be <= the insertion OverlapThreshold (default: 0.12).
	MinOverlapToScore float64
	// BufferSize is the scorerCh channel capacity. Default: 512
	BufferSize int
}

// DefaultScorerConfig returns the spec-recommended scorer defaults.
func DefaultScorerConfig() ScorerConfig {
	return ScorerConfig{
		MinOverlapToScore: 0.12,
		BufferSize:        512,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// StartScorer
// ─────────────────────────────────────────────────────────────────────────────

// StartScorer starts the background BM25 scorer goroutine and returns the
// channel to which candidate IDs should be sent (non-blocking).
// The goroutine runs until ctx is cancelled.
// It reads candidate IDs from the returned channel, fetches the candidate's
// overlap_score and burst_b's tokens, runs an FTS5 BM25 query against
// context_bursts_fts using burst_a's tokens as the query, and updates
// candidate_relations.bm25_score.
//
// writeFn is the provider's write function (p.write).
// db is the read-only database connection (for reads).
func StartScorer(
	ctx context.Context,
	db *sql.DB,
	writeFn func(writeOp) error,
	cfg ScorerConfig,
) chan<- string {
	ch := make(chan string, cfg.BufferSize)
	go runScorer(ctx, db, writeFn, ch, ch, cfg)
	return ch
}

// ─────────────────────────────────────────────────────────────────────────────
// runScorer — goroutine body
// ─────────────────────────────────────────────────────────────────────────────

func runScorer(
	ctx context.Context,
	db *sql.DB,
	writeFn func(writeOp) error,
	ch <-chan string,
	sendCh chan<- string,
	cfg ScorerConfig,
) {
	reconcileTicker := time.NewTicker(scorerReconcileInterval)
	defer reconcileTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case id, ok := <-ch:
			if !ok {
				return
			}
			if err := scoreCandidate(ctx, db, writeFn, id, cfg); err != nil {
				// Log WARN and continue — never block on a single failure.
				fmt.Printf("scorer: WARN: failed to score candidate %s: %v\n", id, err)
			}
		case <-reconcileTicker.C:
			// Re-enqueue any candidates still at bm25_score=0 that the scorer
			// missed (channel overflow, server restart, or threshold mismatch).
			if err := ReconcileUnenrichedCandidates(ctx, db, sendCh); err != nil {
				fmt.Printf("scorer: WARN: reconcile failed: %v\n", err)
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// scoreCandidate — per-ID scoring logic
// ─────────────────────────────────────────────────────────────────────────────

func scoreCandidate(
	ctx context.Context,
	db *sql.DB,
	writeFn func(writeOp) error,
	id string,
	cfg ScorerConfig,
) error {
	// 1. Fetch the candidate's overlap_score, burst_a_id, burst_b_id.
	var overlapScore float64
	var burstAID, burstBID string
	err := db.QueryRowContext(ctx,
		`SELECT overlap_score, burst_a_id, burst_b_id FROM candidate_relations WHERE id=?`, id,
	).Scan(&overlapScore, &burstAID, &burstBID)
	if err == sql.ErrNoRows {
		return nil // candidate was deleted or doesn't exist
	}
	if err != nil {
		return fmt.Errorf("scorer: read candidate: %w", err)
	}

	// 2. Skip if below threshold.
	if overlapScore < cfg.MinOverlapToScore {
		return nil
	}

	// 3. Fetch burst_a's tokens (used as the FTS5 query).
	var tokensA string
	err = db.QueryRowContext(ctx,
		`SELECT tokens FROM context_bursts WHERE id=?`, burstAID,
	).Scan(&tokensA)
	if err == sql.ErrNoRows {
		return nil // burst was swept
	}
	if err != nil {
		return fmt.Errorf("scorer: read burst_a tokens: %w", err)
	}

	// 4. Build FTS5 query from burst_a's token set.
	// Use the first 10 tokens as the FTS match query to avoid very long queries.
	tokens := strings.Fields(tokensA)
	if len(tokens) == 0 {
		return nil
	}
	if len(tokens) > 10 {
		tokens = tokens[:10]
	}
	// Join tokens with " OR " for FTS5 MATCH.
	ftsQuery := strings.Join(tokens, " OR ")

	// 5. Run BM25 FTS5 query scoped to burst_b.
	// SQLite bm25() returns negative values (more negative = better match).
	// We negate before storing so higher stored value = more relevant.
	var rawBM25 float64
	err = db.QueryRowContext(ctx,
		`SELECT -bm25(context_bursts_fts) FROM context_bursts_fts WHERE context_bursts_fts MATCH ? AND id=?`,
		ftsQuery, burstBID,
	).Scan(&rawBM25)
	if err == sql.ErrNoRows {
		// burst_b not in FTS index (not inserted yet or swept).
		return nil
	}
	if err != nil {
		return fmt.Errorf("scorer: bm25 query: %w", err)
	}

	// 6. Update the candidate's bm25_score via the write channel.
	return writeFn(func(db *sql.DB) error {
		_, err := db.Exec(`UPDATE candidate_relations SET bm25_score=? WHERE id=?`, rawBM25, id)
		return err
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// ReconcileUnenrichedCandidates — maintenance helper
// ─────────────────────────────────────────────────────────────────────────────

// ReconcileUnenrichedCandidates re-enqueues candidate IDs that are still
// at bm25_score=0 and were created more than 5 minutes ago. This catches
// IDs that were dropped from the scorer channel due to overflow.
// Sends to ch non-blocking (drops if still full).
func ReconcileUnenrichedCandidates(ctx context.Context, db *sql.DB, ch chan<- string) error {
	cutoff := toMs(time.Now().UTC().Add(-5 * time.Minute))
	rows, err := db.QueryContext(ctx,
		`SELECT id FROM candidate_relations WHERE status='pending' AND bm25_score=0 AND created_at < ?`,
		cutoff,
	)
	if err != nil {
		return fmt.Errorf("scorer reconcile: query: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		select {
		case ch <- id:
		default:
			// Still full — drop silently.
		}
	}
	return rows.Err()
}
