package snip

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/zebaqui/notx-engine/config"
	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/repo"
)

// ─────────────────────────────────────────────────────────────────────────────
// FieldKind — the data type of a snip schema field
// ─────────────────────────────────────────────────────────────────────────────

// FieldKind describes the scalar type of a single field in a SnipSchema.
type FieldKind int

const (
	// FieldKindString is a plain UTF-8 string value.
	FieldKindString FieldKind = iota

	// FieldKindInt is a 64-bit signed integer value.
	FieldKindInt

	// FieldKindBool is a boolean (true/false) value.
	FieldKindBool

	// FieldKindStrList is an ordered list of strings, serialised as a JSON
	// array in storage.
	FieldKindStrList

	// FieldKindYAMLList is an ordered list of arbitrary YAML-serialisable
	// values. Stored as a YAML-encoded blob in the note body.
	FieldKindYAMLList
)

// ─────────────────────────────────────────────────────────────────────────────
// FieldSpec — a single field declaration in a SnipSchema
// ─────────────────────────────────────────────────────────────────────────────

// FieldSpec declares the type and default value for one field in a snip's
// structured schema.
type FieldSpec struct {
	// Kind is the data type of this field.
	Kind FieldKind

	// Default is the zero/default value for this field when it is absent.
	// The concrete type must match Kind:
	//   FieldKindString   → string
	//   FieldKindInt      → int64
	//   FieldKindBool     → bool
	//   FieldKindStrList  → []string
	//   FieldKindYAMLList → []any
	Default any
}

// ─────────────────────────────────────────────────────────────────────────────
// IndexColumn — a field projected into the SQL snip index table
// ─────────────────────────────────────────────────────────────────────────────

// IndexColumn describes one column that a plugin adds to its dedicated SQLite
// index table, enabling efficient filtering and sorting on structured snip
// fields.
type IndexColumn struct {
	// Field is the FieldSpec key this column mirrors.
	Field string

	// SQLType is the SQLite column type declaration (e.g. "TEXT", "INTEGER",
	// "BOOLEAN").
	SQLType string
}

// ─────────────────────────────────────────────────────────────────────────────
// SnipSchema — the structural declaration of a snip type
// ─────────────────────────────────────────────────────────────────────────────

// SnipSchema describes the complete structure of a single snip type. The engine
// uses this at initialisation time to create index tables, configure full-text
// search, and validate field values.
type SnipSchema struct {
	// Fields is the complete set of structured fields for this snip type.
	// Keys are the canonical field names used in storage and API responses.
	Fields map[string]FieldSpec

	// IndexColumns lists the fields (and their SQL types) that are projected
	// into the SQLite index table for this snip type. Only fields listed here
	// are queryable via ListSnips filters beyond the base columns.
	IndexColumns []IndexColumn

	// DisplayField is the name of the field whose value is used as the
	// human-readable title / display label for a snip in list views.
	DisplayField string

	// StatusField, if non-empty, names the field that holds the snip's
	// lifecycle status. The allowed values are enumerated in StatusValues.
	StatusField string

	// StatusValues enumerates the valid values for StatusField in the order
	// they should be presented in UIs (e.g. ["open", "in_progress", "done"]).
	// Ignored when StatusField is empty.
	StatusValues []string

	// FTSFields lists the field names whose text content is indexed for
	// full-text search. Fields not listed here are excluded from FTS.
	FTSFields []string

	// Secure, when true, marks the snip type as end-to-end encrypted. Snips
	// of this type follow the same encryption rules as NoteTypeSecure notes:
	// their content is never stored in plaintext on the server.
	Secure bool
}

// ─────────────────────────────────────────────────────────────────────────────
// PluginEnv — the runtime environment injected into every snip plugin
// ─────────────────────────────────────────────────────────────────────────────

// PluginEnv is the set of engine-managed dependencies made available to a
// SnipPlugin during Init. Plugins must not retain references to PluginEnv
// fields after Stop returns.
type PluginEnv struct {
	// DB is the shared SQLite database connection for index tables.
	DB *sql.DB

	// NoteRepo is the note repository for reading and writing notes.
	NoteRepo repo.NoteRepository

	// ProjRepo is the project/folder repository.
	ProjRepo repo.ProjectRepository

	// Config is the server-wide configuration.
	Config *config.Config

	// Log is the structured logger scoped to this plugin's snip type.
	Log *slog.Logger
}

// ─────────────────────────────────────────────────────────────────────────────
// SnipPlugin — the interface every snip plugin must implement
// ─────────────────────────────────────────────────────────────────────────────

// SnipPlugin is the contract that every snip type plugin must satisfy.
//
// Lifecycle:
//
//	Init → Start → (event callbacks) → Stop
//
// Init is called once at startup to supply dependencies. Start and Stop
// bracket the active serving period. All event callbacks are called
// synchronously on the goroutine that processes the triggering operation;
// plugins must not perform blocking I/O without proper timeouts.
type SnipPlugin interface {
	// ── Identity ─────────────────────────────────────────────────────────────

	// Type returns the canonical snip_type string this plugin handles
	// (e.g. "todo", "bash_history"). Must be unique across all registered
	// plugins and must match the snip_type values stored on Note.SnipType.
	Type() string

	// Version returns the semantic version string of this plugin
	// (e.g. "1.0.0").
	Version() string

	// Description returns a short human-readable description of what this
	// snip type represents.
	Description() string

	// Schema returns the structural declaration for this snip type. The engine
	// calls Schema once during Init; the return value must be stable.
	Schema() SnipSchema

	// ── Lifecycle ─────────────────────────────────────────────────────────────

	// Init supplies the plugin with its runtime dependencies. Called once
	// before Start. The plugin should create any required index tables here.
	Init(ctx context.Context, env PluginEnv) error

	// Start begins background work (timers, watchers, etc.). Called after Init
	// once the engine is ready to serve requests.
	Start(ctx context.Context) error

	// Stop gracefully shuts down background work started by Start. The plugin
	// must not use any PluginEnv fields after Stop returns.
	Stop(ctx context.Context) error

	// ── Event callbacks ───────────────────────────────────────────────────────

	// OnNoteCreated is called immediately after a snip of this plugin's type
	// is persisted for the first time.
	OnNoteCreated(ctx context.Context, note *core.Note) error

	// OnEventAppended is called immediately after an event is appended to a
	// snip of this plugin's type.
	OnEventAppended(ctx context.Context, note *core.Note, event *core.Event) error

	// OnNoteDeleted is called immediately after a snip of this plugin's type
	// is soft-deleted. The plugin should clean up any derived index rows.
	OnNoteDeleted(ctx context.Context, noteURN core.URN) error

	// OnParentAnchorBroken is called when the anchor that a sidecar snip is
	// bound to is removed from its parent note. The plugin decides how to
	// handle the orphaned snip (e.g. detach, archive, or delete it).
	OnParentAnchorBroken(ctx context.Context, noteURN core.URN) error

	// ── Transport registration ────────────────────────────────────────────────

	// RegisterHTTP registers any HTTP handlers provided by this plugin onto
	// the shared ServeMux. The middleware parameter is the engine's standard
	// auth/logging middleware wrapper and should be applied to all handlers.
	// Called once after Init.
	RegisterHTTP(mux *http.ServeMux, middleware func(http.HandlerFunc) http.HandlerFunc)
}
