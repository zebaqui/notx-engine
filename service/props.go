package service

import (
	"context"
	"fmt"
	"time"

	"github.com/zebaqui/notx-engine/repo"
)

// ─────────────────────────────────────────────────────────────────────────────
// PropService interface
// ─────────────────────────────────────────────────────────────────────────────

// validPropTypes enumerates the allowed values for PropSchema.Type.
var validPropTypes = map[string]bool{
	"free":  true,
	"multi": true,
	"date":  true,
	"enum":  true,
}

// PropService defines the business-logic contract for prop schema management.
// Prop schemas define custom front-matter field definitions for notes.
type PropService interface {
	// List returns all prop schema definitions.
	List(ctx context.Context) ([]repo.PropSchema, error)

	// Get returns the prop schema identified by id.
	// Returns an error wrapping repo.ErrNotFound when no schema has that id.
	Get(ctx context.Context, id string) (repo.PropSchema, error)

	// Create persists a new prop schema. Name and Key are required.
	// Type defaults to "free" when empty. CreatedAt and UpdatedAt are set to now.
	Create(ctx context.Context, s *repo.PropSchema) error

	// Update persists changes to an existing prop schema.
	// UpdatedAt is always set to now.
	Update(ctx context.Context, s *repo.PropSchema) error

	// Delete removes the prop schema identified by id.
	Delete(ctx context.Context, id string) error
}

// ─────────────────────────────────────────────────────────────────────────────
// propService — concrete implementation
// ─────────────────────────────────────────────────────────────────────────────

type propService struct {
	repo repo.PropSchemaRepo
}

func newPropService(r repo.PropSchemaRepo) *propService {
	return &propService{repo: r}
}

func (s *propService) List(ctx context.Context) ([]repo.PropSchema, error) {
	return s.repo.ListPropSchemas(ctx)
}

func (s *propService) Get(ctx context.Context, id string) (repo.PropSchema, error) {
	if id == "" {
		return repo.PropSchema{}, fmt.Errorf("%w: id is required", ErrInvalidInput)
	}

	schemas, err := s.repo.ListPropSchemas(ctx)
	if err != nil {
		return repo.PropSchema{}, err
	}
	for _, sc := range schemas {
		if sc.ID == id {
			return sc, nil
		}
	}
	return repo.PropSchema{}, fmt.Errorf("prop schema %q: %w", id, repo.ErrNotFound)
}

func (s *propService) Create(ctx context.Context, sc *repo.PropSchema) error {
	if sc == nil {
		return fmt.Errorf("%w: schema is required", ErrInvalidInput)
	}
	if sc.Name == "" {
		return fmt.Errorf("%w: schema.Name is required", ErrInvalidInput)
	}
	if sc.Key == "" {
		return fmt.Errorf("%w: schema.Key is required", ErrInvalidInput)
	}
	if sc.Type == "" {
		sc.Type = "free"
	}
	if !validPropTypes[sc.Type] {
		return fmt.Errorf("%w: schema.Type must be one of: free, multi, date, enum", ErrInvalidInput)
	}

	now := time.Now().UTC()
	sc.CreatedAt = now
	sc.UpdatedAt = now

	return s.repo.CreatePropSchema(ctx, sc)
}

func (s *propService) Update(ctx context.Context, sc *repo.PropSchema) error {
	if sc == nil {
		return fmt.Errorf("%w: schema is required", ErrInvalidInput)
	}
	if sc.ID == "" {
		return fmt.Errorf("%w: schema.ID is required", ErrInvalidInput)
	}
	if sc.Type != "" && !validPropTypes[sc.Type] {
		return fmt.Errorf("%w: schema.Type must be one of: free, multi, date, enum", ErrInvalidInput)
	}

	sc.UpdatedAt = time.Now().UTC()
	return s.repo.UpdatePropSchema(ctx, sc)
}

func (s *propService) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidInput)
	}
	return s.repo.DeletePropSchema(ctx, id)
}
