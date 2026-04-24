package todo

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"

	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/snip"
)

var todoSchema = snip.SnipSchema{
	Fields: map[string]snip.FieldSpec{
		"text":           {Kind: snip.FieldKindString, Default: ""},
		"file_path":      {Kind: snip.FieldKindString, Default: ""},
		"checkbox_state": {Kind: snip.FieldKindString, Default: "open"},
		"status":         {Kind: snip.FieldKindString, Default: "backlog"},
		"line_number":    {Kind: snip.FieldKindInt, Default: 0},
		"anchor_id":      {Kind: snip.FieldKindString, Default: ""},
		"comments":       {Kind: snip.FieldKindYAMLList, Default: nil},
		"tags":           {Kind: snip.FieldKindStrList, Default: nil},
	},
	IndexColumns: []snip.IndexColumn{
		{Field: "status", SQLType: "TEXT"},
		{Field: "file_path", SQLType: "TEXT"},
		{Field: "checkbox_state", SQLType: "TEXT"},
		{Field: "anchor_id", SQLType: "TEXT"},
	},
	DisplayField: "text",
	StatusField:  "status",
	StatusValues: []string{"backlog", "doing", "done"},
	FTSFields:    []string{"text"},
}

// checkboxRE matches markdown checkbox list items in both forms:
//
//   - [ ] some text
//   - [x] some text   (also [X])
//
// It captures:
//
//	group 1 — the mark character (' ' or 'x'/'X')
//	group 2 — the rest of the line (todo text)
var checkboxRE = regexp.MustCompile(`(?i)^[\s]*-\s+\[([xX ])\]\s+(.*)$`)

// Plugin implements snip.SnipPlugin for the "todo" snip type.
type Plugin struct {
	env      snip.PluginEnv
	stopOnce sync.Once
	stopCh   chan struct{}
}

// New returns a new, uninitialised todo Plugin.
func New() *Plugin {
	return &Plugin{
		stopCh: make(chan struct{}),
	}
}

// ── Identity ─────────────────────────────────────────────────────────────────

func (p *Plugin) Type() string    { return "todo" }
func (p *Plugin) Version() string { return "0.2.0" }
func (p *Plugin) Description() string {
	return "Tracks Markdown checkboxes as structured todos with a Kanban board API"
}
func (p *Plugin) Schema() snip.SnipSchema { return todoSchema }

// ── Lifecycle ─────────────────────────────────────────────────────────────────

