package bashhistory

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"

	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/snip"
)

var bashHistorySchema = snip.SnipSchema{
	Fields: map[string]snip.FieldSpec{
		"command":        {Kind: snip.FieldKindString, Default: ""},
		"shell":          {Kind: snip.FieldKindString, Default: ""},
		"exit_code":      {Kind: snip.FieldKindInt, Default: 0},
		"canonical_form": {Kind: snip.FieldKindString, Default: ""},
		"working_dir":    {Kind: snip.FieldKindString, Default: ""},
		"duration_ms":    {Kind: snip.FieldKindInt, Default: 0},
		"hostname":       {Kind: snip.FieldKindString, Default: ""},
		"tags":           {Kind: snip.FieldKindStrList, Default: nil},
		"note":           {Kind: snip.FieldKindString, Default: ""},
	},
	IndexColumns: []snip.IndexColumn{
		{Field: "canonical_form", SQLType: "TEXT"},
		{Field: "shell", SQLType: "TEXT"},
		{Field: "exit_code", SQLType: "INTEGER"},
		{Field: "hostname", SQLType: "TEXT"},
		{Field: "working_dir", SQLType: "TEXT"},
	},
	DisplayField: "command",
	FTSFields:    []string{"command", "note"},
}

// Plugin implements snip.SnipPlugin for the "bash_history" snip type.
type Plugin struct {
	env      snip.PluginEnv
	stopOnce sync.Once
	stopCh   chan struct{}
}

// New returns a new, uninitialised bash_history Plugin.
func New() *Plugin {
	return &Plugin{
		stopCh: make(chan struct{}),
	}
}

// ── Identity ──────────────────────────────────────────────────────────────────

func (p *Plugin) Type() string    { return "bash_history" }
func (p *Plugin) Version() string { return "0.1.0" }
func (p *Plugin) Description() string {
	return "Captures shell commands as typed snips with statistics and deduplication"
}
func (p *Plugin) Schema() snip.SnipSchema { return bashHistorySchema }

// ── Lifecycle ─────────────────────────────────────────────────────────────────

