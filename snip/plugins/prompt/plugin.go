package prompt

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/snip"
)

var promptSchema = snip.SnipSchema{
	Fields: map[string]snip.FieldSpec{
		"slug":        {Kind: snip.FieldKindString, Default: ""},
		"body":        {Kind: snip.FieldKindString, Default: ""},
		"description": {Kind: snip.FieldKindString, Default: ""},
		"tags":        {Kind: snip.FieldKindStrList, Default: nil},
	},
	IndexColumns: []snip.IndexColumn{
		{Field: "slug", SQLType: "TEXT"},
		{Field: "description", SQLType: "TEXT"},
	},
	DisplayField: "slug",
	FTSFields:    []string{"body", "description"},
}

// Plugin implements snip.SnipPlugin for the "prompt" snip type.
type Plugin struct {
	env      snip.PluginEnv
	stopOnce sync.Once
}

// New returns a new, uninitialised prompt Plugin.
func New() *Plugin {
	return &Plugin{}
}

// ── Identity ──────────────────────────────────────────────────────────────────

func (p *Plugin) Type() string    { return "prompt" }
func (p *Plugin) Version() string { return "0.1.0" }
func (p *Plugin) Description() string {
	return "Stores reusable system prompts indexed by slug"
}
func (p *Plugin) Schema() snip.SnipSchema { return promptSchema }

// ── Lifecycle ─────────────────────────────────────────────────────────────────

func (p *Plugin) Init(_ context.Context, env snip.PluginEnv) error {
	p.env = env
	return nil
}

func (p *Plugin) Start(_ context.Context) error { return nil }

func (p *Plugin) Stop(_ context.Context) error {
	p.stopOnce.Do(func() {})
	return nil
}

// ── Event callbacks ───────────────────────────────────────────────────────────

func (p *Plugin) OnNoteCreated(ctx context.Context, note *core.Note) error {
	return p.upsertFromNote(ctx, note)
}

func (p *Plugin) OnEventAppended(ctx context.Context, note *core.Note, _ *core.Event) error {
	return p.upsertFromNote(ctx, note)
}

func (p *Plugin) OnNoteDeleted(ctx context.Context, noteURN core.URN) error {
	_, err := p.env.DB.ExecContext(ctx,
		`DELETE FROM engine_snips_prompt WHERE namespace = $1 AND note_urn = $2`,
		p.env.Namespace, noteURN.String())
	return err
}

func (p *Plugin) OnParentAnchorBroken(_ context.Context, _ core.URN) error { return nil }

// ── Transport registration ────────────────────────────────────────────────────

func (p *Plugin) RegisterHTTP(mux *http.ServeMux, middleware func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("/v1/snips/prompt", middleware(p.handleRoot))
	mux.HandleFunc("/v1/snips/prompt/", middleware(p.handleSlug))
}

// ── Internal helpers ──────────────────────────────────────────────────────────

type promptFields struct {
	Slug        string `yaml:"slug"`
	Body        string `yaml:"body"`
	Description string `yaml:"description"`
}

func parsePromptContent(content string) (promptFields, error) {
	var f promptFields
	if err := yaml.Unmarshal([]byte(content), &f); err != nil {
		return f, err
	}
	return f, nil
}

func (p *Plugin) upsertFromNote(ctx context.Context, note *core.Note) error {
	f, err := parsePromptContent(note.Content())
	if err != nil || f.Slug == "" {
		return err
	}
	_, err = p.env.DB.ExecContext(ctx, `
		INSERT INTO engine_snips_prompt (namespace, note_urn, slug, body, description)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT(namespace, note_urn) DO UPDATE SET
		    slug        = excluded.slug,
		    body        = excluded.body,
		    description = excluded.description`,
		p.env.Namespace, note.URN.String(), f.Slug, f.Body, f.Description)
	return err
}

func buildYAML(slug, body, description string) string {
	// Marshal via yaml.v3 for proper escaping.
	type ordered struct {
		Slug        string `yaml:"slug"`
		Body        string `yaml:"body"`
		Description string `yaml:"description"`
	}
	out, _ := yaml.Marshal(ordered{Slug: slug, Body: body, Description: description})
	return strings.TrimRight(string(out), "\n")
}

