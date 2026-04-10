package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/repo"
)

// ─────────────────────────────────────────────────────────────────────────────
// InferenceConfig
// ─────────────────────────────────────────────────────────────────────────────

// InferenceConfig holds configuration for the background inference runner.
type InferenceConfig struct {
	// ProjectMinFraction is the minimum average Jaccard similarity score
	// against a project's bursts required to infer that project. Default: 0.60.
	ProjectMinFraction float64
	// ProjectMinCandidates is the minimum number of matching project bursts
	// required before project inference is recorded. Default: 3.
	ProjectMinCandidates int
	// MaxRerunsPerDay caps how many inference records are created for a note
	// per UTC day (across all statuses). Default: 5.
	MaxRerunsPerDay int
}

// DefaultInferenceConfig returns the spec-recommended inference defaults.
func DefaultInferenceConfig() InferenceConfig {
	return InferenceConfig{
		ProjectMinFraction:   0.60,
		ProjectMinCandidates: 3,
		MaxRerunsPerDay:      5,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// projectEvidenceEntry — internal JSON type for project inference evidence
// ─────────────────────────────────────────────────────────────────────────────

type projectEvidenceEntry struct {
	ProjectURN string  `json:"project_urn"`
	MatchCount int     `json:"match_count"`
	Score      float64 `json:"score"`
}

// ─────────────────────────────────────────────────────────────────────────────
// StartInferenceRunner
// ─────────────────────────────────────────────────────────────────────────────

// StartInferenceRunner starts the background inference goroutine and returns
// the channel to which note URNs should be sent (non-blocking send).
// The goroutine runs until ctx is cancelled.
//
// When a note URN arrives on the channel, the runner checks whether the note
// lacks a title or project_urn, applies thresholds, and stores an inference
// record if enough signal exists.
func StartInferenceRunner(
	ctx context.Context,
	db *sql.DB,
	writeFn func(writeOp) error,
	burstCfg core.BurstConfig,
	cfg InferenceConfig,
) chan<- string {
	ch := make(chan string, 256)
	go runInferenceRunner(ctx, db, writeFn, burstCfg, cfg, ch)
	return ch
}

func runInferenceRunner(
	ctx context.Context,
	db *sql.DB,
	writeFn func(writeOp) error,
	burstCfg core.BurstConfig,
	cfg InferenceConfig,
	ch <-chan string,
) {
	// Deduplicate in-flight URNs: if the same note is queued again while its
	// inference is running, the second send is a no-op (channel buffering handles
	// the rest). This map prevents redundant concurrent runs for one note.
	inFlight := make(map[string]struct{})
	for {
		select {
		case <-ctx.Done():
			return
		case noteURN, ok := <-ch:
			if !ok {
				return
			}
			if _, seen := inFlight[noteURN]; seen {
				continue
			}
			inFlight[noteURN] = struct{}{}
			if err := runInferenceForNote(ctx, db, writeFn, burstCfg, cfg, noteURN); err != nil {
				fmt.Printf("inference: WARN: note=%s: %v\n", noteURN, err)
			}
			delete(inFlight, noteURN)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// runInferenceForNote — per-note inference logic
// ─────────────────────────────────────────────────────────────────────────────

func runInferenceForNote(
	ctx context.Context,
	db *sql.DB,
	writeFn func(writeOp) error,
	burstCfg core.BurstConfig,
	cfg InferenceConfig,
	noteURN string,
) error {
	// 1. Read the note's current title and project_urn.
	var title, projectURN string
	err := db.QueryRowContext(ctx,
		`SELECT title, project_urn FROM notes WHERE urn=?`, noteURN,
	).Scan(&title, &projectURN)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // note doesn't exist or was deleted
	}
	if err != nil {
		return fmt.Errorf("read note: %w", err)
	}

	needsTitle := title == ""
	needsProject := projectURN == ""
	if !needsTitle && !needsProject {
		return nil // nothing to infer
	}

	// 2. Check the daily re-run cap.
	startOfDayMs := toMs(time.Now().UTC().Truncate(24 * time.Hour))
	var rerunCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM note_context_inferences WHERE note_urn=? AND created_at>=?`,
		noteURN, startOfDayMs,
	).Scan(&rerunCount); err != nil {
		return fmt.Errorf("rerun count: %w", err)
	}
	if rerunCount >= cfg.MaxRerunsPerDay {
		return nil
	}

	// 3. Skip if a pending inference already exists (wait for it to be reviewed).
	var pendingCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM note_context_inferences WHERE note_urn=? AND status='pending'`,
		noteURN,
	).Scan(&pendingCount); err != nil {
		return fmt.Errorf("pending count: %w", err)
	}
	if pendingCount > 0 {
		return nil
	}

	// 4. Check if content is too similar to a previously rejected inference.
	if skip, err := shouldSkipDueToRejection(ctx, db, noteURN); err != nil {
		return fmt.Errorf("rejection check: %w", err)
	} else if skip {
		return nil
	}

	// 5. Title inference: read note content and derive a candidate title.
	var inferredTitle string
	var titleConfidence float64
	var titleBasisBurstID string
	if needsTitle {
		var content string
		_ = db.QueryRowContext(ctx,
			`SELECT content FROM note_content WHERE urn=?`, noteURN,
		).Scan(&content)

		lines := strings.Split(content, "\n")
		inferredTitle, titleConfidence, _ = core.InferTitle(lines)

		// Record the first burst ID as the basis for auditing.
		_ = db.QueryRowContext(ctx,
			`SELECT id FROM context_bursts WHERE note_urn=? ORDER BY created_at ASC LIMIT 1`,
			noteURN,
		).Scan(&titleBasisBurstID)
	}

	// 6. Project inference: score note bursts against project bursts via Jaccard.
	var inferredProjectURN string
	var projectConfidence float64
	var projectEvidenceJSON string
	if needsProject {
		inferredProjectURN, projectConfidence, projectEvidenceJSON =
			inferProjectFromDB(ctx, db, noteURN, burstCfg, cfg)
	}

	// 7. Nothing useful to store.
	if inferredTitle == "" && inferredProjectURN == "" {
		return nil
	}

	// 8. Generate ID and persist the inference record.
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("generate id: %w", err)
	}
	now := toMs(time.Now().UTC())

	return writeFn(func(db *sql.DB) error {
		_, err := db.Exec(
			`INSERT INTO note_context_inferences
			   (id, note_urn, inferred_title, inferred_project_urn,
			    title_confidence, project_confidence, project_evidence,
			    title_basis_burst_id, status, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?)`,
			id.String(), noteURN,
			inferredTitle, inferredProjectURN,
			titleConfidence, projectConfidence,
			projectEvidenceJSON, titleBasisBurstID,
			now,
		)
		return err
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// shouldSkipDueToRejection
// ─────────────────────────────────────────────────────────────────────────────

// shouldSkipDueToRejection returns true if the note has a rejected inference
// whose stored token hash matches the note's current burst token hash, meaning
// the content hasn't changed substantially since the last rejection.
func shouldSkipDueToRejection(ctx context.Context, db *sql.DB, noteURN string) (bool, error) {
	var rejectedTokenHash string
	err := db.QueryRowContext(ctx,
		`SELECT rejected_token_hash FROM note_context_inferences
		 WHERE note_urn=? AND status='rejected'
		 ORDER BY created_at DESC LIMIT 1`,
		noteURN,
	).Scan(&rejectedTokenHash)
	if errors.Is(err, sql.ErrNoRows) || rejectedTokenHash == "" {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	// Compute the current burst token hash.
	var tokensStr string
	_ = db.QueryRowContext(ctx,
		`SELECT tokens FROM context_bursts WHERE note_urn=? ORDER BY created_at DESC LIMIT 1`,
		noteURN,
	).Scan(&tokensStr)
	if tokensStr == "" {
		return false, nil
	}

	currentHash := inferenceTokenHash(strings.Fields(tokensStr))
	return currentHash == rejectedTokenHash, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// inferProjectFromDB
// ─────────────────────────────────────────────────────────────────────────────

// inferProjectFromDB runs the project suggestion logic directly against the DB.
// It replicates the SuggestProjectForNote query logic without needing the
// full Provider, so it can run inside the background inference goroutine.
func inferProjectFromDB(
	ctx context.Context,
	db *sql.DB,
	noteURN string,
	burstCfg core.BurstConfig,
	cfg InferenceConfig,
) (projectURN string, confidence float64, evidenceJSON string) {
	// Load this note's burst token sets.
	noteRows, err := db.QueryContext(ctx,
		`SELECT tokens FROM context_bursts WHERE note_urn=?`, noteURN,
	)
	if err != nil {
		return "", 0, ""
	}
	var noteTokenSets [][]string
	for noteRows.Next() {
		var tokStr string
		if noteRows.Scan(&tokStr) == nil {
			if toks := strings.Fields(tokStr); len(toks) >= 3 {
				noteTokenSets = append(noteTokenSets, toks)
			}
		}
	}
	noteRows.Close()
	if len(noteTokenSets) == 0 {
		return "", 0, ""
	}

	// Load recent bursts from notes that have a project assigned.
	cutoffMs := toMs(time.Now().UTC().Add(-time.Duration(burstCfg.CandidateLookbackDays) * 24 * time.Hour))
	projRows, err := db.QueryContext(ctx,
		`SELECT project_urn, tokens
		 FROM context_bursts
		 WHERE project_urn != '' AND note_urn != ? AND created_at >= ?
		 ORDER BY created_at DESC LIMIT ?`,
		noteURN, cutoffMs, burstCfg.CandidateLookbackN,
	)
	if err != nil {
		return "", 0, ""
	}
	var hints []core.ProjectBurstHint
	for projRows.Next() {
		var pURN, tokStr string
		if projRows.Scan(&pURN, &tokStr) == nil {
			if toks := strings.Fields(tokStr); len(toks) >= 3 {
				hints = append(hints, core.ProjectBurstHint{ProjectURN: pURN, Tokens: toks})
			}
		}
	}
	projRows.Close()
	if len(hints) == 0 {
		return "", 0, ""
	}

	// Score using the core function (pure in-memory Jaccard).
	suggestions := core.SuggestProjectForNote(noteTokenSets, hints, burstCfg.OverlapThreshold)
	if len(suggestions) == 0 {
		return "", 0, ""
	}

	// Build evidence JSON for all suggestions (for auditing).
	evidenceJSON = buildInferenceEvidence(suggestions)

	// Apply thresholds.
	top := suggestions[0]
	if top.Score < cfg.ProjectMinFraction || top.MatchCount < cfg.ProjectMinCandidates {
		return "", 0, evidenceJSON
	}

	// confidence = min(score, matchCount/5)
	conf := top.Score
	if mc := float64(top.MatchCount) / 5.0; mc < conf {
		conf = mc
	}
	if conf > 1.0 {
		conf = 1.0
	}
	return top.ProjectURN, conf, evidenceJSON
}

// buildInferenceEvidence serializes project suggestions into a JSON evidence array.
func buildInferenceEvidence(suggestions []core.ProjectSuggestion) string {
	entries := make([]projectEvidenceEntry, 0, len(suggestions))
	for _, s := range suggestions {
		if s.ProjectURN == "" {
			continue
		}
		entries = append(entries, projectEvidenceEntry{
			ProjectURN: s.ProjectURN,
			MatchCount: s.MatchCount,
			Score:      s.Score,
		})
	}
	if len(entries) == 0 {
		return ""
	}
	b, _ := json.Marshal(entries)
	return string(b)
}

// inferenceTokenHash returns a hex-encoded SHA-256 hash of a token set.
// Used for rejection re-enable gating: if the hash changes, the content
// has changed substantially since the last rejection.
func inferenceTokenHash(tokens []string) string {
	joined := strings.Join(tokens, " ")
	sum := sha256.Sum256([]byte(joined))
	return fmt.Sprintf("%x", sum[:])
}

// ─────────────────────────────────────────────────────────────────────────────
// Scan helper
// ─────────────────────────────────────────────────────────────────────────────

const inferenceColumns = `id, note_urn, inferred_title, inferred_project_urn,
	title_confidence, project_confidence, project_evidence,
	title_basis_burst_id, status, created_at, reviewed_at, reviewed_by, rejected_token_hash`

func scanInference(rows interface{ Scan(dest ...any) error }) (repo.InferenceRecord, error) {
	var inf repo.InferenceRecord
	var createdAtMs int64
	var reviewedAtMs *int64
	if err := rows.Scan(
		&inf.ID, &inf.NoteURN, &inf.InferredTitle, &inf.InferredProjectURN,
		&inf.TitleConfidence, &inf.ProjectConfidence, &inf.ProjectEvidence,
		&inf.TitleBasisBurstID, &inf.Status, &createdAtMs,
		&reviewedAtMs, &inf.ReviewedBy, &inf.RejectedTokenHash,
	); err != nil {
		return repo.InferenceRecord{}, err
	}
	inf.CreatedAt = fromMs(createdAtMs)
	if reviewedAtMs != nil {
		t := fromMs(*reviewedAtMs)
		inf.ReviewedAt = &t
	}
	return inf, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// GetInference
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) GetInference(ctx context.Context, id string) (repo.InferenceRecord, error) {
	if err := ctx.Err(); err != nil {
		return repo.InferenceRecord{}, err
	}
	row := p.db.QueryRowContext(ctx,
		`SELECT `+inferenceColumns+` FROM note_context_inferences WHERE id=?`, id,
	)
	inf, err := scanInference(row)
	if errors.Is(err, sql.ErrNoRows) {
		return repo.InferenceRecord{}, repo.ErrNotFound
	}
	return inf, err
}

// ─────────────────────────────────────────────────────────────────────────────
// GetNoteInference
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) GetNoteInference(ctx context.Context, noteURN string) (repo.InferenceRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return repo.InferenceRecord{}, false, err
	}
	row := p.db.QueryRowContext(ctx,
		`SELECT `+inferenceColumns+` FROM note_context_inferences
		 WHERE note_urn=? AND status='pending' LIMIT 1`,
		noteURN,
	)
	inf, err := scanInference(row)
	if errors.Is(err, sql.ErrNoRows) {
		return repo.InferenceRecord{}, false, nil
	}
	if err != nil {
		return repo.InferenceRecord{}, false, err
	}
	return inf, true, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// ListInferences
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) ListInferences(ctx context.Context, opts repo.InferenceListOptions) ([]repo.InferenceRecord, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}

	pageSize := resolvePageSize(opts.PageSize)
	offset := 0
	if opts.PageToken != "" {
		if n, err := decodeOffsetToken(opts.PageToken); err == nil {
			offset = n
		}
	}

	var args []any
	q := `SELECT ` + inferenceColumns + ` FROM note_context_inferences WHERE 1=1`
	if opts.NoteURN != "" {
		q += ` AND note_urn=?`
		args = append(args, opts.NoteURN)
	}
	if opts.Status != "" {
		q += ` AND status=?`
		args = append(args, opts.Status)
	}
	q += ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	args = append(args, pageSize+1, offset)

	rows, err := p.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("sqlite: list inferences: %w", err)
	}
	defer rows.Close()

	var results []repo.InferenceRecord
	for rows.Next() {
		inf, err := scanInference(rows)
		if err != nil {
			return nil, "", fmt.Errorf("sqlite: scan inference: %w", err)
		}
		results = append(results, inf)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var nextToken string
	if len(results) > pageSize {
		results = results[:pageSize]
		nextToken = encodeOffsetToken(offset + pageSize)
	}
	return results, nextToken, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// AcceptInference
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) AcceptInference(ctx context.Context, id string, opts repo.AcceptInferenceOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !opts.AcceptTitle && !opts.AcceptProject {
		return fmt.Errorf("sqlite: accept inference: at least one of AcceptTitle or AcceptProject must be true")
	}
	return p.write(func(db *sql.DB) error {
		// Load the inference.
		var noteURN, inferredTitle, inferredProjectURN, infStatus string
		err := db.QueryRow(
			`SELECT note_urn, inferred_title, inferred_project_urn, status
			 FROM note_context_inferences WHERE id=?`, id,
		).Scan(&noteURN, &inferredTitle, &inferredProjectURN, &infStatus)
		if errors.Is(err, sql.ErrNoRows) {
			return repo.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("read inference: %w", err)
		}
		if infStatus != "pending" {
			return fmt.Errorf("%w: inference %s is not pending (status=%s)", repo.ErrNotFound, id, infStatus)
		}

		now := toMs(time.Now().UTC())

		// Apply accepted fields to the notes table.
		if opts.AcceptTitle && inferredTitle != "" {
			if _, err := db.Exec(
				`UPDATE notes SET title=?, updated_at=? WHERE urn=?`,
				inferredTitle, now, noteURN,
			); err != nil {
				return fmt.Errorf("update note title: %w", err)
			}
			// Best-effort FTS title update.
			_, _ = db.Exec(
				`UPDATE notes_fts SET title=? WHERE urn=?`, inferredTitle, noteURN,
			)
		}
		if opts.AcceptProject && inferredProjectURN != "" {
			if _, err := db.Exec(
				`UPDATE notes SET project_urn=?, updated_at=? WHERE urn=?`,
				inferredProjectURN, now, noteURN,
			); err != nil {
				return fmt.Errorf("update note project_urn: %w", err)
			}
			// Also update context_bursts so future candidate detection uses the new project.
			_, _ = db.Exec(
				`UPDATE context_bursts SET project_urn=? WHERE note_urn=? AND project_urn=''`,
				inferredProjectURN, noteURN,
			)
		}

		// Mark the inference as accepted.
		_, err = db.Exec(
			`UPDATE note_context_inferences
			 SET status='accepted', reviewed_at=?, reviewed_by=?
			 WHERE id=?`,
			now, opts.ReviewerURN, id,
		)
		return err
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// RejectInference
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) RejectInference(ctx context.Context, id, reviewerURN string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.write(func(db *sql.DB) error {
		// Load the inference.
		var noteURN, infStatus string
		err := db.QueryRow(
			`SELECT note_urn, status FROM note_context_inferences WHERE id=?`, id,
		).Scan(&noteURN, &infStatus)
		if errors.Is(err, sql.ErrNoRows) {
			return repo.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("read inference: %w", err)
		}
		if infStatus != "pending" {
			return fmt.Errorf("%w: inference %s is not pending (status=%s)", repo.ErrNotFound, id, infStatus)
		}

		// Compute rejection token hash from the note's most recent burst.
		var tokensStr string
		_ = db.QueryRow(
			`SELECT tokens FROM context_bursts WHERE note_urn=? ORDER BY created_at DESC LIMIT 1`,
			noteURN,
		).Scan(&tokensStr)
		hash := ""
		if tokensStr != "" {
			hash = inferenceTokenHash(strings.Fields(tokensStr))
		}

		now := toMs(time.Now().UTC())
		_, err = db.Exec(
			`UPDATE note_context_inferences
			 SET status='rejected', reviewed_at=?, reviewed_by=?, rejected_token_hash=?
			 WHERE id=?`,
			now, reviewerURN, hash, id,
		)
		return err
	})
}