// Init stores the env and ensures the engine_snips_todo table exists.
func (p *Plugin) Init(ctx context.Context, env snip.PluginEnv) error {
	p.env = env
	if _, err := p.env.DB.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS engine_snips_todo (
		    namespace      TEXT    NOT NULL DEFAULT '',
		    note_urn       TEXT    NOT NULL,
		    line_number    INTEGER NOT NULL,
		    text           TEXT    NOT NULL DEFAULT '',
		    status         TEXT    NOT NULL DEFAULT 'backlog',
		    checkbox_state TEXT    NOT NULL DEFAULT 'open',
		    file_path      TEXT    NOT NULL DEFAULT '',
		    anchor_id      TEXT    NOT NULL DEFAULT '',
		    project_urn    TEXT    NOT NULL DEFAULT '',
		    folder_urn     TEXT    NOT NULL DEFAULT '',
		    due_date       TEXT    NOT NULL DEFAULT '',
		    orphaned       INTEGER NOT NULL DEFAULT 0,
		    PRIMARY KEY (namespace, note_urn, line_number)
		)`); err != nil {
		return err
	}
	_, err := p.env.DB.ExecContext(ctx, `
		CREATE UNIQUE INDEX IF NOT EXISTS idx_todo_ns_note_text
		ON engine_snips_todo(namespace, note_urn, text)`)
	return err
}

func (p *Plugin) Start(_ context.Context) error { return nil }

func (p *Plugin) Stop(_ context.Context) error {
	p.stopOnce.Do(func() { close(p.stopCh) })
	return nil
}

// ── Event callbacks ───────────────────────────────────────────────────────────

// OnNoteCreated scans the note for checkboxes and upserts index rows.
// Called for every note write — the plugin self-filters by inspecting content.
func (p *Plugin) OnNoteCreated(ctx context.Context, note *core.Note) error {
	return p.syncCheckboxes(ctx, note)
}

// OnEventAppended applies surgical SQL index updates based on the event entries.
func (p *Plugin) OnEventAppended(ctx context.Context, note *core.Note, event *core.Event) error {
	return p.applyEventToIndex(ctx, note, event)
}

// OnNoteDeleted removes all checkbox rows for this note.
func (p *Plugin) OnNoteDeleted(ctx context.Context, noteURN core.URN) error {
	_, err := p.env.DB.ExecContext(ctx,
		`DELETE FROM engine_snips_todo WHERE namespace = $1 AND note_urn = $2`,
		"", noteURN.String())
	return err
}

// OnParentAnchorBroken marks all rows for this note as orphaned.
func (p *Plugin) OnParentAnchorBroken(ctx context.Context, noteURN core.URN) error {
	_, err := p.env.DB.ExecContext(ctx,
		`UPDATE engine_snips_todo SET orphaned = true WHERE namespace = $1 AND note_urn = $2`,
		"", noteURN.String())
	return err
}

// ── Transport registration ────────────────────────────────────────────────────

func (p *Plugin) RegisterGRPC(_ *grpc.Server) {}

func (p *Plugin) RegisterHTTP(mux *http.ServeMux, middleware func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("/v1/snips/todo/projects/", middleware(p.handleProjectsDispatch))
	mux.HandleFunc("/v1/snips/todo/board", middleware(p.handleBoard))
	mux.HandleFunc("/v1/snips/todo", middleware(p.routeTodo))
}

func (p *Plugin) routeTodo(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		p.handleListTodos(w, r)
	case http.MethodPatch:
		p.handleUpdateTodoStatus(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ── Checkbox scanner ──────────────────────────────────────────────────────────

// parsedCheckbox holds the extracted fields for a single checkbox line.
// isCheckboxLine returns true if the line matches the checkbox regex.
func isCheckboxLine(s string) bool {
	return checkboxRE.MatchString(s)
}

// parseCheckboxLine extracts (text, checkboxState) from a checkbox line.
// Returns ("", "") if not a checkbox.
func parseCheckboxLine(s string) (text, state string) {
	m := checkboxRE.FindStringSubmatch(s)
	if m == nil {
		return "", ""
	}
	mark := strings.ToLower(m[1])
	state = "open"
	if mark == "x" {
		state = "checked"
	}
	return strings.TrimSpace(m[2]), state
}

// applyEventToIndex applies surgical SQL updates to the todo index based on
// the entries in a single event, without rescanning the full note content.
// Entry line numbers are relative to the evolving document state after all
// prior entries in the same event have been applied.
func (p *Plugin) applyEventToIndex(ctx context.Context, note *core.Note, event *core.Event) error {
	ns := ""
	noteURN := note.URN.String()

	var projectURN, folderURN string
	if note.ProjectURN != nil {
		projectURN = note.ProjectURN.String()
	}
	if note.FolderURN != nil {
		folderURN = note.FolderURN.String()
	}

	offset := 0

	for _, entry := range event.Entries {
		effectiveLine := entry.LineNumber + offset // 1-based

		switch entry.Op {
		case core.LineOpInsert:
			// 1. Shift existing todos at line >= effectiveLine down by 1.
			if _, err := p.env.DB.ExecContext(ctx, `
				UPDATE engine_snips_todo
				SET line_number = line_number + 1
				WHERE namespace = $1 AND note_urn = $2 AND line_number >= $3`,
				ns, noteURN, effectiveLine,
			); err != nil {
				return fmt.Errorf("todo: insert shift down line %d: %w", effectiveLine, err)
			}

			// 2. If the inserted content is a checkbox, add it to the index.
			if isCheckboxLine(entry.Content) {
				text, state := parseCheckboxLine(entry.Content)
				if _, err := p.env.DB.ExecContext(ctx, `
					INSERT INTO engine_snips_todo
						(namespace, note_urn, text, line_number, status, checkbox_state,
						 file_path, anchor_id, project_urn, folder_urn, due_date, orphaned)
					VALUES ($1, $2, $3, $4, 'backlog', $5, '', '', $6, $7, '', false)
					ON CONFLICT (namespace, note_urn, text) DO NOTHING`,
					ns, noteURN, text, effectiveLine, state, projectURN, folderURN,
				); err != nil {
					return fmt.Errorf("todo: insert checkbox at line %d: %w", effectiveLine, err)
				}
				// Update line_number and state in case the row already existed
				// (conflict was hit above and the DO NOTHING fired).
				if _, err := p.env.DB.ExecContext(ctx, `
					UPDATE engine_snips_todo
					SET line_number = $4, checkbox_state = $5, project_urn = $6, folder_urn = $7, orphaned = false
					WHERE namespace = $1 AND note_urn = $2 AND text = $3`,
					ns, noteURN, text, effectiveLine, state, projectURN, folderURN,
				); err != nil {
					return fmt.Errorf("todo: update inserted checkbox at line %d: %w", effectiveLine, err)
				}
			}

			offset++

		case core.LineOpDelete:
			// 1. Delete the todo row at this line (if any).
			if _, err := p.env.DB.ExecContext(ctx, `
				DELETE FROM engine_snips_todo
				WHERE namespace = $1 AND note_urn = $2 AND line_number = $3`,
				ns, noteURN, effectiveLine,
			); err != nil {
				return fmt.Errorf("todo: delete checkbox at line %d: %w", effectiveLine, err)
			}

			// 2. Shift todos above the deleted line up by 1.
			if _, err := p.env.DB.ExecContext(ctx, `
				UPDATE engine_snips_todo
				SET line_number = line_number - 1
				WHERE namespace = $1 AND note_urn = $2 AND line_number > $3`,
				ns, noteURN, effectiveLine,
			); err != nil {
				return fmt.Errorf("todo: delete shift up line %d: %w", effectiveLine, err)
			}

			offset--

		case core.LineOpSet, core.LineOpSetEmpty:
			content := entry.Content // empty string for LineOpSetEmpty

			// Look up whether there is currently a todo row at this line.
			var existingText, existingState string
			err := p.env.DB.QueryRowContext(ctx, `
				SELECT text, checkbox_state
				FROM engine_snips_todo
				WHERE namespace = $1 AND note_urn = $2 AND line_number = $3`,
				ns, noteURN, effectiveLine,
			).Scan(&existingText, &existingState)

			rowExists := err == nil
			if err != nil && err != sql.ErrNoRows {
				return fmt.Errorf("todo: query existing at line %d: %w", effectiveLine, err)
			}

			newIsCheckbox := isCheckboxLine(content)
			newText, newState := parseCheckboxLine(content)

			if rowExists {
				if newIsCheckbox {
					// Update text and checkbox_state; preserve status.
					if _, err := p.env.DB.ExecContext(ctx, `
						UPDATE engine_snips_todo
						SET text = $4, checkbox_state = $5
						WHERE namespace = $1 AND note_urn = $2 AND line_number = $3`,
						ns, noteURN, effectiveLine, newText, newState,
					); err != nil {
						return fmt.Errorf("todo: update checkbox at line %d: %w", effectiveLine, err)
					}
				} else {
					// Line was a checkbox, now it's plain text — remove it.
					if _, err := p.env.DB.ExecContext(ctx, `
						DELETE FROM engine_snips_todo
						WHERE namespace = $1 AND note_urn = $2 AND line_number = $3`,
						ns, noteURN, effectiveLine,
					); err != nil {
						return fmt.Errorf("todo: delete ex-checkbox at line %d: %w", effectiveLine, err)
					}
				}
			} else {
				if newIsCheckbox {
					// New checkbox appeared on a previously non-checkbox line.
					if _, err := p.env.DB.ExecContext(ctx, `
						INSERT INTO engine_snips_todo
							(namespace, note_urn, text, line_number, status, checkbox_state,
							 file_path, anchor_id, project_urn, folder_urn, due_date, orphaned)
						VALUES ($1, $2, $3, $4, 'backlog', $5, '', '', $6, $7, '', false)
						ON CONFLICT (namespace, note_urn, text) DO NOTHING`,
						ns, noteURN, newText, effectiveLine, newState, projectURN, folderURN,
					); err != nil {
						return fmt.Errorf("todo: insert new checkbox at line %d: %w", effectiveLine, err)
					}
					if _, err := p.env.DB.ExecContext(ctx, `
						UPDATE engine_snips_todo
						SET line_number = $4, checkbox_state = $5, project_urn = $6, folder_urn = $7, orphaned = false
						WHERE namespace = $1 AND note_urn = $2 AND text = $3`,
						ns, noteURN, newText, effectiveLine, newState, projectURN, folderURN,
					); err != nil {
						return fmt.Errorf("todo: update new checkbox at line %d: %w", effectiveLine, err)
					}
				}
				// else: non-checkbox set on non-checkbox line → no-op for todos.
			}
		}
	}

	return nil
}

type parsedCheckbox struct {
	lineNumber    int
	text          string
	checkboxState string // "open" or "checked"
}

// parseCheckboxes scans note content line by line and returns one entry per
// checkbox found. Line numbers are 0-based.
func parseCheckboxes(content string) []parsedCheckbox {
	var out []parsedCheckbox
	for i, line := range strings.Split(content, "\n") {
		m := checkboxRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		mark := strings.ToLower(m[1])
		state := "open"
		if mark == "x" {
			state = "checked"
		}
		out = append(out, parsedCheckbox{
			lineNumber:    i,
			text:          strings.TrimSpace(m[2]),
			checkboxState: state,
		})
	}
	return out
}

// syncCheckboxes is the core indexing routine. It:
//  1. Parses all checkbox lines from the note content.
//  2. Upserts one engine_snips_todo row per checkbox, preserving any
//     user-set status (backlog/doing/done) that already exists in the index.
//  3. Deletes index rows for line numbers that no longer have a checkbox
//     (e.g. the user removed a checkbox from the note).
func (p *Plugin) syncCheckboxes(ctx context.Context, note *core.Note) error {
	content := note.Content()
	checkboxes := parseCheckboxes(content)

	p.env.Log.Debug("todo: syncCheckboxes called",
		"note_urn", note.URN.String(),
		"content_len", len(content),
		"checkboxes_found", len(checkboxes),
	)

	// If the note has no checkboxes, clean up any stale rows and return.
	if len(checkboxes) == 0 {
		_, err := p.env.DB.ExecContext(ctx,
			`DELETE FROM engine_snips_todo WHERE namespace = $1 AND note_urn = $2`,
			"", note.URN.String())
		return err
	}

	var projectURN, folderURN string
	if note.ProjectURN != nil {
		projectURN = note.ProjectURN.String()
	}
	if note.FolderURN != nil {
		folderURN = note.FolderURN.String()
	}

	// Upsert each checkbox using two steps so we can preserve any user-set
	// status (backlog/doing/done) that already exists in the index:
	//
	//   Step A — INSERT … ON CONFLICT DO NOTHING
	//     Creates the row the first time with status='backlog'.
	//
	//   Step B — UPDATE … SET text/checkbox_state/project_urn/folder_urn
	//     Refreshes mutable fields on every write without touching status.
	for _, cb := range checkboxes {
		p.env.Log.Debug("todo: upserting checkbox",
			"note_urn", note.URN.String(),
			"line", cb.lineNumber,
			"text", cb.text,
			"state", cb.checkboxState,
		)

		// Step A: insert the row if it does not yet exist, keyed by text.
		// text is the stable identity of a checkbox — line_number is just a
		// sort hint that may shift as lines are added/removed above it.
		if _, err := p.env.DB.ExecContext(ctx, `
			INSERT INTO engine_snips_todo
				(namespace, note_urn, text, line_number, status, checkbox_state,
				 file_path, anchor_id, project_urn, folder_urn, due_date, orphaned)
			VALUES ($1, $2, $3, $4, 'backlog', $5, '', '', $6, $7, '', false)
			ON CONFLICT (namespace, note_urn, text) DO NOTHING`,
			"", note.URN.String(), cb.text, cb.lineNumber,
			cb.checkboxState,
			projectURN,
			folderURN,
		); err != nil {
			p.env.Log.Error("todo: insert checkbox failed",
				"note_urn", note.URN.String(),
				"line", cb.lineNumber,
				"text", cb.text,
				"err", err,
			)
			return fmt.Errorf("todo: insert checkbox line %d: %w", cb.lineNumber, err)
		}

		// Step B: update mutable fields — line_number, checkbox_state, urns.
		// Status is intentionally left alone so board moves survive note edits.
		if _, err := p.env.DB.ExecContext(ctx, `
			UPDATE engine_snips_todo SET
				line_number    = $4,
				checkbox_state = $5,
				project_urn    = $6,
				folder_urn     = $7,
				orphaned       = false
			WHERE namespace = $1
			  AND note_urn  = $2
			  AND text      = $3`,
			"", note.URN.String(), cb.text,
			cb.lineNumber, cb.checkboxState, projectURN, folderURN,
		); err != nil {
			p.env.Log.Error("todo: update checkbox failed",
				"note_urn", note.URN.String(),
				"line", cb.lineNumber,
				"text", cb.text,
				"err", err,
			)
			return fmt.Errorf("todo: update checkbox line %d: %w", cb.lineNumber, err)
		}

		p.env.Log.Debug("todo: upsert checkbox ok", "line", cb.lineNumber)
	}

	// Delete rows whose text label is no longer present (checkbox was removed).
	// Build a text array literal: {"Buy milk","Fix bug"} and use <> ALL to
	// exclude every label that still exists in the parsed set.
	if _, err := p.env.DB.ExecContext(ctx, `
		DELETE FROM engine_snips_todo
		WHERE namespace = $1
		  AND note_urn  = $2
		  AND text      <> ALL($3::text[])`,
		"", note.URN.String(), textSliceToArray(currentTexts(checkboxes)),
	); err != nil {
		return fmt.Errorf("todo: prune stale checkbox rows: %w", err)
	}

	return nil
}

// currentTexts returns the text labels of all parsed checkboxes.
func currentTexts(cbs []parsedCheckbox) []string {
	out := make([]string, len(cbs))
	for i, cb := range cbs {
		out[i] = cb.text
	}
	return out
}

// textSliceToArray converts []string to a Postgres text array literal accepted
// by the lib/pq driver (e.g. `{"Buy milk","Fix bug"}`).
// Double-quotes inside values are escaped as \".
func textSliceToArray(ss []string) string {
	if len(ss) == 0 {
		return "{}"
	}
	parts := make([]string, len(ss))
	for i, s := range ss {
		escaped := strings.ReplaceAll(s, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		parts[i] = `"` + escaped + `"`
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// ── Query helpers ─────────────────────────────────────────────────────────────

type todoFilter struct {
	projectURN string
	folderURN  string
	status     string
	overdue    bool
	orphaned   bool
	pageSize   int
	pageToken  string
}

type todoItem struct {
	NoteURN       string    `json:"note_urn"`
	LineNumber    int       `json:"line_number"`
	Text          string    `json:"text"`
	Status        string    `json:"status"`
	CheckboxState string    `json:"checkbox_state"`
	FilePath      string    `json:"file_path"`
	AnchorID      string    `json:"anchor_id"`
	Orphaned      bool      `json:"orphaned"`
	ProjectURN    string    `json:"project_urn"`
	FolderURN     string    `json:"folder_urn"`
	ParentURN     string    `json:"parent_urn"`
	DueDate       string    `json:"due_date"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (p *Plugin) queryTodos(ctx context.Context, f todoFilter) ([]todoItem, error) {
	pageSize := f.pageSize
	if pageSize <= 0 {
		pageSize = 50
	}

	var sb strings.Builder
	var args []interface{}

	sb.WriteString(`
		SELECT t.note_urn, t.line_number, t.text, t.status, t.checkbox_state,
		       t.file_path, COALESCE(t.anchor_id,''), t.orphaned,
		       COALESCE(t.project_urn,''), COALESCE(t.folder_urn,''),
		       COALESCE(n.parent_urn,''), COALESCE(t.due_date,''),
		       n.created_at, n.updated_at
		FROM engine_snips_todo t
		JOIN notes n ON n.urn = t.note_urn
		WHERE t.namespace = ? AND n.deleted = 0`)
	args = append(args, "")

	add := func(cond string, val interface{}) {
		sb.WriteString(" AND " + cond + " ?")
		args = append(args, val)
	}

	if f.projectURN != "" {
		add("t.project_urn =", f.projectURN)
	}
	if f.folderURN != "" {
		add("t.folder_urn =", f.folderURN)
	}
	if f.status != "" {
		add("t.status =", f.status)
	}
	if f.overdue {
		sb.WriteString(" AND t.due_date <> '' AND t.due_date < DATE('now') AND t.status <> 'done'")
	}
	if f.orphaned {
		sb.WriteString(" AND t.orphaned = 1")
	}

	// Keyset pagination on (updated_at DESC, note_urn, line_number).
	if f.pageToken != "" {
		ts, noteURN, lineNum, err := decodePageToken(f.pageToken)
		if err == nil {
			sb.WriteString(" AND (n.updated_at, t.note_urn, t.line_number) < (?, ?, ?)")
			args = append(args, ts.UnixMilli(), noteURN, lineNum)
		}
	}

	sb.WriteString(" ORDER BY n.updated_at DESC, t.note_urn, t.line_number")
	sb.WriteString(" LIMIT ?")
	args = append(args, pageSize+1)

	rows, err := p.env.DB.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("todo: query todos: %w", err)
	}
	defer rows.Close()

	var items []todoItem
	for rows.Next() {
		var item todoItem
		var orphaned bool
		var createdAtMs, updatedAtMs int64
		if err := rows.Scan(
			&item.NoteURN, &item.LineNumber, &item.Text,
			&item.Status, &item.CheckboxState,
			&item.FilePath, &item.AnchorID, &orphaned,
			&item.ProjectURN, &item.FolderURN, &item.ParentURN, &item.DueDate,
			&createdAtMs, &updatedAtMs,
		); err != nil {
			continue
		}
		item.Orphaned = orphaned
		item.CreatedAt = time.UnixMilli(createdAtMs).UTC()
		item.UpdatedAt = time.UnixMilli(updatedAtMs).UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("todo: query todos rows: %w", err)
	}
	if items == nil {
		items = []todoItem{}
	}
	return items, nil
}

