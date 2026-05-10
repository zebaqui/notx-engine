package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/repo"
)

// ─────────────────────────────────────────────────────────────────────────────
// NoteRunnerConfig
// ─────────────────────────────────────────────────────────────────────────────

// NoteRunnerConfig controls the behaviour of the background note analysis runner.
type NoteRunnerConfig struct {
	// PollInterval is how often the runner checks for stale note analyses.
	PollInterval time.Duration // default: 60s
	// MinScore is the minimum score for a note relation to be stored.
	MinScore float64 // default: 0.30
	// TopNPerNote is the maximum number of note relations per source note.
	TopNPerNote int // default: 10
	// CrossFolder allows relations across folders. When false, only same-folder
	// notes are compared (within the same project).
	CrossFolder bool // default: false
}

// DefaultNoteRunnerConfig returns the spec-recommended defaults.
func DefaultNoteRunnerConfig() NoteRunnerConfig {
	return NoteRunnerConfig{
		PollInterval: 60 * time.Second,
		MinScore:     0.30,
		TopNPerNote:  10,
		CrossFolder:  false,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// StartNoteRunner
// ─────────────────────────────────────────────────────────────────────────────

// StartNoteRunner starts the background note analysis goroutine.
// It returns immediately; processing runs until ctx is cancelled.
func StartNoteRunner(
	ctx context.Context,
	db *sql.DB,
	writeFn func(writeOp) error,
	cfg NoteRunnerConfig,
) {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 60 * time.Second
	}
	if cfg.MinScore == 0 {
		cfg.MinScore = 0.30
	}
	if cfg.TopNPerNote == 0 {
		cfg.TopNPerNote = 10
	}
	go runNoteRunner(ctx, db, writeFn, cfg)
}

func runNoteRunner(
	ctx context.Context,
	db *sql.DB,
	writeFn func(writeOp) error,
	cfg NoteRunnerConfig,
) {
	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := runNoteAnalysisBatch(ctx, db, writeFn, cfg); err != nil {
				fmt.Printf("note runner: WARN: %v\n", err)
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// runNoteAnalysisBatch
// ─────────────────────────────────────────────────────────────────────────────

func runNoteAnalysisBatch(
	ctx context.Context,
	db *sql.DB,
	writeFn func(writeOp) error,
	cfg NoteRunnerConfig,
) error {
	// 1. Find notes where head_seq is greater than the stored head_seq in
	//    note_analyses (or not in note_analyses at all).
	const q = `
		SELECT n.urn, n.head_seq, n.project_urn, n.folder_urn
		FROM notes n
		LEFT JOIN note_analyses na ON n.urn = na.note_urn
		WHERE n.deleted = 0 AND (na.note_urn IS NULL OR n.head_seq > na.head_seq)
		ORDER BY n.updated_at ASC LIMIT 20`

	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return fmt.Errorf("runNoteAnalysisBatch: query: %w", err)
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
			return fmt.Errorf("runNoteAnalysisBatch: scan: %w", err)
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

	for _, n := range notes {
		if err := runNoteAnalysis(ctx, db, writeFn, n.urn, n.headSeq, n.projectURN, n.folderURN, cfg); err != nil {
			fmt.Printf("note runner: WARN: note %s: %v\n", n.urn, err)
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// runNoteAnalysis — process a single note
// ─────────────────────────────────────────────────────────────────────────────

func runNoteAnalysis(
	ctx context.Context,
	db *sql.DB,
	writeFn func(writeOp) error,
	noteURN string,
	headSeq int,
	projectURN, folderURN string,
	cfg NoteRunnerConfig,
) error {
	// 2. Fetch note content.
	var content string
	err := db.QueryRowContext(ctx,
		`SELECT content FROM note_content WHERE urn = ?`, noteURN,
	).Scan(&content)
	if err == sql.ErrNoRows {
		// No content yet — store an empty analysis so we don't loop forever.
		return upsertEmptyNoteAnalysis(ctx, writeFn, noteURN, projectURN, folderURN, headSeq)
	}
	if err != nil {
		return fmt.Errorf("runNoteAnalysis: fetch content: %w", err)
	}

	// 3. Split + classify paragraphs.
	rawParagraphs := core.SplitParagraphs(content)
	var annotated []core.AnnotatedParagraph
	for i, raw := range rawParagraphs {
		role := core.ClassifyRole(raw.Text)
		main, _, _ := core.ExtractConcepts(raw.Text)
		annotated = append(annotated, core.AnnotatedParagraph{
			ID:           uuid.New().String(),
			NoteURN:      noteURN,
			FolderURN:    folderURN,
			ProjectURN:   projectURN,
			Position:     i,
			Text:         raw.Text,
			Role:         role,
			MainConcepts: main,
		})
	}

	// 4. Build NoteAnalysis.
	analysis := core.AnalyzeNote(noteURN, projectURN, folderURN, annotated)

	// 5. Persist the analysis.
	rec := noteAnalysisToRecord(analysis, headSeq)
	if err := writeFn(func(db *sql.DB) error {
		allC := mustMarshalJSON(rec.AllConcepts)
		themeC := mustMarshalJSON(rec.ThemeConcepts)
		fams := mustMarshalJSON(rec.Families)
		rc := mustMarshalJSON(rec.RoleCounts)
		now := time.Now().UTC().Unix()
		_, err := db.ExecContext(ctx,
			`INSERT OR REPLACE INTO note_analyses
			(id, note_urn, project_urn, folder_urn,
			 all_concepts, theme_concepts, families,
			 dominant_role, role_counts, paragraph_count, head_seq,
			 created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			rec.ID, rec.NoteURN, rec.ProjectURN, rec.FolderURN,
			allC, themeC, fams,
			rec.DominantRole, rc, rec.ParagraphCount, rec.HeadSeq,
			now, now,
		)
		return err
	}); err != nil {
		return fmt.Errorf("runNoteAnalysis: upsert analysis: %w", err)
	}

	// 6. Load all analyses for the same project (and optionally same folder).
	peers, err := loadPeerAnalyses(ctx, db, projectURN, folderURN, cfg.CrossFolder)
	if err != nil {
		return fmt.Errorf("runNoteAnalysis: load peers: %w", err)
	}

	// 7. Score against all other notes.
	type candidate struct {
		rel   repo.NoteRelationRecord
		score float64
	}
	var candidates []candidate

	for _, peer := range peers {
		if peer.NoteURN == noteURN {
			continue
		}
		peerAnalysis := noteRecordToAnalysis(peer)
		scored := core.ScoreNoteRelation(analysis, peerAnalysis)
		if scored.Score < cfg.MinScore {
			continue
		}
		furn := folderURN
		if peer.FolderURN != folderURN {
			furn = "" // cross-folder
		}
		candidates = append(candidates, candidate{
			score: scored.Score,
			rel: repo.NoteRelationRecord{
				ID:            uuid.New().String(),
				SourceNoteURN: noteURN,
				TargetNoteURN: peer.NoteURN,
				ProjectURN:    projectURN,
				FolderURN:     furn,
				RelationType:  string(scored.RelationType),
				Score:         scored.Score,
				ReasonSignals: scored.ReasonSignals,
				Version:       "heuristic_v1",
				CreatedAt:     time.Now().UTC(),
			},
		})
	}

	// 8. Sort descending and keep top N.
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})
	topN := cfg.TopNPerNote
	if len(candidates) < topN {
		topN = len(candidates)
	}
	topRelations := make([]repo.NoteRelationRecord, topN)
	for i := range topN {
		topRelations[i] = candidates[i].rel
	}

	// 9. Delete old relations for this note, then upsert new ones.
	if err := writeFn(func(db *sql.DB) error {
		_, err := db.ExecContext(ctx,
			`DELETE FROM note_relations WHERE source_note_urn = ?`, noteURN)
		return err
	}); err != nil {
		return fmt.Errorf("runNoteAnalysis: delete old relations: %w", err)
	}

	if len(topRelations) > 0 {
		if err := writeFn(func(db *sql.DB) error {
			const ins = `INSERT OR REPLACE INTO note_relations
				(id, source_note_urn, target_note_urn, project_urn, folder_urn,
				 relation_type, score, reason_signals, version, created_at)
				VALUES (?,?,?,?,?,?,?,?,?,?)`
			for _, r := range topRelations {
				sigs := mustMarshalJSON(r.ReasonSignals)
				if _, err := db.ExecContext(ctx, ins,
					r.ID, r.SourceNoteURN, r.TargetNoteURN, r.ProjectURN, r.FolderURN,
					r.RelationType, r.Score, sigs, r.Version, r.CreatedAt.Unix(),
				); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return fmt.Errorf("runNoteAnalysis: upsert relations: %w", err)
		}
	}

	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func upsertEmptyNoteAnalysis(
	ctx context.Context,
	writeFn func(writeOp) error,
	noteURN, projectURN, folderURN string,
	headSeq int,
) error {
	return writeFn(func(db *sql.DB) error {
		now := time.Now().UTC().Unix()
		_, err := db.ExecContext(ctx,
			`INSERT OR REPLACE INTO note_analyses
			(id, note_urn, project_urn, folder_urn,
			 all_concepts, theme_concepts, families,
			 dominant_role, role_counts, paragraph_count, head_seq,
			 created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			uuid.New().String(), noteURN, projectURN, folderURN,
			"[]", "[]", "[]", "claim", "{}", 0, headSeq,
			now, now,
		)
		return err
	})
}

func noteAnalysisToRecord(a core.NoteAnalysis, headSeq int) repo.NoteAnalysisRecord {
	now := time.Now().UTC()
	return repo.NoteAnalysisRecord{
		ID:             uuid.New().String(),
		NoteURN:        a.NoteURN,
		ProjectURN:     a.ProjectURN,
		FolderURN:      a.FolderURN,
		AllConcepts:    a.AllConcepts,
		ThemeConcepts:  a.ThemeConcepts,
		Families:       a.Families,
		DominantRole:   string(a.DominantRole),
		RoleCounts:     a.RoleCounts,
		ParagraphCount: a.ParagraphCount,
		HeadSeq:        headSeq,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

func noteRecordToAnalysis(r repo.NoteAnalysisRecord) core.NoteAnalysis {
	return core.NoteAnalysis{
		NoteURN:        r.NoteURN,
		ProjectURN:     r.ProjectURN,
		FolderURN:      r.FolderURN,
		AllConcepts:    r.AllConcepts,
		ThemeConcepts:  r.ThemeConcepts,
		Families:       r.Families,
		DominantRole:   core.ParagraphRole(r.DominantRole),
		RoleCounts:     r.RoleCounts,
		ParagraphCount: r.ParagraphCount,
	}
}

// loadPeerAnalyses loads NoteAnalysisRecords for peer notes in the same project.
// When crossFolder is false, only notes in the same folder are returned.
func loadPeerAnalyses(
	ctx context.Context,
	db *sql.DB,
	projectURN, folderURN string,
	crossFolder bool,
) ([]repo.NoteAnalysisRecord, error) {
	var rows *sql.Rows
	var err error

	if crossFolder || folderURN == "" {
		rows, err = db.QueryContext(ctx,
			`SELECT id, note_urn, project_urn, folder_urn,
			 all_concepts, theme_concepts, families,
			 dominant_role, role_counts, paragraph_count, head_seq,
			 created_at, updated_at
			 FROM note_analyses WHERE project_urn = ?`,
			projectURN,
		)
	} else {
		rows, err = db.QueryContext(ctx,
			`SELECT id, note_urn, project_urn, folder_urn,
			 all_concepts, theme_concepts, families,
			 dominant_role, role_counts, paragraph_count, head_seq,
			 created_at, updated_at
			 FROM note_analyses WHERE project_urn = ? AND folder_urn = ?`,
			projectURN, folderURN,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("loadPeerAnalyses: query: %w", err)
	}
	defer rows.Close()

	var out []repo.NoteAnalysisRecord
	for rows.Next() {
		r, err := scanNoteAnalysisRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func mustMarshalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}
