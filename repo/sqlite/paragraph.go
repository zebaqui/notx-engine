package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/repo"
)

// ─────────────────────────────────────────────────────────────────────────────
// Offset cursor helpers (paragraph-specific, offset-based)
// ─────────────────────────────────────────────────────────────────────────────

func encodeOffsetCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func decodeOffsetCursor(token string) (int, error) {
	if token == "" {
		return 0, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, fmt.Errorf("invalid page token: %w", err)
	}
	n, err := strconv.Atoi(string(b))
	if err != nil {
		return 0, fmt.Errorf("invalid page token offset: %w", err)
	}
	return n, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// JSON helpers
// ─────────────────────────────────────────────────────────────────────────────

func marshalStringSlice(s []string) string {
	if len(s) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(s)
	return string(b)
}

func unmarshalStringSlice(s string) []string {
	var out []string
	if s == "" || s == "[]" {
		return out
	}
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// Paragraphs
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) UpsertParagraphs(ctx context.Context, paragraphs []repo.ParagraphRecord) error {
	if len(paragraphs) == 0 {
		return nil
	}
	return p.write(func(db *sql.DB) error {
		const q = `INSERT INTO note_paragraphs
			(id, note_urn, project_urn, folder_urn, sequence, position,
			 line_start, line_end, text, role,
			 main_concepts, supporting_concepts, concept_families,
			 created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET
			  sequence=excluded.sequence,
			  position=excluded.position,
			  line_start=excluded.line_start,
			  line_end=excluded.line_end,
			  text=excluded.text,
			  role=excluded.role,
			  main_concepts=excluded.main_concepts,
			  supporting_concepts=excluded.supporting_concepts,
			  concept_families=excluded.concept_families,
			  updated_at=excluded.updated_at`
		now := toMs(time.Now().UTC())
		for _, pg := range paragraphs {
			createdMs := toMs(pg.CreatedAt)
			if pg.CreatedAt.IsZero() {
				createdMs = now
			}
			if _, err := db.ExecContext(ctx, q,
				pg.ID, pg.NoteURN, pg.ProjectURN, pg.FolderURN,
				pg.Sequence, pg.Position, pg.LineStart, pg.LineEnd,
				pg.Text, pg.Role,
				marshalStringSlice(pg.MainConcepts),
				marshalStringSlice(pg.SupportingConcepts),
				marshalStringSlice(pg.ConceptFamilies),
				createdMs, now,
			); err != nil {
				return fmt.Errorf("UpsertParagraphs: %w", err)
			}
		}
		return nil
	})
}

func scanParagraph(rows *sql.Rows) (repo.ParagraphRecord, error) {
	var pg repo.ParagraphRecord
	var mainC, suppC, famC string
	var createdMs, updatedMs int64
	err := rows.Scan(
		&pg.ID, &pg.NoteURN, &pg.ProjectURN, &pg.FolderURN,
		&pg.Sequence, &pg.Position, &pg.LineStart, &pg.LineEnd,
		&pg.Text, &pg.Role,
		&mainC, &suppC, &famC,
		&createdMs, &updatedMs,
	)
	if err != nil {
		return repo.ParagraphRecord{}, err
	}
	pg.MainConcepts = unmarshalStringSlice(mainC)
	pg.SupportingConcepts = unmarshalStringSlice(suppC)
	pg.ConceptFamilies = unmarshalStringSlice(famC)
	pg.CreatedAt = fromMs(createdMs)
	pg.UpdatedAt = fromMs(updatedMs)
	return pg, nil
}

const paragraphCols = `id, note_urn, project_urn, folder_urn,
	sequence, position, line_start, line_end, text, role,
	main_concepts, supporting_concepts, concept_families,
	created_at, updated_at`

func (p *Provider) ListParagraphs(ctx context.Context, opts repo.ParagraphListOptions) ([]repo.ParagraphRecord, string, error) {
	pageSize := resolvePageSize(opts.PageSize)
	offset := 0
	if opts.PageToken != "" {
		var err error
		offset, err = decodeOffsetCursor(opts.PageToken)
		if err != nil {
			return nil, "", fmt.Errorf("ListParagraphs: bad page token: %w", err)
		}
	}

	var conditions []string
	var args []any
	if opts.NoteURN != "" {
		conditions = append(conditions, "note_urn = ?")
		args = append(args, opts.NoteURN)
	}
	if opts.FolderURN != "" {
		conditions = append(conditions, "folder_urn = ?")
		args = append(args, opts.FolderURN)
	}
	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	q := fmt.Sprintf(`SELECT %s FROM note_paragraphs %s ORDER BY note_urn, position ASC LIMIT ? OFFSET ?`, paragraphCols, where)
	args = append(args, pageSize+1, offset)

	rows, err := p.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("ListParagraphs: query: %w", err)
	}
	defer rows.Close()

	var out []repo.ParagraphRecord
	for rows.Next() {
		pg, err := scanParagraph(rows)
		if err != nil {
			return nil, "", fmt.Errorf("ListParagraphs: scan: %w", err)
		}
		out = append(out, pg)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("ListParagraphs: rows: %w", err)
	}

	var nextToken string
	if len(out) > pageSize {
		out = out[:pageSize]
		nextToken = encodeOffsetCursor(offset + pageSize)
	}
	return out, nextToken, nil
}

