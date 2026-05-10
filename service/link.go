package service

import (
	"context"
	"fmt"
	"time"

	"github.com/zebaqui/notx-engine/repo"
)

// ─────────────────────────────────────────────────────────────────────────────
// LinkService interface
// ─────────────────────────────────────────────────────────────────────────────

// LinkService defines the business-logic contract for link graph operations:
// anchors, backlinks (internal), and external links.
type LinkService interface {
	// ── Anchors ───────────────────────────────────────────────────────────────

	// UpsertAnchor creates or updates an anchor on a note.
	// Status defaults to "ok" if empty. UpdatedAt is always set to now.
	// Returns the stored record as it exists after the upsert.
	UpsertAnchor(ctx context.Context, record repo.AnchorRecord) (repo.AnchorRecord, error)

	// DeleteAnchor removes an anchor from a note.
	DeleteAnchor(ctx context.Context, noteURN, anchorID string, tombstone bool) error

	// GetAnchor returns the anchor identified by noteURN + anchorID.
	GetAnchor(ctx context.Context, noteURN, anchorID string) (repo.AnchorRecord, error)

	// ListAnchors returns all anchors declared on the given note.
	ListAnchors(ctx context.Context, noteURN string) ([]repo.AnchorRecord, error)

	// ── Backlinks ─────────────────────────────────────────────────────────────

	// UpsertBacklink creates or updates a backlink from source to target.
	// CreatedAt is set to now when zero.
	UpsertBacklink(ctx context.Context, record repo.BacklinkRecord) (repo.BacklinkRecord, error)

	// DeleteBacklink removes the backlink from sourceURN to targetURN.
	DeleteBacklink(ctx context.Context, sourceURN, targetURN, targetAnchor string) error

	// ListBacklinks returns all backlinks pointing at targetURN.
	// anchorID, when non-empty, filters to links targeting a specific anchor.
	ListBacklinks(ctx context.Context, targetURN, anchorID string) ([]repo.BacklinkRecord, error)

	// ListOutboundLinks returns all backlinks originating from sourceURN.
	ListOutboundLinks(ctx context.Context, sourceURN string) ([]repo.BacklinkRecord, error)

	// GetReferrers returns the URNs of all notes that link to targetURN.
	GetReferrers(ctx context.Context, targetURN, anchorID string) ([]string, error)

	// RecentBacklinks returns recently created backlinks with optional filters.
	RecentBacklinks(ctx context.Context, opts repo.RecentBacklinksOptions) ([]repo.BacklinkRecord, error)

	// RelabelLinks finds all source notes whose frontmatter `links:` block has an
	// entry label==oldLabel pointing to targetURN, and renames that label to
	// newLabel. Returns the list of source note URNs that were updated.
	RelabelLinks(ctx context.Context, targetURN, oldLabel, newLabel string) ([]string, error)

	// ── External links ────────────────────────────────────────────────────────

	// UpsertExternalLink creates or updates an external link from a note to a URI.
	// CreatedAt is set to now when zero.
	UpsertExternalLink(ctx context.Context, record repo.ExternalLinkRecord) (repo.ExternalLinkRecord, error)

	// DeleteExternalLink removes the external link from sourceURN to uri.
	DeleteExternalLink(ctx context.Context, sourceURN, uri string) error

	// ListExternalLinks returns all external links originating from sourceURN.
	ListExternalLinks(ctx context.Context, sourceURN string) ([]repo.ExternalLinkRecord, error)
}

// ─────────────────────────────────────────────────────────────────────────────
// linkService — concrete implementation
// ─────────────────────────────────────────────────────────────────────────────

type linkService struct {
	repo repo.LinkRepository
}

func newLinkService(r repo.LinkRepository) *linkService {
	return &linkService{repo: r}
}

// ── Anchors ───────────────────────────────────────────────────────────────────

func (s *linkService) UpsertAnchor(ctx context.Context, record repo.AnchorRecord) (repo.AnchorRecord, error) {
	if record.NoteURN == "" {
		return repo.AnchorRecord{}, fmt.Errorf("%w: NoteURN is required", ErrInvalidInput)
	}
	if record.AnchorID == "" {
		return repo.AnchorRecord{}, fmt.Errorf("%w: AnchorID is required", ErrInvalidInput)
	}
	if record.Status == "" {
		record.Status = "ok"
	}
	record.UpdatedAt = time.Now().UTC()

	if err := s.repo.UpsertAnchor(ctx, record); err != nil {
		return repo.AnchorRecord{}, err
	}

	return s.repo.GetAnchor(ctx, record.NoteURN, record.AnchorID)
}

func (s *linkService) DeleteAnchor(ctx context.Context, noteURN, anchorID string, tombstone bool) error {
	if noteURN == "" {
		return fmt.Errorf("%w: noteURN is required", ErrInvalidInput)
	}
	if anchorID == "" {
		return fmt.Errorf("%w: anchorID is required", ErrInvalidInput)
	}
	return s.repo.DeleteAnchor(ctx, noteURN, anchorID, tombstone)
}

