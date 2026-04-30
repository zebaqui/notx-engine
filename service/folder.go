package service

import (
	"context"
	"fmt"
	"time"

	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/repo"
)

// ─────────────────────────────────────────────────────────────────────────────
// FolderService interface
// ─────────────────────────────────────────────────────────────────────────────

// FolderService defines the business-logic contract for folder operations.
// Folders are sub-groupings that live inside a Project.
type FolderService interface {
	// Create persists a new folder.
	// folder.URN, folder.ProjectURN, and folder.Name must be set.
	// CreatedAt and UpdatedAt are set to the current time if zero.
	Create(ctx context.Context, folder *core.Folder) error

	// Get returns the folder identified by urn.
	Get(ctx context.Context, urn string) (*core.Folder, error)

	// List returns a filtered, paginated list of folders.
	List(ctx context.Context, opts repo.FolderListOptions) (*repo.FolderListResult, error)

	// Update applies partial mutations to an existing folder.
	Update(ctx context.Context, urn string, upd FolderUpdate) (*core.Folder, error)

	// Delete soft-deletes the folder identified by urn.
	Delete(ctx context.Context, urn string) error
}

// FolderUpdate carries the optional fields to change on an existing folder.
// Nil pointer fields are treated as "no change".
type FolderUpdate struct {
	// Name, when non-empty, replaces the folder's current name.
	Name string

	// Description, when non-nil, replaces the folder's description.
	// Use a pointer to empty string to explicitly clear the description.
	Description *string

	// Deleted, when non-nil, sets the folder's soft-delete flag.
	Deleted *bool
}

// ─────────────────────────────────────────────────────────────────────────────
// folderService — concrete implementation
// ─────────────────────────────────────────────────────────────────────────────

type folderService struct {
	repo        repo.ProjectRepository
	defaultPage int
	maxPage     int
}

func newFolderService(r repo.ProjectRepository, defaultPage, maxPage int) *folderService {
	dp, mx := resolvePageDefaults(defaultPage, maxPage)
	return &folderService{repo: r, defaultPage: dp, maxPage: mx}
}

func (s *folderService) Create(ctx context.Context, folder *core.Folder) error {
	if folder == nil {
		return fmt.Errorf("%w: folder is required", ErrInvalidInput)
	}
	if folder.URN == (core.URN{}) {
		return fmt.Errorf("%w: folder.URN is required", ErrInvalidInput)
	}
	if folder.ProjectURN == (core.URN{}) {
		return fmt.Errorf("%w: folder.ProjectURN is required", ErrInvalidInput)
	}
	if folder.Name == "" {
		return fmt.Errorf("%w: folder.Name is required", ErrInvalidInput)
	}

	now := time.Now().UTC()
	if folder.CreatedAt.IsZero() {
		folder.CreatedAt = now
	}
	folder.UpdatedAt = now

	return s.repo.CreateFolder(ctx, folder)
}

func (s *folderService) Get(ctx context.Context, urn string) (*core.Folder, error) {
	if urn == "" {
		return nil, fmt.Errorf("%w: urn is required", ErrInvalidInput)
	}
	return s.repo.GetFolder(ctx, urn)
}

func (s *folderService) List(ctx context.Context, opts repo.FolderListOptions) (*repo.FolderListResult, error) {
	opts.PageSize = clampPageSize(opts.PageSize, s.defaultPage, s.maxPage)
	return s.repo.ListFolders(ctx, opts)
}

func (s *folderService) Update(ctx context.Context, urn string, upd FolderUpdate) (*core.Folder, error) {
	if urn == "" {
		return nil, fmt.Errorf("%w: urn is required", ErrInvalidInput)
	}

	folder, err := s.repo.GetFolder(ctx, urn)
	if err != nil {
		return nil, err
	}

	if upd.Name != "" {
		folder.Name = upd.Name
	}
	if upd.Description != nil {
		folder.Description = *upd.Description
	}
	if upd.Deleted != nil {
		folder.Deleted = *upd.Deleted
	}

	folder.UpdatedAt = time.Now().UTC()

	if err := s.repo.UpdateFolder(ctx, folder); err != nil {
		return nil, err
	}
	return folder, nil
}

func (s *folderService) Delete(ctx context.Context, urn string) error {
	if urn == "" {
		return fmt.Errorf("%w: urn is required", ErrInvalidInput)
	}
	return s.repo.DeleteFolder(ctx, urn)
}