// ── Page token ────────────────────────────────────────────────────────────────

type pageTokenData struct {
	T  time.Time `json:"t"`
	U  string    `json:"u"`
	LN int       `json:"ln"`
}

func encodePageToken(updatedAt time.Time, noteURN string, lineNum int) string {
	b, _ := json.Marshal(pageTokenData{T: updatedAt, U: noteURN, LN: lineNum})
	return fmt.Sprintf("%x", b)
}

func decodePageToken(token string) (time.Time, string, int, error) {
	if token == "" {
		return time.Time{}, "", 0, fmt.Errorf("empty token")
	}
	b := make([]byte, len(token)/2)
	if _, err := fmt.Sscanf(strings.ReplaceAll(token, " ", ""), "%x", &b); err != nil {
		// fallback: try direct hex decode
		b2, err2 := hexDecode(token)
		if err2 != nil {
			return time.Time{}, "", 0, err
		}
		b = b2
	}
	var d pageTokenData
	if err := json.Unmarshal(b, &d); err != nil {
		return time.Time{}, "", 0, err
	}
	return d.T, d.U, d.LN, nil
}

func hexDecode(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, fmt.Errorf("odd hex length")
	}
	b := make([]byte, len(s)/2)
	for i := 0; i < len(s); i += 2 {
		var v byte
		if _, err := fmt.Sscanf(s[i:i+2], "%02x", &v); err != nil {
			return nil, err
		}
		b[i/2] = v
	}
	return b, nil
}