func (s *linkService) GetAnchor(ctx context.Context, noteURN, anchorID string) (repo.AnchorRecord, error) {
	if noteURN == "" {
		return repo.AnchorRecord{}, fmt.Errorf("%w: noteURN is required", ErrInvalidInput)
	}
	if anchorID == "" {
		return repo.AnchorRecord{}, fmt.Errorf("%w: anchorID is required", ErrInvalidInput)
	}
	return s.repo.GetAnchor(ctx, noteURN, anchorID)
}

func (s *linkService) ListAnchors(ctx context.Context, noteURN string) ([]repo.AnchorRecord, error) {
	if noteURN == "" {
		return nil, fmt.Errorf("%w: noteURN is required", ErrInvalidInput)
	}
	return s.repo.ListAnchors(ctx, noteURN)
}

// ── Backlinks ─────────────────────────────────────────────────────────────────

func (s *linkService) UpsertBacklink(ctx context.Context, record repo.BacklinkRecord) (repo.BacklinkRecord, error) {
	if record.SourceURN == "" {
		return repo.BacklinkRecord{}, fmt.Errorf("%w: SourceURN is required", ErrInvalidInput)
	}
	if record.TargetURN == "" {
		return repo.BacklinkRecord{}, fmt.Errorf("%w: TargetURN is required", ErrInvalidInput)
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}

	if err := s.repo.UpsertBacklink(ctx, record); err != nil {
		return repo.BacklinkRecord{}, err
	}
	return record, nil
}

func (s *linkService) DeleteBacklink(ctx context.Context, sourceURN, targetURN, targetAnchor string) error {
	if sourceURN == "" {
		return fmt.Errorf("%w: sourceURN is required", ErrInvalidInput)
	}
	if targetURN == "" {
		return fmt.Errorf("%w: targetURN is required", ErrInvalidInput)
	}
	return s.repo.DeleteBacklink(ctx, sourceURN, targetURN, targetAnchor)
}

func (s *linkService) ListBacklinks(ctx context.Context, targetURN, anchorID string) ([]repo.BacklinkRecord, error) {
	if targetURN == "" {
		return nil, fmt.Errorf("%w: targetURN is required", ErrInvalidInput)
	}
	return s.repo.ListBacklinks(ctx, targetURN, anchorID)
}

func (s *linkService) ListOutboundLinks(ctx context.Context, sourceURN string) ([]repo.BacklinkRecord, error) {
	if sourceURN == "" {
		return nil, fmt.Errorf("%w: sourceURN is required", ErrInvalidInput)
	}
	return s.repo.ListOutboundLinks(ctx, sourceURN)
}

func (s *linkService) GetReferrers(ctx context.Context, targetURN, anchorID string) ([]string, error) {
	if targetURN == "" {
		return nil, fmt.Errorf("%w: targetURN is required", ErrInvalidInput)
	}
	return s.repo.GetReferrers(ctx, targetURN, anchorID)
}

func (s *linkService) RecentBacklinks(ctx context.Context, opts repo.RecentBacklinksOptions) ([]repo.BacklinkRecord, error) {
	return s.repo.RecentBacklinks(ctx, opts)
}

func (s *linkService) RelabelLinks(ctx context.Context, targetURN, oldLabel, newLabel string) ([]string, error) {
	if targetURN == "" {
		return nil, fmt.Errorf("%w: targetURN is required", ErrInvalidInput)
	}
	if oldLabel == "" {
		return nil, fmt.Errorf("%w: oldLabel is required", ErrInvalidInput)
	}
	if newLabel == "" {
		return nil, fmt.Errorf("%w: newLabel is required", ErrInvalidInput)
	}
	return s.repo.RelabelLinks(ctx, targetURN, oldLabel, newLabel)
}

// ── External links ────────────────────────────────────────────────────────────

func (s *linkService) UpsertExternalLink(ctx context.Context, record repo.ExternalLinkRecord) (repo.ExternalLinkRecord, error) {
	if record.SourceURN == "" {
		return repo.ExternalLinkRecord{}, fmt.Errorf("%w: SourceURN is required", ErrInvalidInput)
	}
	if record.URI == "" {
		return repo.ExternalLinkRecord{}, fmt.Errorf("%w: URI is required", ErrInvalidInput)
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}

	if err := s.repo.UpsertExternalLink(ctx, record); err != nil {
		return repo.ExternalLinkRecord{}, err
	}
	return record, nil
}

func (s *linkService) DeleteExternalLink(ctx context.Context, sourceURN, uri string) error {
	if sourceURN == "" {
		return fmt.Errorf("%w: sourceURN is required", ErrInvalidInput)
	}
	if uri == "" {
		return fmt.Errorf("%w: uri is required", ErrInvalidInput)
	}
	return s.repo.DeleteExternalLink(ctx, sourceURN, uri)
}

func (s *linkService) ListExternalLinks(ctx context.Context, sourceURN string) ([]repo.ExternalLinkRecord, error) {
	if sourceURN == "" {
		return nil, fmt.Errorf("%w: sourceURN is required", ErrInvalidInput)
	}
	return s.repo.ListExternalLinks(ctx, sourceURN)
}
