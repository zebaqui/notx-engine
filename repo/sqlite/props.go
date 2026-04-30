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

// ── ListPropSchemas ──────────────────────────────────────────────────────────

func (p *Provider) ListPropSchemas(ctx context.Context) ([]repo.PropSchema, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, name, key, type, options, position, created_at, updated_at
		FROM prop_schemas
		ORDER BY position ASC, created_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list prop schemas: %w", err)
	}
	defer rows.Close()

	var schemas []repo.PropSchema
	for rows.Next() {
		var s repo.PropSchema
		var optionsJSON string
		var createdMs, updatedMs int64
		if err := rows.Scan(
			&s.ID, &s.Name, &s.Key, &s.Type,
			&optionsJSON, &s.Position, &createdMs, &updatedMs,
		); err != nil {
			return nil, fmt.Errorf("sqlite: scan prop schema: %w", err)
		}
		_ = json.Unmarshal([]byte(optionsJSON), &s.Options)
		s.CreatedAt = fromMs(createdMs)
		s.UpdatedAt = fromMs(updatedMs)
		schemas = append(schemas, s)
	}
	return schemas, rows.Err()
}

// ── CreatePropSchema ─────────────────────────────────────────────────────────

func (p *Provider) CreatePropSchema(ctx context.Context, s *repo.PropSchema) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	s.CreatedAt = now
	s.UpdatedAt = now

	optionsJSON, err := json.Marshal(s.Options)
	if err != nil || s.Options == nil {
		optionsJSON = []byte("[]")
	}

	return p.write(func(db *sql.DB) error {
		_, err := db.Exec(`
			INSERT INTO prop_schemas (id, name, key, type, options, position, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			s.ID, s.Name, s.Key, s.Type,
			string(optionsJSON), s.Position,
			toMs(s.CreatedAt), toMs(s.UpdatedAt),
		)
		if err != nil {
			if isSQLiteConstraintUnique(err) {
				return fmt.Errorf("%w: key %q already exists", repo.ErrAlreadyExists, s.Key)
			}
			return fmt.Errorf("sqlite: create prop schema: %w", err)
		}
		return nil
	})
}

// ── UpdatePropSchema ─────────────────────────────────────────────────────────

func (p *Provider) UpdatePropSchema(ctx context.Context, s *repo.PropSchema) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.UpdatedAt = time.Now().UTC()

	optionsJSON, err := json.Marshal(s.Options)
	if err != nil || s.Options == nil {
		optionsJSON = []byte("[]")
	}

	return p.write(func(db *sql.DB) error {
		res, err := db.Exec(`
			UPDATE prop_schemas
			SET name=?, key=?, type=?, options=?, position=?, updated_at=?
			WHERE id=?`,
			s.Name, s.Key, s.Type,
			string(optionsJSON), s.Position,
			toMs(s.UpdatedAt), s.ID,
		)
		if err != nil {
			if isSQLiteConstraintUnique(err) {
				return fmt.Errorf("%w: key %q already exists", repo.ErrAlreadyExists, s.Key)
			}
			return fmt.Errorf("sqlite: update prop schema: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("%w: %s", repo.ErrNotFound, s.ID)
		}
		return nil
	})
}

// ── DeletePropSchema ─────────────────────────────────────────────────────────

func (p *Provider) DeletePropSchema(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.write(func(db *sql.DB) error {
		res, err := db.Exec(`DELETE FROM prop_schemas WHERE id=?`, id)
		if err != nil {
			return fmt.Errorf("sqlite: delete prop schema: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("%w: %s", repo.ErrNotFound, id)
		}
		return nil
	})
}