// ── Summary helpers ───────────────────────────────────────────────────────────

type folderSummary struct {
	FolderURN     string         `json:"folder_urn"`
	Total         int            `json:"total"`
	ByStatus      map[string]int `json:"by_status"`
	Overdue       int            `json:"overdue"`
	Orphaned      int            `json:"orphaned"`
	CompletionPct float64        `json:"completion_pct"`
}

type projectSummary struct {
	ProjectURN    string          `json:"project_urn"`
	Total         int             `json:"total"`
	ByStatus      map[string]int  `json:"by_status"`
	Overdue       int             `json:"overdue"`
	Orphaned      int             `json:"orphaned"`
	CompletionPct float64         `json:"completion_pct"`
	Folders       []folderSummary `json:"folders"`
}

func buildSummary(projectURN string, items []todoItem) projectSummary {
	s := projectSummary{
		ProjectURN: projectURN,
		ByStatus:   map[string]int{"backlog": 0, "doing": 0, "done": 0},
		Folders:    []folderSummary{},
	}
	folderMap := map[string]*folderSummary{}

	today := time.Now().UTC().Format("2006-01-02")

	for _, item := range items {
		s.Total++
		s.ByStatus[item.Status]++
		if item.Orphaned {
			s.Orphaned++
		}
		if item.DueDate != "" && item.DueDate < today && item.Status != "done" {
			s.Overdue++
		}

		if item.FolderURN != "" {
			fs, ok := folderMap[item.FolderURN]
			if !ok {
				fs = &folderSummary{
					FolderURN: item.FolderURN,
					ByStatus:  map[string]int{"backlog": 0, "doing": 0, "done": 0},
				}
				folderMap[item.FolderURN] = fs
			}
			fs.Total++
			fs.ByStatus[item.Status]++
			if item.Orphaned {
				fs.Orphaned++
			}
			if item.DueDate != "" && item.DueDate < today && item.Status != "done" {
				fs.Overdue++
			}
		}
	}

	if s.Total > 0 {
		s.CompletionPct = float64(s.ByStatus["done"]) / float64(s.Total) * 100
	}
	for _, fs := range folderMap {
		if fs.Total > 0 {
			fs.CompletionPct = float64(fs.ByStatus["done"]) / float64(fs.Total) * 100
		}
		s.Folders = append(s.Folders, *fs)
	}
	return s
}

