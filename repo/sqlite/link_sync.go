// repo/sqlite/link_sync.go
//
// Frontmatter-driven link index reconciliation for the SQLite provider.
// Mirrors the logic in notx/internal/engine/postgres/link_sync.go but adapted
// for the SQLite write-serialisation model and table layout.

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/repo"
)

// syncLinksFromFrontmatter parses the frontmatter block from noteContent and
// fully reconciles the anchor, backlink, and external-link indexes for the note.
//
// Called asynchronously after every successful AppendEvent commit. All errors
// are logged and suppressed — link sync must never fail a write.
func (p *Provider) syncLinksFromFrontmatter(ctx context.Context, noteURN, noteContent string) {
	fm, _, err := core.ParseFrontMatter(noteContent)
	if err != nil {
		slog.Warn("link_sync: parse frontmatter failed", "note_urn", noteURN, "err", err)
		return
	}

	// No frontmatter at all — wipe every indexed entry for this note.
	if fm == nil {
		p.deleteAllAnchorsForNote(ctx, noteURN)
		p.deleteAllOutboundLinksForNote(ctx, noteURN)
		p.deleteAllExternalLinksForNote(ctx, noteURN)
		return
	}

	p.reconcileAnchors(ctx, noteURN, fm.Anchors)
	p.reconcileNodeLinks(ctx, noteURN, fm.NodeLinks)
}

// ── Anchors ───────────────────────────────────────────────────────────────────

func (p *Provider) reconcileAnchors(ctx context.Context, noteURN string, anchors []core.Anchor) {
	want := make(map[string]struct{}, len(anchors))
	for _, a := range anchors {
		want[a.ID] = struct{}{}
	}

	now := time.Now().UTC()
	for _, a := range anchors {
		status := string(a.Status)
		if status == "" {
			status = "ok"
		}
		if err := p.UpsertAnchor(ctx, repo.AnchorRecord{
			NoteURN:   noteURN,
			AnchorID:  a.ID,
			Line:      a.Line,
			CharStart: a.CharStart,
			CharEnd:   a.CharEnd,
			Preview:   a.Preview,
			Status:    status,
			UpdatedAt: now,
		}); err != nil {
			slog.Warn("link_sync: upsert anchor failed",
				"note_urn", noteURN, "anchor_id", a.ID, "err", err)
		}
	}

	existing, err := p.ListAnchors(ctx, noteURN)
	if err != nil {
		slog.Warn("link_sync: list anchors failed (skipping stale-delete)",
			"note_urn", noteURN, "err", err)
		return
	}
	for _, a := range existing {
		if _, ok := want[a.AnchorID]; !ok {
			if err := p.DeleteAnchor(ctx, noteURN, a.AnchorID, false); err != nil {
				slog.Warn("link_sync: delete stale anchor failed",
					"note_urn", noteURN, "anchor_id", a.AnchorID, "err", err)
			}
		}
	}
}

// ── Node links (backlinks + external links) ───────────────────────────────────

func (p *Provider) reconcileNodeLinks(ctx context.Context, noteURN string, nodeLinks map[string]string) {
	type backlinkKey struct{ targetURN, targetAnchor string }
	type extKey struct{ uri string }

	wantBacklinks := make(map[backlinkKey]struct{})
	wantExternal := make(map[extKey]struct{})

	now := time.Now().UTC()
	for label, token := range nodeLinks {
		pl, err := core.ParseLinkToken(token)
		if err != nil {
			slog.Warn("link_sync: parse link token failed",
				"note_urn", noteURN, "label", label, "token", token, "err", err)
			continue
		}
		switch pl.LinkType {
		case core.LinkTypeNotxID:
			targetURN := pl.TargetURN
			if targetURN == "" {
				targetURN = noteURN
			}
			key := backlinkKey{targetURN, pl.TargetAnchor}
			wantBacklinks[key] = struct{}{}

			if err := p.UpsertBacklink(ctx, repo.BacklinkRecord{
				SourceURN:    noteURN,
				TargetURN:    targetURN,
				TargetAnchor: pl.TargetAnchor,
				Label:        label,
				CreatedAt:    now,
			}); err != nil {
				slog.Warn("link_sync: upsert backlink failed",
					"note_urn", noteURN, "label", label, "err", err)
			}

		case core.LinkTypeExternalURI:
			key := extKey{pl.URI}
			wantExternal[key] = struct{}{}

			if err := p.UpsertExternalLink(ctx, repo.ExternalLinkRecord{
				SourceURN: noteURN,
				URI:       pl.URI,
				Label:     label,
				CreatedAt: now,
			}); err != nil {
				slog.Warn("link_sync: upsert external link failed",
					"note_urn", noteURN, "label", label, "err", err)
			}
		}
	}

	existingBacklinks, err := p.ListOutboundLinks(ctx, noteURN)
	if err != nil {
		slog.Warn("link_sync: list outbound links failed (skipping stale-delete)",
			"note_urn", noteURN, "err", err)
	} else {
		for _, b := range existingBacklinks {
			key := backlinkKey{b.TargetURN, b.TargetAnchor}
			if _, ok := wantBacklinks[key]; !ok {
				if err := p.DeleteBacklink(ctx, noteURN, b.TargetURN, b.TargetAnchor); err != nil {
					slog.Warn("link_sync: delete stale backlink failed",
						"note_urn", noteURN,
						"target_urn", b.TargetURN,
						"target_anchor", b.TargetAnchor,
						"err", err)
				}
			}
		}
	}

	existingExt, err := p.ListExternalLinks(ctx, noteURN)
	if err != nil {
		slog.Warn("link_sync: list external links failed (skipping stale-delete)",
			"note_urn", noteURN, "err", err)
	} else {
		for _, el := range existingExt {
			key := extKey{el.URI}
			if _, ok := wantExternal[key]; !ok {
				if err := p.DeleteExternalLink(ctx, noteURN, el.URI); err != nil {
					slog.Warn("link_sync: delete stale external link failed",
						"note_urn", noteURN, "uri", el.URI, "err", err)
				}
			}
		}
	}
}

