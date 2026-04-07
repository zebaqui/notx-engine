package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/zebaqui/notx-engine/repo"
)

// ─────────────────────────────────────────────────────────────────────────────
// Anchors
// ─────────────────────────────────────────────────────────────────────────────

// UpsertAnchor inserts or replaces an anchor record in the index.
func (p *Provider) UpsertAnchor(ctx context.Context, a repo.AnchorRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.write(func(db *sql.DB) error {
		_, err := db.Exec(
			`INSERT INTO anchors(note_urn, anchor_id, line, char_start, char_end, preview, status, updated_at)
			 VALUES(?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(note_urn, anchor_id) DO UPDATE SET
			     line       = excluded.line,
			     char_start = excluded.char_start,
			     char_end   = excluded.char_end,
			     preview    = excluded.preview,
			     status     = excluded.status,
			     updated_at = excluded.updated_at`,
			a.NoteURN,
			a.AnchorID,
			a.Line,
			a.CharStart,
			a.CharEnd,
			a.Preview,
			a.Status,
			a.UpdatedAt.UTC().Format(time.RFC3339),
		)
		if err != nil {
			return fmt.Errorf("sqlite: upsert anchor: %w", err)
		}
		return nil
	})
}

// DeleteAnchor removes an anchor from the index. If createTombstone is true,
// the anchor status is set to "deprecated" instead of being deleted.
func (p *Provider) DeleteAnchor(ctx context.Context, noteURN, anchorID string, createTombstone bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.write(func(db *sql.DB) error {
		var (
			res sql.Result
			err error
		)
		if createTombstone {
			res, err = db.Exec(
				`UPDATE anchors SET status='deprecated', updated_at=? WHERE note_urn=? AND anchor_id=?`,
				time.Now().UTC().Format(time.RFC3339), noteURN, anchorID,
			)
		} else {
			res, err = db.Exec(
				`DELETE FROM anchors WHERE note_urn=? AND anchor_id=?`,
				noteURN, anchorID,
			)
		}
		if err != nil {
			return fmt.Errorf("sqlite: delete anchor: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("%w: anchor %s/%s", repo.ErrNotFound, noteURN, anchorID)
		}
		return nil
	})
}

// GetAnchor retrieves a single anchor by note URN and anchor ID.
// Returns repo.ErrNotFound if the anchor does not exist.
func (p *Provider) GetAnchor(ctx context.Context, noteURN, anchorID string) (repo.AnchorRecord, error) {
	if err := ctx.Err(); err != nil {
		return repo.AnchorRecord{}, err
	}
	row := p.db.QueryRowContext(ctx,
		`SELECT note_urn, anchor_id, line, char_start, char_end, preview, status, updated_at
		 FROM anchors
		 WHERE note_urn=? AND anchor_id=?`,
		noteURN, anchorID,
	)
	a, err := scanAnchor(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return repo.AnchorRecord{}, fmt.Errorf("%w: anchor %s/%s", repo.ErrNotFound, noteURN, anchorID)
		}
		return repo.AnchorRecord{}, fmt.Errorf("sqlite: get anchor: %w", err)
	}
	return a, nil
}