// ── Filter parser ─────────────────────────────────────────────────────────────

func filterFromQuery(r *http.Request) todoFilter {
	q := r.URL.Query()
	f := todoFilter{
		projectURN: q.Get("project_urn"),
		folderURN:  q.Get("folder_urn"),
		status:     q.Get("status"),
		overdue:    q.Get("overdue") == "true",
		orphaned:   q.Get("orphaned") == "true",
		pageToken:  q.Get("page_token"),
	}
	if v := q.Get("page_size"); v != "" {
		fmt.Sscanf(v, "%d", &f.pageSize)
	}
	return f
}

// ── HTTP handlers ─────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func (p *Plugin) handleListTodos(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	f := filterFromQuery(r)
	items, err := p.queryTodos(r.Context(), f)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := map[string]interface{}{"todos": items}

	// Attach next_page_token when there are more results.
	pageSize := f.pageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	if len(items) > pageSize {
		items = items[:pageSize]
		last := items[len(items)-1]
		resp["todos"] = items
		resp["next_page_token"] = encodePageToken(last.UpdatedAt, last.NoteURN, last.LineNumber)
	}

	writeJSON(w, resp)
}

func (p *Plugin) handleBoard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	f := filterFromQuery(r)
	items, err := p.queryTodos(r.Context(), f)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	board := map[string][]todoItem{
		"backlog": {},
		"doing":   {},
		"done":    {},
	}
	for _, item := range items {
		if _, ok := board[item.Status]; ok {
			board[item.Status] = append(board[item.Status], item)
		} else {
			board[item.Status] = append(board[item.Status], item)
		}
	}

	resp := map[string]interface{}{"board": board}
	if f.projectURN != "" {
		s := buildSummary(f.projectURN, items)
		resp["summary"] = s
	}
	writeJSON(w, resp)
}