func (p *Plugin) Init(ctx context.Context, env snip.PluginEnv) error {
	p.env = env

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS snips_bash_history (
			note_urn       TEXT PRIMARY KEY REFERENCES notes(note_urn),
			canonical_form TEXT NOT NULL DEFAULT '',
			shell          TEXT NOT NULL DEFAULT '',
			exit_code      INTEGER NOT NULL DEFAULT 0,
			hostname       TEXT NOT NULL DEFAULT '',
			working_dir    TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS snips_bash_history_canon ON snips_bash_history(canonical_form)`,
		`CREATE INDEX IF NOT EXISTS snips_bash_history_shell ON snips_bash_history(shell)`,
		`CREATE TABLE IF NOT EXISTS bash_history_stats (
			note_urn       TEXT PRIMARY KEY,
			canonical_form TEXT NOT NULL DEFAULT '',
			run_count      INTEGER NOT NULL DEFAULT 0,
			success_count  INTEGER NOT NULL DEFAULT 0,
			failure_count  INTEGER NOT NULL DEFAULT 0,
			first_run_at   TEXT NOT NULL DEFAULT '',
			last_run_at    TEXT NOT NULL DEFAULT '',
			recent_dirs    TEXT NOT NULL DEFAULT '[]',
			recent_hosts   TEXT NOT NULL DEFAULT '[]'
		)`,
		`CREATE TABLE IF NOT EXISTS bash_history_variations (
			note_urn       TEXT NOT NULL,
			command        TEXT NOT NULL,
			run_count      INTEGER NOT NULL DEFAULT 1,
			last_run_at    TEXT NOT NULL DEFAULT '',
			last_exit_code INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (note_urn, command)
		)`,
	}

	for _, stmt := range stmts {
		if _, err := env.DB.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (p *Plugin) Start(_ context.Context) error { return nil }

func (p *Plugin) Stop(_ context.Context) error {
	p.stopOnce.Do(func() { close(p.stopCh) })
	return nil
}

// ── Event callbacks ───────────────────────────────────────────────────────────

func (p *Plugin) OnNoteCreated(ctx context.Context, note *core.Note) error {
	content := note.Content()
	canonicalForm, shell, exitCode, hostname, workingDir := extractFields(content)
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := p.env.DB.ExecContext(ctx,
		`INSERT OR REPLACE INTO snips_bash_history(note_urn, canonical_form, shell, exit_code, hostname, working_dir)
		 VALUES(?, ?, ?, ?, ?, ?)`,
		note.URN.String(), canonicalForm, shell, exitCode, hostname, workingDir,
	)
	if err != nil {
		return err
	}

	successCount := 0
	if exitCode == 0 {
		successCount = 1
	}

	_, err = p.env.DB.ExecContext(ctx,
		`INSERT OR IGNORE INTO bash_history_stats(note_urn, canonical_form, run_count, success_count, failure_count, first_run_at, last_run_at)
		 VALUES(?, ?, 1, ?, 0, ?, ?)`,
		note.URN.String(), canonicalForm, successCount, now, now,
	)
	return err
}

func (p *Plugin) OnEventAppended(ctx context.Context, note *core.Note, _ *core.Event) error {
	content := note.Content()
	canonicalForm, shell, exitCode, hostname, workingDir := extractFields(content)
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := p.env.DB.ExecContext(ctx,
		`INSERT OR REPLACE INTO snips_bash_history(note_urn, canonical_form, shell, exit_code, hostname, working_dir)
		 VALUES(?, ?, ?, ?, ?, ?)`,
		note.URN.String(), canonicalForm, shell, exitCode, hostname, workingDir,
	)
	if err != nil {
		return err
	}

	_, err = p.env.DB.ExecContext(ctx,
		`UPDATE bash_history_stats SET run_count = run_count + 1, last_run_at = ? WHERE note_urn = ?`,
		now, note.URN.String(),
	)
	if err != nil {
		return err
	}

	command := extractYAMLField(content, "command")
	_, err = p.env.DB.ExecContext(ctx,
		`INSERT INTO bash_history_variations(note_urn, command, run_count, last_run_at, last_exit_code)
		 VALUES(?, ?, 1, ?, ?)
		 ON CONFLICT(note_urn, command) DO UPDATE SET
		     run_count      = run_count + 1,
		     last_run_at    = excluded.last_run_at,
		     last_exit_code = excluded.last_exit_code`,
		note.URN.String(), command, now, exitCode,
	)
	return err
}

func (p *Plugin) OnNoteDeleted(ctx context.Context, noteURN core.URN) error {
	urn := noteURN.String()
	for _, tbl := range []string{"bash_history_variations", "bash_history_stats", "snips_bash_history"} {
		if _, err := p.env.DB.ExecContext(ctx,
			"DELETE FROM "+tbl+" WHERE note_urn = ?", urn); err != nil {
			return err
		}
	}
	return nil
}

func (p *Plugin) OnParentAnchorBroken(_ context.Context, _ core.URN) error {
	// bash_history snips are standalone; nothing to do.
	return nil
}

// ── Transport registration ────────────────────────────────────────────────────

func (p *Plugin) RegisterGRPC(_ *grpc.Server) {}

func (p *Plugin) RegisterHTTP(mux *http.ServeMux, middleware func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("/v1/snips/bash_history/search", middleware(p.handleSearch))
	mux.HandleFunc("/v1/snips/bash_history", middleware(p.handleList))
}

// ── Internal helpers ──────────────────────────────────────────────────────────

func extractFields(content string) (canonicalForm, shell string, exitCode int, hostname, workingDir string) {
	canonicalForm = extractYAMLField(content, "canonical_form")
	shell = extractYAMLField(content, "shell")
	hostname = extractYAMLField(content, "hostname")
	workingDir = extractYAMLField(content, "working_dir")

	exitCodeStr := extractYAMLField(content, "exit_code")
	if v, err := strconv.Atoi(exitCodeStr); err == nil {
		exitCode = v
	}
	return
}

// extractYAMLField does a simple line-by-line scan for a top-level scalar YAML
// field and returns its raw string value (quotes stripped).
func extractYAMLField(content, field string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		prefix := field + ":"
		if strings.HasPrefix(line, prefix) {
			val := strings.TrimSpace(line[len(prefix):])
			val = strings.Trim(val, `"'`)
			return val
		}
	}
	return ""
}

// ── HTTP handlers ─────────────────────────────────────────────────────────────

type historyItem struct {
	NoteURN       string `json:"note_urn"`
	CanonicalForm string `json:"canonical_form"`
	Shell         string `json:"shell"`
	ExitCode      int    `json:"exit_code"`
	Hostname      string `json:"hostname"`
	WorkingDir    string `json:"working_dir"`
	RunCount      int    `json:"run_count"`
	LastRunAt     string `json:"last_run_at"`
}

func (p *Plugin) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rows, err := p.env.DB.QueryContext(r.Context(),
		`SELECT b.note_urn, b.canonical_form, b.shell, b.exit_code, b.hostname, b.working_dir,
		        COALESCE(s.run_count, 0), COALESCE(s.last_run_at, '')
		 FROM snips_bash_history b
		 JOIN notes n ON n.urn = b.note_urn
		 LEFT JOIN bash_history_stats s ON s.note_urn = b.note_urn
		 WHERE n.deleted = 0
		 ORDER BY s.last_run_at DESC`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var items []historyItem
	for rows.Next() {
		var item historyItem
		if err := rows.Scan(
			&item.NoteURN, &item.CanonicalForm, &item.Shell, &item.ExitCode,
			&item.Hostname, &item.WorkingDir, &item.RunCount, &item.LastRunAt,
		); err != nil {
			continue
		}
		items = append(items, item)
	}
	if items == nil {
		items = []historyItem{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"commands": items})
}

func (p *Plugin) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Stub: full-text search is a future enhancement.
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"commands": []historyItem{}})
}