func (p *Provider) GetParagraph(ctx context.Context, id string) (repo.ParagraphRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM note_paragraphs WHERE id = ?`, paragraphCols)
	rows, err := p.db.QueryContext(ctx, q, id)
	if err != nil {
		return repo.ParagraphRecord{}, fmt.Errorf("GetParagraph: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return repo.ParagraphRecord{}, repo.ErrNotFound
	}
	pg, err := scanParagraph(rows)
	if err != nil {
		return repo.ParagraphRecord{}, fmt.Errorf("GetParagraph: scan: %w", err)
	}
	return pg, nil
}

func (p *Provider) DeleteParagraphsForNote(ctx context.Context, noteURN string) error {
	return p.write(func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, `DELETE FROM note_paragraphs WHERE note_urn = ?`, noteURN)
		return err
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Relations
// ─────────────────────────────────────────────────────────────────────────────

const relationCols = `id, source_paragraph_id, target_paragraph_id,
	note_urn_source, note_urn_target,
	project_urn_source, project_urn_target,
	folder_urn_source, folder_urn_target,
	proximity_tier, relation_type, score,
	reason_signals, pattern_hash, version,
	feedback_vote, feedback_at, created_at`

func scanRelation(rows *sql.Rows) (repo.ParagraphRelationRecord, error) {
	var r repo.ParagraphRelationRecord
	var signals string
	var feedbackVote sql.NullString
	var feedbackAt sql.NullInt64
	var createdMs int64
	err := rows.Scan(
		&r.ID, &r.SourceParagraphID, &r.TargetParagraphID,
		&r.NoteURNSource, &r.NoteURNTarget,
		&r.ProjectURNSource, &r.ProjectURNTarget,
		&r.FolderURNSource, &r.FolderURNTarget,
		&r.ProximityTier, &r.RelationType, &r.Score,
		&signals, &r.PatternHash, &r.Version,
		&feedbackVote, &feedbackAt, &createdMs,
	)
	if err != nil {
		return repo.ParagraphRelationRecord{}, err
	}
	r.ReasonSignals = unmarshalStringSlice(signals)
	if feedbackVote.Valid {
		r.FeedbackVote = &feedbackVote.String
	}
	if feedbackAt.Valid {
		t := fromMs(feedbackAt.Int64)
		r.FeedbackAt = &t
	}
	r.CreatedAt = fromMs(createdMs)
	return r, nil
}

func (p *Provider) UpsertRelations(ctx context.Context, relations []repo.ParagraphRelationRecord) error {
	if len(relations) == 0 {
		return nil
	}
	return p.write(func(db *sql.DB) error {
		const q = `INSERT INTO paragraph_relations
			(id, source_paragraph_id, target_paragraph_id,
			 note_urn_source, note_urn_target,
			 project_urn_source, project_urn_target,
			 folder_urn_source, folder_urn_target,
			 proximity_tier, relation_type, score,
			 reason_signals, pattern_hash, version,
			 feedback_vote, feedback_at, created_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(source_paragraph_id, target_paragraph_id, relation_type) DO UPDATE SET
			  score=excluded.score,
			  reason_signals=excluded.reason_signals,
			  pattern_hash=excluded.pattern_hash,
			  version=excluded.version,
			  proximity_tier=excluded.proximity_tier`
		now := toMs(time.Now().UTC())
		for _, r := range relations {
			if r.Version == "" {
				r.Version = "heuristic_v1"
			}
			if _, err := db.ExecContext(ctx, q,
				r.ID, r.SourceParagraphID, r.TargetParagraphID,
				r.NoteURNSource, r.NoteURNTarget,
				r.ProjectURNSource, r.ProjectURNTarget,
				r.FolderURNSource, r.FolderURNTarget,
				r.ProximityTier, r.RelationType, r.Score,
				marshalStringSlice(r.ReasonSignals),
				r.PatternHash, r.Version,
				nil, nil, now,
			); err != nil {
				return fmt.Errorf("UpsertRelations: %w", err)
			}
		}
		return nil
	})
}

func (p *Provider) ListRelations(ctx context.Context, opts repo.ParagraphRelationListOptions) ([]repo.ParagraphRelationRecord, string, error) {
	pageSize := resolvePageSize(opts.PageSize)
	offset := 0
	if opts.PageToken != "" {
		var err error
		offset, err = decodeOffsetCursor(opts.PageToken)
		if err != nil {
			return nil, "", fmt.Errorf("ListRelations: bad page token: %w", err)
		}
	}

	var conditions []string
	var args []any
	if opts.NoteURN != "" {
		conditions = append(conditions, "note_urn_source = ?")
		args = append(args, opts.NoteURN)
	}
	if opts.FolderURN != "" {
		conditions = append(conditions, "folder_urn_source = ?")
		args = append(args, opts.FolderURN)
	}
	if opts.SourceParagraphID != "" {
		conditions = append(conditions, "source_paragraph_id = ?")
		args = append(args, opts.SourceParagraphID)
	}
	if opts.MinScore > 0 {
		conditions = append(conditions, "score >= ?")
		args = append(args, opts.MinScore)
	}
	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	q := fmt.Sprintf(`SELECT %s FROM paragraph_relations %s ORDER BY score DESC LIMIT ? OFFSET ?`, relationCols, where)
	args = append(args, pageSize+1, offset)

	rows, err := p.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("ListRelations: query: %w", err)
	}
	defer rows.Close()

	var out []repo.ParagraphRelationRecord
	for rows.Next() {
		r, err := scanRelation(rows)
		if err != nil {
			return nil, "", fmt.Errorf("ListRelations: scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("ListRelations: rows: %w", err)
	}

	var nextToken string
	if len(out) > pageSize {
		out = out[:pageSize]
		nextToken = encodeOffsetCursor(offset + pageSize)
	}
	return out, nextToken, nil
}

func (p *Provider) GetRelation(ctx context.Context, id string) (repo.ParagraphRelationRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM paragraph_relations WHERE id = ?`, relationCols)
	rows, err := p.db.QueryContext(ctx, q, id)
	if err != nil {
		return repo.ParagraphRelationRecord{}, fmt.Errorf("GetRelation: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return repo.ParagraphRelationRecord{}, repo.ErrNotFound
	}
	r, err := scanRelation(rows)
	if err != nil {
		return repo.ParagraphRelationRecord{}, fmt.Errorf("GetRelation: scan: %w", err)
	}
	return r, nil
}

func (p *Provider) RecordFeedback(ctx context.Context, relationID, vote string) (repo.ParagraphRelationRecord, error) {
	now := toMs(time.Now().UTC())
	err := p.write(func(db *sql.DB) error {
		_, err := db.ExecContext(ctx,
			`UPDATE paragraph_relations SET feedback_vote=?, feedback_at=? WHERE id=?`,
			vote, now, relationID,
		)
		return err
	})
	if err != nil {
		return repo.ParagraphRelationRecord{}, fmt.Errorf("RecordFeedback: %w", err)
	}
	return p.GetRelation(ctx, relationID)
}

// ─────────────────────────────────────────────────────────────────────────────
// Pattern scores
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) GetPatternScore(ctx context.Context, patternHash string) (repo.PatternScoreRecord, error) {
	const q = `SELECT pattern_hash, role_a, role_b, relation_type, proximity_tier,
		cue_present, up_count, down_count, net_score, updated_at
		FROM paragraph_pattern_scores WHERE pattern_hash = ?`
	var ps repo.PatternScoreRecord
	var cueInt int
	var updatedMs int64
	err := p.db.QueryRowContext(ctx, q, patternHash).Scan(
		&ps.PatternHash, &ps.RoleA, &ps.RoleB, &ps.RelationType, &ps.ProximityTier,
		&cueInt, &ps.UpCount, &ps.DownCount, &ps.NetScore, &updatedMs,
	)
	if err == sql.ErrNoRows {
		return repo.PatternScoreRecord{}, repo.ErrNotFound
	}
	if err != nil {
		return repo.PatternScoreRecord{}, fmt.Errorf("GetPatternScore: %w", err)
	}
	ps.CuePresent = cueInt == 1
	ps.UpdatedAt = fromMs(updatedMs)
	return ps, nil
}

