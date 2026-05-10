package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/zebaqui/notx-engine/repo"
)

// ─────────────────────────────────────────────────────────────────────────────
// UpsertNoteAnalysis
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) UpsertNoteAnalysis(ctx context.Context, records []repo.NoteAnalysisRecord) error {
	if len(records) == 0 {
		return nil
	}
	return p.write(func(db *sql.DB) error {
		const q = `INSERT OR REPLACE INTO note_analyses
			(id, note_urn, project_urn, folder_urn,
			 all_concepts, theme_concepts, families,
			 dominant_role, role_counts, paragraph_count, head_seq,
			 created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`
		for _, r := range records {
			allC, err := json.Marshal(r.AllConcepts)
			if err != nil {
				return fmt.Errorf("UpsertNoteAnalysis: marshal all_concepts: %w", err)
			}
			themeC, err := json.Marshal(r.ThemeConcepts)
			if err != nil {
				return fmt.Errorf("UpsertNoteAnalysis: marshal theme_concepts: %w", err)
			}
			fams, err := json.Marshal(r.Families)
			if err != nil {
				return fmt.Errorf("UpsertNoteAnalysis: marshal families: %w", err)
			}
			rc, err := json.Marshal(r.RoleCounts)
			if err != nil {
				return fmt.Errorf("UpsertNoteAnalysis: marshal role_counts: %w", err)
			}
			if r.ID == "" {
				r.ID = uuid.New().String()
			}
			now := time.Now().UTC()
			if r.CreatedAt.IsZero() {
				r.CreatedAt = now
			}
			r.UpdatedAt = now
			if _, err := db.ExecContext(ctx, q,
				r.ID, r.NoteURN, r.ProjectURN, r.FolderURN,
				string(allC), string(themeC), string(fams),
				r.DominantRole, string(rc), r.ParagraphCount, r.HeadSeq,
				r.CreatedAt.Unix(), r.UpdatedAt.Unix(),
			); err != nil {
				return fmt.Errorf("UpsertNoteAnalysis: insert: %w", err)
			}
		}
		return nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// GetNoteAnalysis
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) GetNoteAnalysis(ctx context.Context, noteURN string) (repo.NoteAnalysisRecord, error) {
	const q = `SELECT id, note_urn, project_urn, folder_urn,
		all_concepts, theme_concepts, families,
		dominant_role, role_counts, paragraph_count, head_seq,
		created_at, updated_at
		FROM note_analyses WHERE note_urn = ?`
	row := p.db.QueryRowContext(ctx, q, noteURN)
	r, err := scanNoteAnalysis(row)
	if err == sql.ErrNoRows {
		return repo.NoteAnalysisRecord{}, repo.ErrNotFound
	}
	return r, err
}

// ─────────────────────────────────────────────────────────────────────────────
// ListNoteAnalyses
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) ListNoteAnalyses(ctx context.Context, projectURN string) ([]repo.NoteAnalysisRecord, error) {
	const q = `SELECT id, note_urn, project_urn, folder_urn,
		all_concepts, theme_concepts, families,
		dominant_role, role_counts, paragraph_count, head_seq,
		created_at, updated_at
		FROM note_analyses WHERE project_urn = ?`
	rows, err := p.db.QueryContext(ctx, q, projectURN)
	if err != nil {
		return nil, fmt.Errorf("ListNoteAnalyses: query: %w", err)
	}
	defer rows.Close()

	var out []repo.NoteAnalysisRecord
	for rows.Next() {
		r, err := scanNoteAnalysisRow(rows)
		if err != nil {
			return nil, fmt.Errorf("ListNoteAnalyses: scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ─────────────────────────────────────────────────────────────────────────────
// DeleteNoteAnalysis
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) DeleteNoteAnalysis(ctx context.Context, noteURN string) error {
	return p.write(func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, `DELETE FROM note_analyses WHERE note_urn = ?`, noteURN)
		return err
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// UpsertNoteRelations
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) UpsertNoteRelations(ctx context.Context, records []repo.NoteRelationRecord) error {
	if len(records) == 0 {
		return nil
	}
	return p.write(func(db *sql.DB) error {
		const q = `INSERT OR REPLACE INTO note_relations
			(id, source_note_urn, target_note_urn, project_urn, folder_urn,
			 relation_type, score, reason_signals, version, created_at)
			VALUES (?,?,?,?,?,?,?,?,?,?)`
		for _, r := range records {
			sigs, err := json.Marshal(r.ReasonSignals)
			if err != nil {
				return fmt.Errorf("UpsertNoteRelations: marshal reason_signals: %w", err)
			}
			if r.ID == "" {
				r.ID = uuid.New().String()
			}
			if r.CreatedAt.IsZero() {
				r.CreatedAt = time.Now().UTC()
			}
			if r.Version == "" {
				r.Version = "heuristic_v1"
			}
			if _, err := db.ExecContext(ctx, q,
				r.ID, r.SourceNoteURN, r.TargetNoteURN, r.ProjectURN, r.FolderURN,
				r.RelationType, r.Score, string(sigs), r.Version, r.CreatedAt.Unix(),
			); err != nil {
				return fmt.Errorf("UpsertNoteRelations: insert: %w", err)
			}
		}
		return nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// ListNoteRelations
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) ListNoteRelations(ctx context.Context, projectURN string, limit int) ([]repo.NoteRelationRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	const q = `SELECT id, source_note_urn, target_note_urn, project_urn, folder_urn,
		relation_type, score, reason_signals, version, created_at
		FROM note_relations WHERE project_urn = ?
		ORDER BY score DESC LIMIT ?`
	rows, err := p.db.QueryContext(ctx, q, projectURN, limit)
	if err != nil {
		return nil, fmt.Errorf("ListNoteRelations: query: %w", err)
	}
	defer rows.Close()

	var out []repo.NoteRelationRecord
	for rows.Next() {
		r, err := scanNoteRelation(rows)
		if err != nil {
			return nil, fmt.Errorf("ListNoteRelations: scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ─────────────────────────────────────────────────────────────────────────────
// ListNoteRelationsForNote (used by HTTP handler)
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) ListNoteRelationsForNote(ctx context.Context, noteURN string, limit int) ([]repo.NoteRelationRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	const q = `SELECT id, source_note_urn, target_note_urn, project_urn, folder_urn,
		relation_type, score, reason_signals, version, created_at
		FROM note_relations WHERE source_note_urn = ?
		ORDER BY score DESC LIMIT ?`
	rows, err := p.db.QueryContext(ctx, q, noteURN, limit)
	if err != nil {
		return nil, fmt.Errorf("ListNoteRelationsForNote: query: %w", err)
	}
	defer rows.Close()

	var out []repo.NoteRelationRecord
	for rows.Next() {
		r, err := scanNoteRelation(rows)
		if err != nil {
			return nil, fmt.Errorf("ListNoteRelationsForNote: scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ─────────────────────────────────────────────────────────────────────────────
// DeleteNoteRelationsForNote
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) DeleteNoteRelationsForNote(ctx context.Context, noteURN string) error {
	return p.write(func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, `DELETE FROM note_relations WHERE source_note_urn = ?`, noteURN)
		return err
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Scan helpers
// ─────────────────────────────────────────────────────────────────────────────

type noteAnalysisScanner interface {
	Scan(dest ...any) error
}

func scanNoteAnalysis(s noteAnalysisScanner) (repo.NoteAnalysisRecord, error) {
	var r repo.NoteAnalysisRecord
	var allC, themeC, fams, rc string
	var createdAtUnix, updatedAtUnix int64
	if err := s.Scan(
		&r.ID, &r.NoteURN, &r.ProjectURN, &r.FolderURN,
		&allC, &themeC, &fams,
		&r.DominantRole, &rc, &r.ParagraphCount, &r.HeadSeq,
		&createdAtUnix, &updatedAtUnix,
	); err != nil {
		return repo.NoteAnalysisRecord{}, err
	}
	_ = json.Unmarshal([]byte(allC), &r.AllConcepts)
	_ = json.Unmarshal([]byte(themeC), &r.ThemeConcepts)
	_ = json.Unmarshal([]byte(fams), &r.Families)
	_ = json.Unmarshal([]byte(rc), &r.RoleCounts)
	r.CreatedAt = time.Unix(createdAtUnix, 0).UTC()
	r.UpdatedAt = time.Unix(updatedAtUnix, 0).UTC()
	return r, nil
}

func scanNoteAnalysisRow(rows *sql.Rows) (repo.NoteAnalysisRecord, error) {
	var r repo.NoteAnalysisRecord
	var allC, themeC, fams, rc string
	var createdAtUnix, updatedAtUnix int64
	if err := rows.Scan(
		&r.ID, &r.NoteURN, &r.ProjectURN, &r.FolderURN,
		&allC, &themeC, &fams,
		&r.DominantRole, &rc, &r.ParagraphCount, &r.HeadSeq,
		&createdAtUnix, &updatedAtUnix,
	); err != nil {
		return repo.NoteAnalysisRecord{}, err
	}
	_ = json.Unmarshal([]byte(allC), &r.AllConcepts)
	_ = json.Unmarshal([]byte(themeC), &r.ThemeConcepts)
	_ = json.Unmarshal([]byte(fams), &r.Families)
	_ = json.Unmarshal([]byte(rc), &r.RoleCounts)
	r.CreatedAt = time.Unix(createdAtUnix, 0).UTC()
	r.UpdatedAt = time.Unix(updatedAtUnix, 0).UTC()
	return r, nil
}

func scanNoteRelation(rows *sql.Rows) (repo.NoteRelationRecord, error) {
	var r repo.NoteRelationRecord
	var sigs string
	var createdAtUnix int64
	if err := rows.Scan(
		&r.ID, &r.SourceNoteURN, &r.TargetNoteURN, &r.ProjectURN, &r.FolderURN,
		&r.RelationType, &r.Score, &sigs, &r.Version, &createdAtUnix,
	); err != nil {
		return repo.NoteRelationRecord{}, err
	}
	_ = json.Unmarshal([]byte(sigs), &r.ReasonSignals)
	r.CreatedAt = time.Unix(createdAtUnix, 0).UTC()
	return r, nil
}