// ListAnchors returns all anchors declared in a note, ordered by line ASC.
func (p *Provider) ListAnchors(ctx context.Context, noteURN string) ([]repo.AnchorRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := p.db.QueryContext(ctx,
		`SELECT note_urn, anchor_id, line, char_start, char_end, preview, status, updated_at
		 FROM anchors
		 WHERE note_urn=?
		 ORDER BY line ASC`,
		noteURN,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list anchors: %w", err)
	}
	defer rows.Close()

	var anchors []repo.AnchorRecord
	for rows.Next() {
		a, err := scanAnchorRow(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: list anchors scan: %w", err)
		}
		anchors = append(anchors, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list anchors rows: %w", err)
	}
	return anchors, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Backlinks
// ─────────────────────────────────────────────────────────────────────────────

// UpsertBacklink inserts or replaces a backlink record.
func (p *Provider) UpsertBacklink(ctx context.Context, b repo.BacklinkRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.write(func(db *sql.DB) error {
		_, err := db.Exec(
			`INSERT INTO backlinks(source_urn, target_urn, target_anchor, label, created_at)
			 VALUES(?, ?, ?, ?, ?)
			 ON CONFLICT(source_urn, target_urn, target_anchor) DO UPDATE SET
			     label      = excluded.label,
			     created_at = excluded.created_at`,
			b.SourceURN,
			b.TargetURN,
			b.TargetAnchor,
			b.Label,
			b.CreatedAt.UTC().Format(time.RFC3339),
		)
		if err != nil {
			return fmt.Errorf("sqlite: upsert backlink: %w", err)
		}
		return nil
	})
}

// DeleteBacklink removes a specific backlink record.
func (p *Provider) DeleteBacklink(ctx context.Context, sourceURN, targetURN, targetAnchor string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.write(func(db *sql.DB) error {
		res, err := db.Exec(
			`DELETE FROM backlinks WHERE source_urn=? AND target_urn=? AND target_anchor=?`,
			sourceURN, targetURN, targetAnchor,
		)
		if err != nil {
			return fmt.Errorf("sqlite: delete backlink: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("%w: backlink %s -> %s#%s", repo.ErrNotFound, sourceURN, targetURN, targetAnchor)
		}
		return nil
	})
}

// ListBacklinks returns all inbound backlinks for a note.
// If anchorID is non-empty, restricts to backlinks for that anchor only.
func (p *Provider) ListBacklinks(ctx context.Context, targetURN, anchorID string) ([]repo.BacklinkRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var (
		rows *sql.Rows
		err  error
	)
	if anchorID != "" {
		rows, err = p.db.QueryContext(ctx,
			`SELECT source_urn, target_urn, target_anchor, label, created_at
			 FROM backlinks
			 WHERE target_urn=? AND target_anchor=?`,
			targetURN, anchorID,
		)
	} else {
		rows, err = p.db.QueryContext(ctx,
			`SELECT source_urn, target_urn, target_anchor, label, created_at
			 FROM backlinks
			 WHERE target_urn=?`,
			targetURN,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: list backlinks: %w", err)
	}
	defer rows.Close()

	return scanBacklinks(rows)
}

// ListOutboundLinks returns all outbound backlink records from a source note.
func (p *Provider) ListOutboundLinks(ctx context.Context, sourceURN string) ([]repo.BacklinkRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := p.db.QueryContext(ctx,
		`SELECT source_urn, target_urn, target_anchor, label, created_at
		 FROM backlinks
		 WHERE source_urn=?`,
		sourceURN,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list outbound links: %w", err)
	}
	defer rows.Close()

	return scanBacklinks(rows)
}

// RecentBacklinks returns recently created backlinks with optional filters.
func (p *Provider) RecentBacklinks(ctx context.Context, opts repo.RecentBacklinksOptions) ([]repo.BacklinkRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	query := `SELECT source_urn, target_urn, target_anchor, label, created_at
	          FROM backlinks
	          WHERE 1=1`
	args := []any{}

	if opts.NoteURN != "" {
		query += ` AND (source_urn=? OR target_urn=?)`
		args = append(args, opts.NoteURN, opts.NoteURN)
	}
	if opts.Label != "" {
		query += ` AND label LIKE ?`
		args = append(args, "%"+opts.Label+"%")
	}

	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: recent backlinks: %w", err)
	}
	defer rows.Close()

	return scanBacklinks(rows)
}

// GetReferrers returns the URNs of all notes that link to a specific anchor.
func (p *Provider) GetReferrers(ctx context.Context, targetURN, anchorID string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := p.db.QueryContext(ctx,
		`SELECT DISTINCT source_urn
		 FROM backlinks
		 WHERE target_urn=? AND target_anchor=?`,
		targetURN, anchorID,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: get referrers: %w", err)
	}
	defer rows.Close()

	var urns []string
	for rows.Next() {
		var urn string
		if err := rows.Scan(&urn); err != nil {
			return nil, fmt.Errorf("sqlite: get referrers scan: %w", err)
		}
		urns = append(urns, urn)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: get referrers rows: %w", err)
	}
	return urns, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// External links
// ─────────────────────────────────────────────────────────────────────────────

// UpsertExternalLink inserts or replaces an external link record.
func (p *Provider) UpsertExternalLink(ctx context.Context, e repo.ExternalLinkRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.write(func(db *sql.DB) error {
		_, err := db.Exec(
			`INSERT INTO external_links(source_urn, uri, label, created_at)
			 VALUES(?, ?, ?, ?)
			 ON CONFLICT(source_urn, uri) DO UPDATE SET
			     label      = excluded.label,
			     created_at = excluded.created_at`,
			e.SourceURN,
			e.URI,
			e.Label,
			e.CreatedAt.UTC().Format(time.RFC3339),
		)
		if err != nil {
			return fmt.Errorf("sqlite: upsert external link: %w", err)
		}
		return nil
	})
}

// DeleteExternalLink removes an external link record.
func (p *Provider) DeleteExternalLink(ctx context.Context, sourceURN, uri string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.write(func(db *sql.DB) error {
		res, err := db.Exec(
			`DELETE FROM external_links WHERE source_urn=? AND uri=?`,
			sourceURN, uri,
		)
		if err != nil {
			return fmt.Errorf("sqlite: delete external link: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("%w: external link %s -> %s", repo.ErrNotFound, sourceURN, uri)
		}
		return nil
	})
}

// ListExternalLinks returns all external links from a source note.
func (p *Provider) ListExternalLinks(ctx context.Context, sourceURN string) ([]repo.ExternalLinkRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := p.db.QueryContext(ctx,
		`SELECT source_urn, uri, label, created_at
		 FROM external_links
		 WHERE source_urn=?`,
		sourceURN,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list external links: %w", err)
	}
	defer rows.Close()

	var links []repo.ExternalLinkRecord
	for rows.Next() {
		var (
			e         repo.ExternalLinkRecord
			createdAt string
		)
		if err := rows.Scan(&e.SourceURN, &e.URI, &e.Label, &createdAt); err != nil {
			return nil, fmt.Errorf("sqlite: list external links scan: %w", err)
		}
		t, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, fmt.Errorf("sqlite: list external links parse time: %w", err)
		}
		e.CreatedAt = t.UTC()
		links = append(links, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list external links rows: %w", err)
	}
	return links, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Scan helpers
// ─────────────────────────────────────────────────────────────────────────────

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanAnchor(r rowScanner) (repo.AnchorRecord, error) {
	var (
		a         repo.AnchorRecord
		updatedAt string
	)
	if err := r.Scan(
		&a.NoteURN,
		&a.AnchorID,
		&a.Line,
		&a.CharStart,
		&a.CharEnd,
		&a.Preview,
		&a.Status,
		&updatedAt,
	); err != nil {
		return repo.AnchorRecord{}, err
	}
	t, err := time.Parse(time.RFC3339, updatedAt)
	if err != nil {
		return repo.AnchorRecord{}, fmt.Errorf("parse updated_at: %w", err)
	}
	a.UpdatedAt = t.UTC()
	return a, nil
}

func scanAnchorRow(rows *sql.Rows) (repo.AnchorRecord, error) {
	var (
		a         repo.AnchorRecord
		updatedAt string
	)
	if err := rows.Scan(
		&a.NoteURN,
		&a.AnchorID,
		&a.Line,
		&a.CharStart,
		&a.CharEnd,
		&a.Preview,
		&a.Status,
		&updatedAt,
	); err != nil {
		return repo.AnchorRecord{}, err
	}
	t, err := time.Parse(time.RFC3339, updatedAt)
	if err != nil {
		return repo.AnchorRecord{}, fmt.Errorf("parse updated_at: %w", err)
	}
	a.UpdatedAt = t.UTC()
	return a, nil
}

func scanBacklinks(rows *sql.Rows) ([]repo.BacklinkRecord, error) {
	var backlinks []repo.BacklinkRecord
	for rows.Next() {
		var (
			b         repo.BacklinkRecord
			createdAt string
		)
		if err := rows.Scan(
			&b.SourceURN,
			&b.TargetURN,
			&b.TargetAnchor,
			&b.Label,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("sqlite: scan backlink: %w", err)
		}
		t, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, fmt.Errorf("sqlite: parse backlink created_at: %w", err)
		}
		b.CreatedAt = t.UTC()
		backlinks = append(backlinks, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: backlinks rows: %w", err)
	}
	return backlinks, nil
}
