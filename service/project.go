package service

import (
	"context"
	"fmt"
	"time"

	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/repo"
)

// ─────────────────────────────────────────────────────────────────────────────
// ProjectService interface
// ─────────────────────────────────────────────────────────────────────────────

// ProjectService defines the business-logic contract for project operations.
type ProjectService interface {
	// Create persists a new project. proj.URN and proj.Name must be set.
	// CreatedAt and UpdatedAt are set to the current time if zero.
	Create(ctx context.Context, proj *core.Project) error

	// Get returns the project identified by urn.
	Get(ctx context.Context, urn string) (*core.Project, error)

	// List returns a filtered, paginated list of projects.
	List(ctx context.Context, opts repo.ProjectListOptions) (*repo.ProjectListResult, error)

	// Update applies partial mutations to an existing project.
	Update(ctx context.Context, urn string, upd ProjectUpdate) (*core.Project, error)

	// Delete soft-deletes the project identified by urn.
	Delete(ctx context.Context, urn string) error
}

// ProjectUpdate carries the optional fields to change on an existing project.
// Nil pointer fields are treated as "no change".
type ProjectUpdate struct {
	// Name, when non-empty, replaces the project's current name.
	Name string

	// Description, when non-nil, replaces the project's description.
	// Use a pointer to empty string to explicitly clear the description.
	Description *string

	// Deleted, when non-nil, sets the project's soft-delete flag.
	Deleted *bool
}

// ─────────────────────────────────────────────────────────────────────────────
// projectService — concrete implementation
// ─────────────────────────────────────────────────────────────────────────────

type projectService struct {
	repo        repo.ProjectRepository
	defaultPage int
	maxPage     int
}

func newProjectService(r repo.ProjectRepository, defaultPage, maxPage int) *projectService {
	dp, mx := resolvePageDefaults(defaultPage, maxPage)
	return &projectService{repo: r, defaultPage: dp, maxPage: mx}
}

func (s *projectService) Create(ctx context.Context, proj *core.Project) error {
	if proj == nil {
		return fmt.Errorf("%w: project is required", ErrInvalidInput)
	}
	if proj.URN == (core.URN{}) {
		return fmt.Errorf("%w: project.URN is required", ErrInvalidInput)
	}
	if proj.Name == "" {
		return fmt.Errorf("%w: project.Name is required", ErrInvalidInput)
	}

	now := time.Now().UTC()
	if proj.CreatedAt.IsZero() {
		proj.CreatedAt = now
	}
	proj.UpdatedAt = now

	return s.repo.CreateProject(ctx, proj)
}

func (s *projectService) Get(ctx context.Context, urn string) (*core.Project, error) {
	if urn == "" {
		return nil, fmt.Errorf("%w: urn is required", ErrInvalidInput)
	}
	return s.repo.GetProject(ctx, urn)
}

func (s *projectService) List(ctx context.Context, opts repo.ProjectListOptions) (*repo.ProjectListResult, error) {
	opts.PageSize = clampPageSize(opts.PageSize, s.defaultPage, s.maxPage)
	return s.repo.ListProjects(ctx, opts)
}

func (s *projectService) Update(ctx context.Context, urn string, upd ProjectUpdate) (*core.Project, error) {
	if urn == "" {
		return nil, fmt.Errorf("%w: urn is required", ErrInvalidInput)
	}

	proj, err := s.repo.GetProject(ctx, urn)
	if err != nil {
		return nil, err
	}

	if upd.Name != "" {
		proj.Name = upd.Name
	}
	if upd.Description != nil {
		proj.Description = *upd.Description
	}
	if upd.Deleted != nil {
		proj.Deleted = *upd.Deleted
	}

	proj.UpdatedAt = time.Now().UTC()

	if err := s.repo.UpdateProject(ctx, proj); err != nil {
		return nil, err
	}
	return proj, nil
}

func (s *projectService) Delete(ctx context.Context, urn string) error {
	if urn == "" {
		return fmt.Errorf("%w: urn is required", ErrInvalidInput)
	}
	return s.repo.DeleteProject(ctx, urn)
}