func buildEntries(yamlContent string) []core.LineEntry {
	lines := strings.Split(yamlContent, "\n")
	entries := make([]core.LineEntry, len(lines))
	for i, line := range lines {
		entries[i] = core.LineEntry{Op: core.LineOpSet, LineNumber: i + 1, Content: line}
	}
	return entries
}

// ── Direct-SQL note helpers (no NoteRepo dependency) ─────────────────────────

// sqlCreateNote inserts a row into engine_notes directly.
// This mirrors the logic in TenantScopedProvider.createNote but uses only
// the columns guaranteed to exist (migration 9 + migration 22 for snip_type).
func (p *Plugin) sqlCreateNote(ctx context.Context, noteURN core.URN, name string, snipType string, now time.Time) error {
	const q = `
		INSERT INTO public.engine_notes
			(urn, namespace, name, note_type, snip_type, head_sequence,
			 content, node_links, deleted, created_at, updated_at)
		VALUES
			($1, $2, $3, 'normal', $4, 0, '', '{}', false, $5, $6)
		ON CONFLICT (urn) DO NOTHING`

	res, err := p.env.DB.ExecContext(ctx, q,
		noteURN.String(),
		p.env.Namespace,
		name,
		snipType,
		now.UTC(),
		now.UTC(),
	)
	if err != nil {
		return fmt.Errorf("prompt plugin: create note %s: %w", noteURN, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("prompt plugin: create note %s: rows affected: %w", noteURN, err)
	}
	if n == 0 {
		return fmt.Errorf("prompt plugin: create note %s: already exists", noteURN)
	}
	return nil
}

// sqlAppendEvent inserts a row into engine_events and updates engine_notes
// head_sequence + content atomically in a single transaction.
// It mirrors the core logic in TenantScopedProvider.appendEvent for normal notes.
func (p *Plugin) sqlAppendEvent(ctx context.Context, noteURN core.URN, sequence int, authorURN string, entries []core.LineEntry, now time.Time) error {
	tx, err := p.env.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("prompt plugin: append event: begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// Lock the note row and read current content.
	var headSeq int
	var currentContent string
	err = tx.QueryRowContext(ctx, `
		SELECT head_sequence, content
		FROM public.engine_notes
		WHERE urn = $1 AND namespace = $2
		FOR UPDATE`,
		noteURN.String(), p.env.Namespace,
	).Scan(&headSeq, &currentContent)
	if err != nil {
		return fmt.Errorf("prompt plugin: append event: lock note: %w", err)
	}

	if sequence != headSeq+1 {
		return fmt.Errorf("prompt plugin: append event: sequence conflict: want %d got %d", headSeq+1, sequence)
	}

	// Materialise the payload from the line entries.
	payload := entriesToPayload(entries)

	// Insert the event row.
	eventURN := fmt.Sprintf("evt_%s", uuid.New().String())
	_, err = tx.ExecContext(ctx, `
		INSERT INTO public.engine_events
			(urn, namespace, note_urn, sequence, author_urn, label, payload, created_at)
		VALUES
			($1, $2, $3, $4, $5, '', $6, $7)`,
		eventURN,
		p.env.Namespace,
		noteURN.String(),
		sequence,
		authorURN,
		payload,
		now.UTC(),
	)
	if err != nil {
		return fmt.Errorf("prompt plugin: append event: insert event: %w", err)
	}

	// Apply entries to existing content to get the new materialised content.
	lines := core.SplitLines(currentContent)
	lines = applyEntriesToLines(lines, entries)
	newContent := strings.Join(lines, "\n")

	// Update the note header.
	_, err = tx.ExecContext(ctx, `
		UPDATE public.engine_notes
		SET head_sequence = $1,
		    content       = $2,
		    updated_at    = $3
		WHERE urn = $4 AND namespace = $5`,
		sequence,
		newContent,
		now.UTC(),
		noteURN.String(),
		p.env.Namespace,
	)
	if err != nil {
		return fmt.Errorf("prompt plugin: append event: update note: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("prompt plugin: append event: commit: %w", err)
	}
	return nil
}

// sqlGetHeadSequence returns the current head_sequence for a note.
func (p *Plugin) sqlGetHeadSequence(ctx context.Context, noteURNStr string) (int, error) {
	var headSeq int
	err := p.env.DB.QueryRowContext(ctx,
		`SELECT head_sequence FROM public.engine_notes WHERE urn = $1 AND namespace = $2`,
		noteURNStr, p.env.Namespace,
	).Scan(&headSeq)
	if err != nil {
		return 0, fmt.Errorf("prompt plugin: get head sequence for %s: %w", noteURNStr, err)
	}
	return headSeq, nil
}

// sqlDeleteNote deletes a note row; engine_events and engine_snapshots cascade.
func (p *Plugin) sqlDeleteNote(ctx context.Context, noteURNStr string) error {
	_, err := p.env.DB.ExecContext(ctx,
		`DELETE FROM public.engine_notes WHERE urn = $1 AND namespace = $2`,
		noteURNStr, p.env.Namespace,
	)
	if err != nil {
		return fmt.Errorf("prompt plugin: delete note %s: %w", noteURNStr, err)
	}
	return nil
}

// entriesToPayload converts line entries to a flat newline-delimited string
// suitable for the engine_events.payload column. Each line is rendered as
// "SET <n> <content>" so the standard applyEntriesToLines helper can replay it.
func entriesToPayload(entries []core.LineEntry) string {
	var sb strings.Builder
	for _, e := range entries {
		switch e.Op {
		case core.LineOpSet:
			fmt.Fprintf(&sb, "SET %d %s\n", e.LineNumber, e.Content)
		case core.LineOpDelete:
			fmt.Fprintf(&sb, "DEL %d\n", e.LineNumber)
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// applyEntriesToLines applies a slice of LineEntry edits to the current lines
// of a note. It is a local copy of the same logic in the postgres provider so
// this plugin does not need to import internal packages.
func applyEntriesToLines(lines []string, entries []core.LineEntry) []string {
	for _, e := range entries {
		idx := e.LineNumber - 1
		switch e.Op {
		case core.LineOpSet:
			for len(lines) <= idx {
				lines = append(lines, "")
			}
			lines[idx] = e.Content
		case core.LineOpDelete:
			if idx >= 0 && idx < len(lines) {
				lines = append(lines[:idx], lines[idx+1:]...)
			}
		}
	}
	return lines
}

// ── HTTP handlers ─────────────────────────────────────────────────────────────

type promptItem struct {
	URN         string    `json:"urn"`
	Slug        string    `json:"slug"`
	Body        string    `json:"body"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (p *Plugin) handleRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		p.handleList(w, r)
	case http.MethodPost:
		p.handleCreate(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (p *Plugin) handleSlug(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/v1/snips/prompt/")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "slug is required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		p.handleGet(w, r, slug)
	case http.MethodPatch:
		p.handlePatch(w, r, slug)
	case http.MethodDelete:
		p.handleDelete(w, r, slug)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (p *Plugin) queryBySlug(ctx context.Context, slug string) (promptItem, error) {
	row := p.env.DB.QueryRowContext(ctx, `
		SELECT n.urn, esp.slug, esp.body, esp.description, n.created_at, n.updated_at
		FROM engine_snips_prompt esp
		JOIN public.engine_notes n ON n.urn = esp.note_urn
		WHERE esp.namespace = $1 AND esp.slug = $2`, p.env.Namespace, slug)
	var item promptItem
	err := row.Scan(&item.URN, &item.Slug, &item.Body, &item.Description, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func (p *Plugin) handleList(w http.ResponseWriter, r *http.Request) {
	rows, err := p.env.DB.QueryContext(r.Context(), `
		SELECT n.urn, esp.slug, esp.body, esp.description, n.created_at, n.updated_at
		FROM engine_snips_prompt esp
		JOIN public.engine_notes n ON n.urn = esp.note_urn
		WHERE esp.namespace = $1
		ORDER BY esp.slug ASC`, p.env.Namespace)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	items := make([]promptItem, 0)
	for rows.Next() {
		var item promptItem
		if err := rows.Scan(&item.URN, &item.Slug, &item.Body, &item.Description, &item.CreatedAt, &item.UpdatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (p *Plugin) handleGet(w http.ResponseWriter, r *http.Request, slug string) {
	item, err := p.queryBySlug(r.Context(), slug)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "prompt not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, item)
}

type createRequest struct {
	Slug        string `json:"slug"`
	Body        string `json:"body"`
	Description string `json:"description"`
	Name        string `json:"name"`
}

func (p *Plugin) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Slug == "" {
		writeError(w, http.StatusBadRequest, "slug is required")
		return
	}

	ctx := r.Context()
	now := time.Now().UTC()
	noteURN := core.NewURN(core.ObjectTypeNote)
	snipType := "prompt"
	name := req.Name
	if name == "" {
		name = req.Slug
	}

	// ── Step 1: create the engine_notes row directly (no NoteRepo) ─────────
	if err := p.sqlCreateNote(ctx, noteURN, name, snipType, now); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("create note: %v", err))
		return
	}

	// ── Step 2: append the first event with the YAML content ────────────────
	yamlContent := buildYAML(req.Slug, req.Body, req.Description)
	entries := buildEntries(yamlContent)
	if err := p.sqlAppendEvent(ctx, noteURN, 1, core.AnonURN().String(), entries, now); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("append event: %v", err))
		return
	}

	// ── Step 3: upsert into the prompt index ───────────────────────────────
	_, err := p.env.DB.ExecContext(ctx, `
		INSERT INTO engine_snips_prompt (namespace, note_urn, slug, body, description)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT(namespace, note_urn) DO UPDATE SET
		    slug        = excluded.slug,
		    body        = excluded.body,
		    description = excluded.description`,
		p.env.Namespace, noteURN.String(), req.Slug, req.Body, req.Description)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("index upsert: %v", err))
		return
	}

	writeJSON(w, http.StatusCreated, promptItem{
		URN:         noteURN.String(),
		Slug:        req.Slug,
		Body:        req.Body,
		Description: req.Description,
		CreatedAt:   now,
		UpdatedAt:   now,
	})
}

type patchRequest struct {
	Body        *string `json:"body"`
	Description *string `json:"description"`
}

func (p *Plugin) handlePatch(w http.ResponseWriter, r *http.Request, slug string) {
	ctx := r.Context()

	var req patchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// Look up the note URN and current field values from the index.
	var noteURNStr string
	var currentBody, currentDescription string
	row := p.env.DB.QueryRowContext(ctx,
		`SELECT note_urn, body, description FROM engine_snips_prompt WHERE namespace = $1 AND slug = $2`,
		p.env.Namespace, slug)
	if err := row.Scan(&noteURNStr, &currentBody, &currentDescription); err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "prompt not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	noteURN, err := core.ParseURN(noteURNStr)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("invalid note URN: %v", err))
		return
	}

	// Apply patch fields.
	newBody := currentBody
	newDescription := currentDescription
	if req.Body != nil {
		newBody = *req.Body
	}
	if req.Description != nil {
		newDescription = *req.Description
	}

	// Get current head sequence directly from the DB (no NoteRepo.Get).
	headSeq, err := p.sqlGetHeadSequence(ctx, noteURNStr)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("get head sequence: %v", err))
		return
	}

	now := time.Now().UTC()
	yamlContent := buildYAML(slug, newBody, newDescription)
	entries := buildEntries(yamlContent)
	if err := p.sqlAppendEvent(ctx, noteURN, headSeq+1, core.AnonURN().String(), entries, now); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("append event: %v", err))
		return
	}

	// Update the prompt index.
	_, err = p.env.DB.ExecContext(ctx,
		`UPDATE engine_snips_prompt SET body = $1, description = $2 WHERE namespace = $3 AND slug = $4`,
		newBody, newDescription, p.env.Namespace, slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("index update: %v", err))
		return
	}

	item, err := p.queryBySlug(ctx, slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (p *Plugin) handleDelete(w http.ResponseWriter, r *http.Request, slug string) {
	ctx := r.Context()

	var noteURNStr string
	row := p.env.DB.QueryRowContext(ctx,
		`SELECT note_urn FROM engine_snips_prompt WHERE namespace = $1 AND slug = $2`,
		p.env.Namespace, slug)
	if err := row.Scan(&noteURNStr); err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "prompt not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Delete the engine_notes row; engine_events + engine_snapshots cascade.
	if err := p.sqlDeleteNote(ctx, noteURNStr); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("delete note: %v", err))
		return
	}

	// The ON DELETE CASCADE on engine_snips_prompt.note_urn (if present) will
	// clean up the index row automatically; if not, do it explicitly.
	if _, err := p.env.DB.ExecContext(ctx,
		`DELETE FROM engine_snips_prompt WHERE namespace = $1 AND slug = $2`,
		p.env.Namespace, slug); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("index delete: %v", err))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