func (p *Provider) UpsertPatternScore(ctx context.Context, ps repo.PatternScoreRecord) error {
	return p.write(func(db *sql.DB) error {
		cueInt := 0
		if ps.CuePresent {
			cueInt = 1
		}
		const q = `INSERT INTO paragraph_pattern_scores
			(pattern_hash, role_a, role_b, relation_type, proximity_tier,
			 cue_present, up_count, down_count, net_score, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(pattern_hash) DO UPDATE SET
			  up_count=excluded.up_count,
			  down_count=excluded.down_count,
			  net_score=excluded.net_score,
			  updated_at=excluded.updated_at`
		_, err := db.ExecContext(ctx, q,
			ps.PatternHash, ps.RoleA, ps.RoleB, ps.RelationType, ps.ProximityTier,
			cueInt, ps.UpCount, ps.DownCount, ps.NetScore,
			toMs(time.Now().UTC()),
		)
		return err
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Weights
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) GetWeights(ctx context.Context) (repo.ParagraphWeights, error) {
	const q = `SELECT w_proximity_tier, w_role_pair, w_overlap, w_cue, w_pattern,
		tier_same_doc, tier_same_folder, tier_same_project, tier_global, updated_at
		FROM paragraph_weights WHERE id = 1`
	var w repo.ParagraphWeights
	var updatedMs int64
	err := p.db.QueryRowContext(ctx, q).Scan(
		&w.WProximityTier, &w.WRolePair, &w.WOverlap, &w.WCue, &w.WPattern,
		&w.TierSameDoc, &w.TierSameFolder, &w.TierSameProject, &w.TierGlobal,
		&updatedMs,
	)
	if err == sql.ErrNoRows {
		// Return defaults when no row exists yet.
		dw := core.DefaultWeights()
		return repo.ParagraphWeights{
			WProximityTier:  dw.WProximityTier,
			WRolePair:       dw.WRolePair,
			WOverlap:        dw.WOverlap,
			WCue:            dw.WCue,
			WPattern:        dw.WPattern,
			TierSameDoc:     dw.TierSameDoc,
			TierSameFolder:  dw.TierSameFolder,
			TierSameProject: dw.TierSameProject,
			TierGlobal:      dw.TierGlobal,
			UpdatedAt:       time.Now().UTC(),
		}, nil
	}
	if err != nil {
		return repo.ParagraphWeights{}, fmt.Errorf("GetWeights: %w", err)
	}
	w.UpdatedAt = fromMs(updatedMs)
	return w, nil
}

func (p *Provider) UpsertWeights(ctx context.Context, w repo.ParagraphWeights) error {
	return p.write(func(db *sql.DB) error {
		const q = `INSERT INTO paragraph_weights
			(id, w_proximity_tier, w_role_pair, w_overlap, w_cue, w_pattern,
			 tier_same_doc, tier_same_folder, tier_same_project, tier_global, updated_at)
			VALUES (1,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET
			  w_proximity_tier=excluded.w_proximity_tier,
			  w_role_pair=excluded.w_role_pair,
			  w_overlap=excluded.w_overlap,
			  w_cue=excluded.w_cue,
			  w_pattern=excluded.w_pattern,
			  tier_same_doc=excluded.tier_same_doc,
			  tier_same_folder=excluded.tier_same_folder,
			  tier_same_project=excluded.tier_same_project,
			  tier_global=excluded.tier_global,
			  updated_at=excluded.updated_at`
		_, err := db.ExecContext(ctx, q,
			w.WProximityTier, w.WRolePair, w.WOverlap, w.WCue, w.WPattern,
			w.TierSameDoc, w.TierSameFolder, w.TierSameProject, w.TierGlobal,
			toMs(time.Now().UTC()),
		)
		return err
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Processing queue
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) ListNotesNeedingParagraphProcessing(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := p.db.QueryContext(ctx,
		`SELECT urn FROM notes
		 WHERE deleted = 0 AND paragraph_head_seq < head_seq
		 ORDER BY updated_at ASC
		 LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("ListNotesNeedingParagraphProcessing: %w", err)
	}
	defer rows.Close()
	var urns []string
	for rows.Next() {
		var urn string
		if err := rows.Scan(&urn); err != nil {
			return nil, err
		}
		urns = append(urns, urn)
	}
	return urns, rows.Err()
}

func (p *Provider) MarkNoteProcessed(ctx context.Context, noteURN string, sequence int) error {
	return p.write(func(db *sql.DB) error {
		_, err := db.ExecContext(ctx,
			`UPDATE notes SET paragraph_head_seq = ? WHERE urn = ?`,
			sequence, noteURN,
		)
		return err
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// ResetGraph
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) ResetGraph(ctx context.Context) error {
	return p.write(func(db *sql.DB) error {
		if _, err := db.ExecContext(ctx, `DELETE FROM paragraph_relations`); err != nil {
			return fmt.Errorf("ResetGraph: delete relations: %w", err)
		}
		if _, err := db.ExecContext(ctx, `DELETE FROM note_paragraphs`); err != nil {
			return fmt.Errorf("ResetGraph: delete paragraphs: %w", err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE notes SET paragraph_head_seq = -1`); err != nil {
			return fmt.Errorf("ResetGraph: reset queue: %w", err)
		}
		// paragraph_weights and paragraph_pattern_scores are intentionally preserved.
		return nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Background Paragraph Runner
// ─────────────────────────────────────────────────────────────────────────────

// ParagraphRunnerConfig controls the background paragraph processing runner.
type ParagraphRunnerConfig struct {
	// PollInterval is how often the runner checks for unprocessed notes.
	PollInterval time.Duration // default: 30s
	// SameDocWindowSize is the look-ahead/behind within the same note.
	SameDocWindowSize int // default: 3
	// CrossDocEnabled enables cross-note scoring (Phase 2 — disabled for SQLite MVP).
	CrossDocEnabled bool
	// TopN is the maximum number of relations to keep per source paragraph.
	TopN int // default: 3
	// MinScore is the minimum score threshold for same-doc relations.
	MinScore float64 // default: 0.55
	// CrossDocMinScore is the minimum score threshold for cross-doc relations.
	// Lower than MinScore because cross-doc tier multipliers reduce max achievable scores.
	CrossDocMinScore float64 // default: 0.20
	// MinNoteOverlap is the minimum Jaccard similarity between two notes' concept
	// sets required before paragraph-level cross-doc scoring is attempted.
	// Prevents unrelated notes (e.g. French vocabulary vs. penguin biology)
	// from generating spurious cross-doc relations.
	MinNoteOverlap float64 // default: 0.05
}

// DefaultParagraphRunnerConfig returns spec-recommended defaults.
func DefaultParagraphRunnerConfig() ParagraphRunnerConfig {
	return ParagraphRunnerConfig{
		PollInterval:      30 * time.Second,
		SameDocWindowSize: 3,
		CrossDocEnabled:   false,
		TopN:              3,
		MinScore:          0.55,
		CrossDocMinScore:  0.20,
		MinNoteOverlap:    0.05,
	}
}

// StartParagraphRunner starts the background paragraph processing goroutine.
// It returns immediately; processing runs until ctx is cancelled.
func StartParagraphRunner(
	ctx context.Context,
	db *sql.DB,
	writeFn func(writeOp) error,
	cfg ParagraphRunnerConfig,
) {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 30 * time.Second
	}
	if cfg.SameDocWindowSize == 0 {
		cfg.SameDocWindowSize = 3
	}
	if cfg.TopN == 0 {
		cfg.TopN = 3
	}
	if cfg.MinScore == 0 {
		cfg.MinScore = 0.55
	}
	if cfg.CrossDocMinScore == 0 {
		cfg.CrossDocMinScore = 0.20
	}
	if cfg.MinNoteOverlap == 0 {
		cfg.MinNoteOverlap = 0.05
	}
	go runParagraphRunner(ctx, db, writeFn, cfg)
}

func runParagraphRunner(
	ctx context.Context,
	db *sql.DB,
	writeFn func(writeOp) error,
	cfg ParagraphRunnerConfig,
) {
	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := processParagraphBatch(ctx, db, writeFn, cfg); err != nil {
				fmt.Printf("paragraph runner: WARN: %v\n", err)
			}
		}
	}
}

func processParagraphBatch(
	ctx context.Context,
	db *sql.DB,
	writeFn func(writeOp) error,
	cfg ParagraphRunnerConfig,
) error {
	// 1. Find notes needing processing.
	rows, err := db.QueryContext(ctx,
		`SELECT urn, head_seq, project_urn, folder_urn FROM notes
		 WHERE deleted = 0 AND paragraph_head_seq < head_seq
		 ORDER BY updated_at ASC LIMIT 20`,
	)
	if err != nil {
		return fmt.Errorf("processParagraphBatch: query: %w", err)
	}

	type noteRow struct {
		urn        string
		headSeq    int
		projectURN string
		folderURN  string
	}
	var notes []noteRow
	for rows.Next() {
		var n noteRow
		if err := rows.Scan(&n.urn, &n.headSeq, &n.projectURN, &n.folderURN); err != nil {
			rows.Close()
			return err
		}
		notes = append(notes, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(notes) == 0 {
		return nil
	}

	// 2. Load global weights + pattern scores once for the batch.
	weights := coreWeightsFromDB(ctx, db)
	patternScores := loadPatternScores(ctx, db)

	for _, n := range notes {
		if err := processNoteParagraphs(ctx, db, writeFn, n.urn, n.headSeq, n.projectURN, n.folderURN, weights, patternScores, cfg); err != nil {
			fmt.Printf("paragraph runner: WARN: note %s: %v\n", n.urn, err)
			// Continue to next note — never block on a single failure.
		}
	}
	return nil
}

func coreWeightsFromDB(ctx context.Context, db *sql.DB) core.ScorerWeights {
	const q = `SELECT w_proximity_tier, w_role_pair, w_overlap, w_cue, w_pattern,
		tier_same_doc, tier_same_folder, tier_same_project, tier_global
		FROM paragraph_weights WHERE id = 1`
	var w core.ScorerWeights
	err := db.QueryRowContext(ctx, q).Scan(
		&w.WProximityTier, &w.WRolePair, &w.WOverlap, &w.WCue, &w.WPattern,
		&w.TierSameDoc, &w.TierSameFolder, &w.TierSameProject, &w.TierGlobal,
	)
	if err != nil {
		return core.DefaultWeights()
	}
	return w
}

func loadPatternScores(ctx context.Context, db *sql.DB) map[string]float64 {
	rows, err := db.QueryContext(ctx, `SELECT pattern_hash, net_score FROM paragraph_pattern_scores`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make(map[string]float64)
	for rows.Next() {
		var hash string
		var score float64
		if err := rows.Scan(&hash, &score); err != nil {
			continue
		}
		out[hash] = score
	}
	return out
}

// processNoteParagraphs processes one note: splits it into paragraphs, scores
// relations within the same-doc window, then persists everything and marks the
// note as processed.
func processNoteParagraphs(
	ctx context.Context,
	db *sql.DB,
	writeFn func(writeOp) error,
	noteURN string,
	headSeq int,
	projectURN, folderURN string,
	weights core.ScorerWeights,
	patternScores map[string]float64,
	cfg ParagraphRunnerConfig,
) error {
	// Fetch note content.
	var content string
	err := db.QueryRowContext(ctx,
		`SELECT content FROM note_content WHERE urn = ?`, noteURN,
	).Scan(&content)
	if err == sql.ErrNoRows {
		// No content yet — mark processed at current seq to avoid re-queuing.
		return markProcessed(ctx, writeFn, noteURN, headSeq)
	}
	if err != nil {
		return fmt.Errorf("processNoteParagraphs: fetch content: %w", err)
	}

	// Split into paragraphs.
	rawParagraphs := core.SplitParagraphs(content)
	if len(rawParagraphs) == 0 {
		return markProcessed(ctx, writeFn, noteURN, headSeq)
	}

	// Build ParagraphRecord slice.
	now := time.Now().UTC()
	var paragraphRecords []repo.ParagraphRecord
	var annotated []core.AnnotatedParagraph

	for i, raw := range rawParagraphs {
		role := core.ClassifyRole(raw.Text)
		main, supporting, families := core.ExtractConcepts(raw.Text)
		id := uuid.New().String()
		paragraphRecords = append(paragraphRecords, repo.ParagraphRecord{
			ID:                 id,
			NoteURN:            noteURN,
			ProjectURN:         projectURN,
			FolderURN:          folderURN,
			Sequence:           headSeq,
			Position:           i,
			LineStart:          raw.LineStart,
			LineEnd:            raw.LineEnd,
			Text:               raw.Text,
			Role:               string(role),
			MainConcepts:       main,
			SupportingConcepts: supporting,
			ConceptFamilies:    families,
			CreatedAt:          now,
			UpdatedAt:          now,
		})
		annotated = append(annotated, core.AnnotatedParagraph{
			ID:           id,
			NoteURN:      noteURN,
			FolderURN:    folderURN,
			ProjectURN:   projectURN,
			Position:     i,
			Text:         raw.Text,
			Role:         role,
			MainConcepts: main,
		})
	}

	// Delete old paragraphs + relations for this note and re-insert.
	if err := deleteParagraphsAndRelationsForNote(ctx, writeFn, noteURN); err != nil {
		return err
	}

	// Upsert new paragraphs.
	if err := upsertParagraphRecords(ctx, writeFn, paragraphRecords); err != nil {
		return err
	}

	// Score relations (same-doc window).
	type candidate struct {
		rel   repo.ParagraphRelationRecord
		score float64
	}

	var relations []repo.ParagraphRelationRecord
	window := cfg.SameDocWindowSize

	for i, a := range annotated {
		var candidates []candidate

		for j, b := range annotated {
			if i == j {
				continue
			}
			dist := i - j
			if dist < 0 {
				dist = -dist
			}
			if dist > window {
				continue
			}
			scored := core.ScoreCandidate(a, b, patternScores, weights)
			if scored.Score < cfg.MinScore {
				continue
			}
			candidates = append(candidates, candidate{
				score: scored.Score,
				rel: repo.ParagraphRelationRecord{
					ID:                uuid.New().String(),
					SourceParagraphID: a.ID,
					TargetParagraphID: b.ID,
					NoteURNSource:     noteURN,
					NoteURNTarget:     noteURN,
					ProjectURNSource:  projectURN,
					ProjectURNTarget:  projectURN,
					FolderURNSource:   folderURN,
					FolderURNTarget:   folderURN,
					ProximityTier:     string(scored.ProximityTier),
					RelationType:      string(scored.RelationType),
					Score:             scored.Score,
					ReasonSignals:     scored.ReasonSignals,
					PatternHash:       scored.PatternHash,
					Version:           "heuristic_v1",
					CreatedAt:         now,
				},
			})
		}

		// Keep top N by score descending.
		sort.Slice(candidates, func(x, y int) bool {
			return candidates[x].score > candidates[y].score
		})
		topN := cfg.TopN
		if len(candidates) < topN {
			topN = len(candidates)
		}
		for _, c := range candidates[:topN] {
			relations = append(relations, c.rel)
		}
	}

	// Upsert relations.
	if err := upsertRelationRecords(ctx, writeFn, relations); err != nil {
		return err
	}

	return markProcessed(ctx, writeFn, noteURN, headSeq)
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal write helpers
// ─────────────────────────────────────────────────────────────────────────────

func deleteParagraphsAndRelationsForNote(ctx context.Context, writeFn func(writeOp) error, noteURN string) error {
	return writeFn(func(db *sql.DB) error {
		if _, err := db.ExecContext(ctx, `DELETE FROM paragraph_relations WHERE note_urn_source = ?`, noteURN); err != nil {
			return err
		}
		_, err := db.ExecContext(ctx, `DELETE FROM note_paragraphs WHERE note_urn = ?`, noteURN)
		return err
	})
}

func upsertParagraphRecords(ctx context.Context, writeFn func(writeOp) error, paragraphs []repo.ParagraphRecord) error {
	if len(paragraphs) == 0 {
		return nil
	}
	return writeFn(func(db *sql.DB) error {
		const q = `INSERT INTO note_paragraphs
			(id, note_urn, project_urn, folder_urn, sequence, position,
			 line_start, line_end, text, role,
			 main_concepts, supporting_concepts, concept_families,
			 created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET
			  role=excluded.role,
			  main_concepts=excluded.main_concepts,
			  supporting_concepts=excluded.supporting_concepts,
			  concept_families=excluded.concept_families,
			  updated_at=excluded.updated_at`
		now := toMs(time.Now().UTC())
		for _, pg := range paragraphs {
			if _, err := db.ExecContext(ctx, q,
				pg.ID, pg.NoteURN, pg.ProjectURN, pg.FolderURN,
				pg.Sequence, pg.Position, pg.LineStart, pg.LineEnd,
				pg.Text, pg.Role,
				marshalStringSlice(pg.MainConcepts),
				marshalStringSlice(pg.SupportingConcepts),
				marshalStringSlice(pg.ConceptFamilies),
				now, now,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func upsertRelationRecords(ctx context.Context, writeFn func(writeOp) error, relations []repo.ParagraphRelationRecord) error {
	if len(relations) == 0 {
		return nil
	}
	return writeFn(func(db *sql.DB) error {
		const q = `INSERT INTO paragraph_relations
			(id, source_paragraph_id, target_paragraph_id,
			 note_urn_source, note_urn_target,
			 project_urn_source, project_urn_target,
			 folder_urn_source, folder_urn_target,
			 proximity_tier, relation_type, score,
			 reason_signals, pattern_hash, version,
			 feedback_vote, feedback_at, created_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(source_paragraph_id, target_paragraph_id, relation_type) DO UPDATE SET
			  score=excluded.score,
			  reason_signals=excluded.reason_signals,
			  pattern_hash=excluded.pattern_hash,
			  proximity_tier=excluded.proximity_tier`
		now := toMs(time.Now().UTC())
		for _, r := range relations {
			if _, err := db.ExecContext(ctx, q,
				r.ID, r.SourceParagraphID, r.TargetParagraphID,
				r.NoteURNSource, r.NoteURNTarget,
				r.ProjectURNSource, r.ProjectURNTarget,
				r.FolderURNSource, r.FolderURNTarget,
				r.ProximityTier, r.RelationType, r.Score,
				marshalStringSlice(r.ReasonSignals),
				r.PatternHash, r.Version,
				nil, nil, now,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func markProcessed(ctx context.Context, writeFn func(writeOp) error, noteURN string, seq int) error {
	return writeFn(func(db *sql.DB) error {
		_, err := db.ExecContext(ctx,
			`UPDATE notes SET paragraph_head_seq = ? WHERE urn = ?`,
			seq, noteURN,
		)
		return err
	})
}
