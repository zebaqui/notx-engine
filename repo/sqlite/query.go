package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/repo"
)

// ─────────────────────────────────────────────────────────────────────────────
// Note queries
// ─────────────────────────────────────────────────────────────────────────────

// queryNotes executes a filtered, paginated SELECT on the notes table.
// Results are ordered by (updated_at DESC, urn ASC).
func queryNotes(ctx context.Context, db *sql.DB, opts repo.ListOptions) ([]*core.Note, string, error) {
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}

	cursorMs, cursorURN, err := decodeCursor(opts.PageToken)
	if err != nil {
		return nil, "", err
	}

	var conditions []string
	var args []interface{}

	if !opts.IncludeDeleted {
		conditions = append(conditions, "deleted = 0")
	}
	if opts.ProjectURN != "" {
		conditions = append(conditions, "project_urn = ?")
		args = append(args, opts.ProjectURN)
	}
	if opts.FolderURN != "" {
		conditions = append(conditions, "folder_urn = ?")
		args = append(args, opts.FolderURN)
	}
	if opts.FilterByType {
		conditions = append(conditions, "note_type = ?")
		args = append(args, opts.NoteTypeFilter.String())
	}
	// Exclude snips — regular list never returns snip-typed notes.
	conditions = append(conditions, "snip_type IS NULL")

	if cursorMs > 0 {
		conditions = append(conditions, "(updated_at < ? OR (updated_at = ? AND urn > ?))")
		args = append(args, cursorMs, cursorMs, cursorURN)
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	query := fmt.Sprintf(
		`SELECT urn, project_urn, folder_urn, note_type, title, head_seq, deleted, created_at, updated_at,
		        COALESCE(snip_type, ''), COALESCE(parent_anchor, ''), COALESCE(parent_urn, '')
		 FROM notes %s ORDER BY updated_at DESC, urn ASC LIMIT ?`,
		where,
	)
	args = append(args, pageSize+1)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("sqlite: list notes: %w", err)
	}
	defer rows.Close()

	var notes []*core.Note
	for rows.Next() {
		var (
			urnStr        string
			projectURNStr string
			folderURNStr  string
			noteType      string
			title         string
			headSeq       int
			deleted       int
			createdMs     int64
			updatedMs     int64
			snipType      string
			parentAnchor  string
			parentURN     string
		)
		if err := rows.Scan(&urnStr, &projectURNStr, &folderURNStr, &noteType, &title,
			&headSeq, &deleted, &createdMs, &updatedMs,
			&snipType, &parentAnchor, &parentURN); err != nil {
			return nil, "", fmt.Errorf("sqlite: scan note row: %w", err)
		}
		n, err := noteFromRow(urnStr, projectURNStr, folderURNStr, noteType, title,
			headSeq, deleted == 1, createdMs, updatedMs, snipType, parentAnchor, parentURN)
		if err != nil {
			return nil, "", err
		}
		notes = append(notes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("sqlite: iterate notes: %w", err)
	}

	var nextToken string
	if len(notes) > pageSize {
		last := notes[pageSize-1]
		nextToken = encodeCursor(last.UpdatedAt.UnixMilli(), last.URN.String())
		notes = notes[:pageSize]
	}
	if notes == nil {
		notes = []*core.Note{}
	}
	return notes, nextToken, nil
}