// handleProjectsDispatch routes /v1/snips/todo/projects/{urn}/{action}
func (p *Plugin) handleProjectsDispatch(w http.ResponseWriter, r *http.Request) {
	// Strip prefix and split: ["", "{urn}", "{action}"]
	rest := strings.TrimPrefix(r.URL.Path, "/v1/snips/todo/projects/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) < 2 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	projectURN := parts[0]
	action := parts[1]

	switch action {
	case "summary":
		p.handleProjectSummary(w, r, projectURN)
	case "board":
		p.handleProjectBoard(w, r, projectURN)
	case "activity":
		p.handleProjectActivity(w, r, projectURN)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (p *Plugin) handleProjectSummary(w http.ResponseWriter, r *http.Request, projectURN string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	items, err := p.queryTodos(r.Context(), todoFilter{projectURN: projectURN, pageSize: 10000})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, buildSummary(projectURN, items))
}

func (p *Plugin) handleProjectBoard(w http.ResponseWriter, r *http.Request, projectURN string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	f := filterFromQuery(r)
	f.projectURN = projectURN

	items, err := p.queryTodos(r.Context(), f)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	board := map[string][]todoItem{
		"backlog": {},
		"doing":   {},
		"done":    {},
	}
	for _, item := range items {
		if _, ok := board[item.Status]; ok {
			board[item.Status] = append(board[item.Status], item)
		} else {
			board[item.Status] = append(board[item.Status], item)
		}
	}

	writeJSON(w, map[string]interface{}{
		"board":   board,
		"summary": buildSummary(projectURN, items),
	})
}

type activityDay struct {
	Date      string `json:"date"`
	Created   int    `json:"created"`
	Completed int    `json:"completed"`
}

func (p *Plugin) handleProjectActivity(w http.ResponseWriter, r *http.Request, projectURN string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	days := 30
	if v := r.URL.Query().Get("days"); v != "" {
		fmt.Sscanf(v, "%d", &days)
	}
	if days <= 0 || days > 365 {
		days = 30
	}

	// Query created counts per day.
	createdRows, err := p.env.DB.QueryContext(r.Context(), `
		SELECT TO_CHAR(n.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD') AS day,
		       COUNT(*) AS cnt
		FROM engine_snips_todo t
		JOIN engine_notes n ON n.urn = t.note_urn
		WHERE t.namespace  = $1
		  AND t.project_urn = $2
		  AND n.created_at >= NOW() - ($3 || ' days')::interval
		  AND n.deleted = false
		GROUP BY day
		ORDER BY day`,
		"", projectURN, fmt.Sprintf("%d", days),
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer createdRows.Close()

	actMap := map[string]*activityDay{}
	for createdRows.Next() {
		var day string
		var cnt int
		if err := createdRows.Scan(&day, &cnt); err != nil {
			continue
		}
		actMap[day] = &activityDay{Date: day, Created: cnt}
	}

	// Query completed counts per day (updated_at when status = 'done').
	completedRows, err := p.env.DB.QueryContext(r.Context(), `
		SELECT TO_CHAR(n.updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD') AS day,
		       COUNT(*) AS cnt
		FROM engine_snips_todo t
		JOIN engine_notes n ON n.urn = t.note_urn
		WHERE t.namespace   = $1
		  AND t.project_urn = $2
		  AND t.status      = 'done'
		  AND n.updated_at >= NOW() - ($3 || ' days')::interval
		  AND n.deleted = false
		GROUP BY day
		ORDER BY day`,
		"", projectURN, fmt.Sprintf("%d", days),
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer completedRows.Close()

	for completedRows.Next() {
		var day string
		var cnt int
		if err := completedRows.Scan(&day, &cnt); err != nil {
			continue
		}
		if d, ok := actMap[day]; ok {
			d.Completed = cnt
		} else {
			actMap[day] = &activityDay{Date: day, Completed: cnt}
		}
	}

	// Sort by date.
	type kv struct {
		k string
		v *activityDay
	}
	sorted := make([]kv, 0, len(actMap))
	for k, v := range actMap {
		sorted = append(sorted, kv{k, v})
	}
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[i].k > sorted[j].k {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	result := make([]activityDay, len(sorted))
	for i, kv := range sorted {
		result[i] = *kv.v
	}

	writeJSON(w, map[string]interface{}{"activity": result})
}

// ── Status update handler ─────────────────────────────────────────────────────

type updateTodoStatusRequest struct {
	NoteURN    string `json:"note_urn"`
	LineNumber int    `json:"line_number"`
	Status     string `json:"status"`
}

type updateTodoStatusResponse struct {
	Updated bool   `json:"updated"`
	Status  string `json:"status"`
}

func (p *Plugin) handleUpdateTodoStatus(w http.ResponseWriter, r *http.Request) {
	var req updateTodoStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	validStatus := map[string]bool{"backlog": true, "doing": true, "done": true}
	if !validStatus[req.Status] {
		http.Error(w, "invalid status: must be backlog, doing, or done", http.StatusBadRequest)
		return
	}
	result, err := p.env.DB.ExecContext(r.Context(),
		`UPDATE engine_snips_todo SET status = ? WHERE namespace = ? AND note_urn = ? AND line_number = ?`,
		req.Status, "", req.NoteURN, req.LineNumber,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		http.Error(w, "todo not found", http.StatusNotFound)
		return
	}
	writeJSON(w, updateTodoStatusResponse{Updated: true, Status: req.Status})
}