// ── Title-change cascade ──────────────────────────────────────────────────────

// cascadeTitleChange detects when a note's frontmatter title changed and
// updates the `links:` label in every source note that used the old title slug.
func (p *Provider) cascadeTitleChange(ctx context.Context, noteURN, oldContent, newContent string) {
	oldFm, _, err := core.ParseFrontMatter(oldContent)
	if err != nil || oldFm == nil {
		return
	}
	newFm, _, err := core.ParseFrontMatter(newContent)
	if err != nil || newFm == nil {
		return
	}

	oldTitle := strings.TrimSpace(oldFm.Title)
	newTitle := strings.TrimSpace(newFm.Title)
	if oldTitle == "" || newTitle == "" || oldTitle == newTitle {
		return
	}

	oldSlug := core.SlugFromText(oldTitle, nil)
	newSlug := core.SlugFromText(newTitle, nil)
	if oldSlug == newSlug {
		return
	}

	rows, err := p.db.QueryContext(ctx,
		`SELECT DISTINCT source_urn FROM backlinks WHERE target_urn = ?`,
		noteURN,
	)
	if err != nil {
		slog.Warn("link_sync: cascade title: query source urns failed",
			"note_urn", noteURN, "err", err)
		return
	}
	defer rows.Close()

	var sourceURNs []string
	for rows.Next() {
		var urn string
		if scanErr := rows.Scan(&urn); scanErr == nil {
			sourceURNs = append(sourceURNs, urn)
		}
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		slog.Warn("link_sync: cascade title: iterate rows failed",
			"note_urn", noteURN, "err", rowsErr)
		return
	}

	for _, sourceURN := range sourceURNs {
		p.updateLinkLabelInNote(ctx, sourceURN, noteURN, oldSlug, newSlug)
	}
}

// updateLinkLabelInNote rewrites the `links:` frontmatter in a source note,
// renaming any entry whose label == oldLabel and token resolves to targetURN.
func (p *Provider) updateLinkLabelInNote(ctx context.Context, sourceURN, targetURN, oldLabel, newLabel string) {
	var content string
	err := p.db.QueryRowContext(ctx,
		`SELECT content FROM note_content WHERE urn = ?`, sourceURN,
	).Scan(&content)
	if err != nil {
		if err != sql.ErrNoRows {
			slog.Warn("link_sync: cascade title: load source note failed",
				"source_urn", sourceURN, "err", err)
		}
		return
	}

	fm, body, err := core.ParseFrontMatter(content)
	if err != nil || fm == nil {
		return
	}

	updated := false
	newNodeLinks := make(map[string]string, len(fm.NodeLinks))
	for label, token := range fm.NodeLinks {
		if label != oldLabel {
			newNodeLinks[label] = token
			continue
		}
		pl, parseErr := core.ParseLinkToken(token)
		if parseErr != nil {
			newNodeLinks[label] = token
			continue
		}
		resolvedTarget := pl.TargetURN
		if resolvedTarget == "" {
			// Self-reference — cannot point to another note.
			newNodeLinks[label] = token
			continue
		}
		if resolvedTarget == targetURN {
			newNodeLinks[newLabel] = token
			updated = true
		} else {
			newNodeLinks[label] = token
		}
	}

	if !updated {
		return
	}

	fm.NodeLinks = newNodeLinks
	newFrontmatter := core.FormatFrontMatter(*fm)
	var newContent string
	if body != "" {
		newContent = newFrontmatter + "\n" + body
	} else {
		newContent = newFrontmatter + "\n"
	}

	// Write updated content through the serialised write queue.
	if err := p.write(func(db *sql.DB) error {
		_, err := db.ExecContext(ctx,
			`INSERT INTO note_content(urn, content) VALUES(?, ?)
			 ON CONFLICT(urn) DO UPDATE SET content = excluded.content`,
			sourceURN, newContent,
		)
		return err
	}); err != nil {
		slog.Warn("link_sync: cascade title: update source note content failed",
			"source_urn", sourceURN, "target_urn", targetURN, "err", err)
		return
	}

	slog.Debug("link_sync: cascade title: updated link label",
		"source_urn", sourceURN,
		"target_urn", targetURN,
		"old_label", oldLabel,
		"new_label", newLabel,
	)
}