// noteFromRow reconstructs a core.Note from flat column values.
// Only metadata fields are populated — the event stream is empty.
func noteFromRow(urnStr, projectURNStr, folderURNStr, noteType, title string,
	headSeq int, deleted bool, createdMs, updatedMs int64,
	snipType, parentAnchor, parentURN string) (*core.Note, error) {

	urn, err := core.ParseURN(urnStr)
	if err != nil {
		return nil, fmt.Errorf("sqlite: parse note urn %q: %w", urnStr, err)
	}

	parsedNoteType, err := core.ParseNoteType(noteType)
	if err != nil {
		return nil, fmt.Errorf("sqlite: parse note_type %q: %w", noteType, err)
	}
	n := &core.Note{
		URN:       urn,
		Name:      title,
		NoteType:  parsedNoteType,
		Deleted:   deleted,
		CreatedAt: time.UnixMilli(createdMs).UTC(),
		UpdatedAt: time.UnixMilli(updatedMs).UTC(),
	}
	// headSeq is read from the DB but not stored on core.Note directly;
	// it is available via n.HeadSequence() after events are loaded.
	_ = headSeq

	if projectURNStr != "" {
		u, err := core.ParseURN(projectURNStr)
		if err != nil {
			return nil, fmt.Errorf("sqlite: parse note project_urn %q: %w", projectURNStr, err)
		}
		n.ProjectURN = &u
	}
	if folderURNStr != "" {
		u, err := core.ParseURN(folderURNStr)
		if err != nil {
			return nil, fmt.Errorf("sqlite: parse note folder_urn %q: %w", folderURNStr, err)
		}
		n.FolderURN = &u
	}
	if snipType != "" {
		s := snipType
		n.SnipType = &s
	}
	if parentAnchor != "" {
		a := parentAnchor
		n.ParentAnchor = &a
	}
	if parentURN != "" {
		u, err := core.ParseURN(parentURN)
		if err != nil {
			return nil, fmt.Errorf("sqlite: parse note parent_urn %q: %w", parentURN, err)
		}
		n.ParentURN = &u
	}
	return n, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Snip queries
// ─────────────────────────────────────────────────────────────────────────────

// querySnips executes a filtered, paginated SELECT on the notes table,
// returning only rows where snip_type IS NOT NULL.
func querySnips(ctx context.Context, db *sql.DB, opts repo.ListSnipsOptions) ([]*core.Note, string, error) {
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}

	cursorMs, cursorURN, err := decodeCursor(opts.PageToken)
	if err != nil {
		return nil, "", err
	}

	// snip_type IS NOT NULL is the mandatory discriminator.
	conditions := []string{"snip_type IS NOT NULL"}
	var args []interface{}

	if !opts.IncludeDeleted {
		conditions = append(conditions, "deleted = 0")
	}
	if opts.SnipType != "" {
		conditions = append(conditions, "snip_type = ?")
		args = append(args, opts.SnipType)
	}
	if opts.ProjectURN != "" {
		conditions = append(conditions, "project_urn = ?")
		args = append(args, opts.ProjectURN)
	}
	if opts.ParentURN != "" {
		conditions = append(conditions, "parent_urn = ?")
		args = append(args, opts.ParentURN)
	}
	if opts.ParentAnchor != "" {
		conditions = append(conditions, "parent_anchor = ?")
		args = append(args, opts.ParentAnchor)
	}
	if cursorMs > 0 {
		conditions = append(conditions, "(updated_at < ? OR (updated_at = ? AND urn > ?))")
		args = append(args, cursorMs, cursorMs, cursorURN)
	}

	where := "WHERE " + strings.Join(conditions, " AND ")

	query := fmt.Sprintf(
		`SELECT urn, project_urn, folder_urn, note_type, title, head_seq, deleted, created_at, updated_at,
		        COALESCE(snip_type, ''), COALESCE(parent_anchor, ''), COALESCE(parent_urn, '')
		 FROM notes %s ORDER BY updated_at DESC, urn ASC LIMIT ?`,
		where,
	)
	args = append(args, pageSize+1)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("sqlite: list snips: %w", err)
	}
	defer rows.Close()

	var notes []*core.Note
	for rows.Next() {
		var (
			urnStr        string
			projectURNStr string
			folderURNStr  string
			noteType      string
			title         string
			headSeq       int
			deleted       int
			createdMs     int64
			updatedMs     int64
			snipType      string
			parentAnchor  string
			parentURN     string
		)
		if err := rows.Scan(&urnStr, &projectURNStr, &folderURNStr, &noteType, &title,
			&headSeq, &deleted, &createdMs, &updatedMs,
			&snipType, &parentAnchor, &parentURN); err != nil {
			return nil, "", fmt.Errorf("sqlite: scan snip row: %w", err)
		}
		n, err := noteFromRow(urnStr, projectURNStr, folderURNStr, noteType, title,
			headSeq, deleted == 1, createdMs, updatedMs, snipType, parentAnchor, parentURN)
		if err != nil {
			return nil, "", err
		}
		notes = append(notes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("sqlite: iterate snips: %w", err)
	}

	var nextToken string
	if len(notes) > pageSize {
		last := notes[pageSize-1]
		nextToken = encodeCursor(last.UpdatedAt.UnixMilli(), last.URN.String())
		notes = notes[:pageSize]
	}
	if notes == nil {
		notes = []*core.Note{}
	}
	return notes, nextToken, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// FTS search
// ─────────────────────────────────────────────────────────────────────────────

// searchNotes executes an FTS5 query and returns matched note headers with
// snippet excerpts. Only normal notes are ever in the FTS index.
func searchNotes(ctx context.Context, db *sql.DB, opts repo.SearchOptions) (*repo.SearchResults, error) {
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}

	// FTS5 snippet() generates an excerpt; column index 1 targets the body column.
	query := `
		SELECT n.urn, n.project_urn, n.folder_urn, n.note_type, n.title,
		       n.head_seq, n.deleted, n.created_at, n.updated_at,
		       snippet(notes_fts, 1, '[', ']', '...', 10) AS excerpt
		FROM notes_fts
		JOIN notes n ON notes_fts.urn = n.urn
		WHERE notes_fts MATCH ?
		  AND n.note_type = 'normal'
		  AND n.deleted = 0
		ORDER BY rank
		LIMIT ?`

	rows, err := db.QueryContext(ctx, query, opts.Query, pageSize)
	if err != nil {
		return nil, fmt.Errorf("sqlite: search notes: %w", err)
	}
	defer rows.Close()

	var results []*repo.SearchResult
	for rows.Next() {
		var (
			urnStr        string
			projectURNStr string
			folderURNStr  string
			noteType      string
			title         string
			headSeq       int
			deleted       int
			createdMs     int64
			updatedMs     int64
			excerpt       string
		)
		if err := rows.Scan(&urnStr, &projectURNStr, &folderURNStr, &noteType, &title,
			&headSeq, &deleted, &createdMs, &updatedMs, &excerpt); err != nil {
			return nil, fmt.Errorf("sqlite: scan search row: %w", err)
		}
		n, err := noteFromRow(urnStr, projectURNStr, folderURNStr, noteType, title,
			headSeq, deleted == 1, createdMs, updatedMs, "", "", "")
		if err != nil {
			return nil, err
		}
		results = append(results, &repo.SearchResult{
			Note:    n,
			Excerpt: excerpt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterate search: %w", err)
	}
	if results == nil {
		results = []*repo.SearchResult{}
	}
	return &repo.SearchResults{Results: results}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Project queries
// ─────────────────────────────────────────────────────────────────────────────

func queryProject(ctx context.Context, db *sql.DB, urn string) (*core.Project, error) {
	var (
		name      string
		deleted   int
		createdMs int64
		updatedMs int64
	)
	err := db.QueryRowContext(ctx,
		`SELECT name, deleted, created_at, updated_at FROM projects WHERE urn = ?`, urn,
	).Scan(&name, &deleted, &createdMs, &updatedMs)
	if err == sql.ErrNoRows {
		return nil, repo.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get project: %w", err)
	}
	parsedURN, err := core.ParseURN(urn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: parse project urn %q: %w", urn, err)
	}
	return &core.Project{
		URN:       parsedURN,
		Name:      name,
		Deleted:   deleted == 1,
		CreatedAt: time.UnixMilli(createdMs).UTC(),
		UpdatedAt: time.UnixMilli(updatedMs).UTC(),
	}, nil
}

func queryProjects(ctx context.Context, db *sql.DB, opts repo.ProjectListOptions) (*repo.ProjectListResult, error) {
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 100
	}

	var conditions []string
	var args []interface{}
	if !opts.IncludeDeleted {
		conditions = append(conditions, "deleted = 0")
	}

	cursorMs, cursorURN, err := decodeCursor(opts.PageToken)
	if err != nil {
		return nil, err
	}
	if cursorMs > 0 {
		conditions = append(conditions, "(updated_at < ? OR (updated_at = ? AND urn > ?))")
		args = append(args, cursorMs, cursorMs, cursorURN)
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	q := fmt.Sprintf(
		`SELECT urn, name, deleted, created_at, updated_at FROM projects %s
		 ORDER BY updated_at DESC, urn ASC LIMIT ?`, where,
	)
	args = append(args, pageSize+1)

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list projects: %w", err)
	}
	defer rows.Close()

	var projects []*core.Project
	for rows.Next() {
		var (
			urnStr    string
			name      string
			deleted   int
			createdMs int64
			updatedMs int64
		)
		if err := rows.Scan(&urnStr, &name, &deleted, &createdMs, &updatedMs); err != nil {
			return nil, fmt.Errorf("sqlite: scan project: %w", err)
		}
		parsedURN, err := core.ParseURN(urnStr)
		if err != nil {
			return nil, fmt.Errorf("sqlite: parse project urn %q: %w", urnStr, err)
		}
		projects = append(projects, &core.Project{
			URN:       parsedURN,
			Name:      name,
			Deleted:   deleted == 1,
			CreatedAt: time.UnixMilli(createdMs).UTC(),
			UpdatedAt: time.UnixMilli(updatedMs).UTC(),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterate projects: %w", err)
	}

	var nextToken string
	if len(projects) > pageSize {
		last := projects[pageSize-1]
		nextToken = encodeCursor(last.UpdatedAt.UnixMilli(), last.URN.String())
		projects = projects[:pageSize]
	}
	if projects == nil {
		projects = []*core.Project{}
	}
	return &repo.ProjectListResult{Projects: projects, NextPageToken: nextToken}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Folder queries
// ─────────────────────────────────────────────────────────────────────────────

func queryFolder(ctx context.Context, db *sql.DB, urn string) (*core.Folder, error) {
	var (
		projectURNStr string
		name          string
		deleted       int
		createdMs     int64
		updatedMs     int64
	)
	err := db.QueryRowContext(ctx,
		`SELECT project_urn, name, deleted, created_at, updated_at FROM folders WHERE urn = ?`, urn,
	).Scan(&projectURNStr, &name, &deleted, &createdMs, &updatedMs)
	if err == sql.ErrNoRows {
		return nil, repo.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get folder: %w", err)
	}
	parsedURN, err := core.ParseURN(urn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: parse folder urn %q: %w", urn, err)
	}
	parsedProjectURN, err := core.ParseURN(projectURNStr)
	if err != nil {
		return nil, fmt.Errorf("sqlite: parse folder project_urn %q: %w", projectURNStr, err)
	}
	return &core.Folder{
		URN:        parsedURN,
		ProjectURN: parsedProjectURN,
		Name:       name,
		Deleted:    deleted == 1,
		CreatedAt:  time.UnixMilli(createdMs).UTC(),
		UpdatedAt:  time.UnixMilli(updatedMs).UTC(),
	}, nil
}

func queryFolders(ctx context.Context, db *sql.DB, opts repo.FolderListOptions) (*repo.FolderListResult, error) {
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 100
	}

	var conditions []string
	var args []interface{}
	if opts.ProjectURN != "" {
		conditions = append(conditions, "project_urn = ?")
		args = append(args, opts.ProjectURN)
	}
	if !opts.IncludeDeleted {
		conditions = append(conditions, "deleted = 0")
	}

	cursorMs, cursorURN, err := decodeCursor(opts.PageToken)
	if err != nil {
		return nil, err
	}
	if cursorMs > 0 {
		conditions = append(conditions, "(updated_at < ? OR (updated_at = ? AND urn > ?))")
		args = append(args, cursorMs, cursorMs, cursorURN)
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	q := fmt.Sprintf(
		`SELECT urn, project_urn, name, deleted, created_at, updated_at FROM folders %s
		 ORDER BY updated_at DESC, urn ASC LIMIT ?`, where,
	)
	args = append(args, pageSize+1)

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list folders: %w", err)
	}
	defer rows.Close()

	var folders []*core.Folder
	for rows.Next() {
		var (
			urnStr        string
			projectURNStr string
			name          string
			deleted       int
			createdMs     int64
			updatedMs     int64
		)
		if err := rows.Scan(&urnStr, &projectURNStr, &name, &deleted, &createdMs, &updatedMs); err != nil {
			return nil, fmt.Errorf("sqlite: scan folder: %w", err)
		}
		parsedURN, err := core.ParseURN(urnStr)
		if err != nil {
			return nil, fmt.Errorf("sqlite: parse folder urn %q: %w", urnStr, err)
		}
		parsedProjectURN, err := core.ParseURN(projectURNStr)
		if err != nil {
			return nil, fmt.Errorf("sqlite: parse folder project_urn %q: %w", projectURNStr, err)
		}
		folders = append(folders, &core.Folder{
			URN:        parsedURN,
			ProjectURN: parsedProjectURN,
			Name:       name,
			Deleted:    deleted == 1,
			CreatedAt:  time.UnixMilli(createdMs).UTC(),
			UpdatedAt:  time.UnixMilli(updatedMs).UTC(),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterate folders: %w", err)
	}

	var nextToken string
	if len(folders) > pageSize {
		last := folders[pageSize-1]
		nextToken = encodeCursor(last.UpdatedAt.UnixMilli(), last.URN.String())
		folders = folders[:pageSize]
	}
	if folders == nil {
		folders = []*core.Folder{}
	}
	return &repo.FolderListResult{Folders: folders, NextPageToken: nextToken}, nil
}