// ── Bulk-delete helpers ───────────────────────────────────────────────────────

// RelabelLinks implements repo.LinkRepository.
func (p *Provider) RelabelLinks(ctx context.Context, targetURN, oldLabel, newLabel string) ([]string, error) {
	if oldLabel == "" || newLabel == "" || oldLabel == newLabel {
		return nil, nil
	}

	// Find all source notes with a backlink to targetURN.
	rows, err := p.db.QueryContext(ctx,
		`SELECT DISTINCT source_urn FROM backlinks WHERE target_urn = ?`,
		targetURN,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: relabel links: query sources: %w", err)
	}
	defer rows.Close()

	var sourceURNs []string
	for rows.Next() {
		var urn string
		if err := rows.Scan(&urn); err == nil {
			sourceURNs = append(sourceURNs, urn)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: relabel links: iterate rows: %w", err)
	}

	var updated []string
	for _, sourceURN := range sourceURNs {
		if ok := p.relabelInNote(ctx, sourceURN, targetURN, oldLabel, newLabel); ok {
			updated = append(updated, sourceURN)
		}
	}
	return updated, nil
}

// relabelInNote does the actual frontmatter rewrite for one source note.
// Returns true if the note was modified.
func (p *Provider) relabelInNote(ctx context.Context, sourceURN, targetURN, oldLabel, newLabel string) bool {
	var content string
	err := p.db.QueryRowContext(ctx,
		`SELECT content FROM note_content WHERE urn = ?`, sourceURN,
	).Scan(&content)
	if err != nil {
		return false
	}

	fm, body, err := core.ParseFrontMatter(content)
	if err != nil || fm == nil {
		return false
	}

	updated := false
	newNodeLinks := make(map[string]string, len(fm.NodeLinks))
	for label, token := range fm.NodeLinks {
		if label != oldLabel {
			newNodeLinks[label] = token
			continue
		}
		pl, parseErr := core.ParseLinkToken(token)
		if parseErr != nil {
			newNodeLinks[label] = token
			continue
		}
		resolved := pl.TargetURN
		if resolved == "" {
			newNodeLinks[label] = token
			continue
		}
		if resolved == targetURN {
			newNodeLinks[newLabel] = token
			updated = true
		} else {
			newNodeLinks[label] = token
		}
	}
	if !updated {
		return false
	}

	fm.NodeLinks = newNodeLinks
	newFrontmatter := core.FormatFrontMatter(*fm)
	var newContent string
	if body != "" {
		newContent = newFrontmatter + "\n" + body
	} else {
		newContent = newFrontmatter + "\n"
	}

	writeErr := p.write(func(db *sql.DB) error {
		_, err := db.ExecContext(ctx,
			`INSERT INTO note_content(urn, content) VALUES(?, ?)
			 ON CONFLICT(urn) DO UPDATE SET content = excluded.content`,
			sourceURN, newContent,
		)
		return err
	})
	return writeErr == nil
}

// ── Bulk-delete helpers ───────────────────────────────────────────────────────

func (p *Provider) deleteAllAnchorsForNote(ctx context.Context, noteURN string) {
	if err := p.write(func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, `DELETE FROM anchors WHERE note_urn = ?`, noteURN)
		return err
	}); err != nil {
		slog.Warn("link_sync: delete all anchors failed", "note_urn", noteURN, "err", err)
	}
}

func (p *Provider) deleteAllOutboundLinksForNote(ctx context.Context, noteURN string) {
	if err := p.write(func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, `DELETE FROM backlinks WHERE source_urn = ?`, noteURN)
		return err
	}); err != nil {
		slog.Warn("link_sync: delete all outbound backlinks failed", "note_urn", noteURN, "err", err)
	}
}

func (p *Provider) deleteAllExternalLinksForNote(ctx context.Context, noteURN string) {
	if err := p.write(func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, `DELETE FROM external_links WHERE source_urn = ?`, noteURN)
		return err
	}); err != nil {
		slog.Warn("link_sync: delete all external links failed", "note_urn", noteURN, "err", err)
	}
}
