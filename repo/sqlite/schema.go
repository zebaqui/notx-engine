package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// currentSchemaVersion is incremented every time a new migration is added.
// Migrations are additive only: new columns, new tables.
const currentSchemaVersion = 1

// currentProjectionVersion is incremented when the projection logic changes
// (i.e. when existing SQLite rows need to be recomputed from the event log
// even though the schema itself has not changed).
const currentProjectionVersion = 1

// ddl contains every CREATE TABLE and CREATE INDEX statement for the schema.
// Executed once when the database is first created.
const ddl = `
-- Materialized note state derived from .notx event logs.
CREATE TABLE IF NOT EXISTS notes (
    urn               TEXT    PRIMARY KEY,
    project_urn       TEXT    NOT NULL DEFAULT '',
    folder_urn        TEXT    NOT NULL DEFAULT '',
    note_type         TEXT    NOT NULL DEFAULT 'normal',
    title             TEXT    NOT NULL DEFAULT '',
    preview           TEXT    NOT NULL DEFAULT '',
    head_seq          INTEGER NOT NULL DEFAULT 0,
    deleted           INTEGER NOT NULL DEFAULT 0,
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL,
    extra             TEXT    NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_notes_updated ON notes(updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_notes_project ON notes(project_urn, deleted, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_notes_folder  ON notes(folder_urn,  deleted, updated_at DESC);

-- Full-text search over normal note content.
-- Each row is one note; content is the full materialised text.
CREATE VIRTUAL TABLE IF NOT EXISTS notes_fts USING fts5(
    urn    UNINDEXED,
    title,
    body,
    content='notes',
    content_rowid='rowid'
);

-- Projects (lightweight index-only entities).
CREATE TABLE IF NOT EXISTS projects (
    urn        TEXT    PRIMARY KEY,
    name       TEXT    NOT NULL,
    deleted    INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- Folders (sub-grouping entities inside a project).
CREATE TABLE IF NOT EXISTS folders (
    urn         TEXT    PRIMARY KEY,
    project_urn TEXT    NOT NULL,
    name        TEXT    NOT NULL,
    deleted     INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

-- Users.
CREATE TABLE IF NOT EXISTS users (
    urn          TEXT    PRIMARY KEY,
    display_name TEXT    NOT NULL DEFAULT '',
    email        TEXT    NOT NULL DEFAULT '',
    deleted      INTEGER NOT NULL DEFAULT 0,
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);

-- Devices.
CREATE TABLE IF NOT EXISTS devices (
    urn             TEXT    PRIMARY KEY,
    name            TEXT    NOT NULL DEFAULT '',
    owner_urn       TEXT    NOT NULL DEFAULT '',
    public_key_b64  TEXT    NOT NULL DEFAULT '',
    role            TEXT    NOT NULL DEFAULT 'client',
    approval_status TEXT    NOT NULL DEFAULT 'pending',
    revoked         INTEGER NOT NULL DEFAULT 0,
    registered_at   INTEGER NOT NULL,
    last_seen_at    INTEGER NOT NULL DEFAULT 0
);

-- Servers (paired authority peers).
CREATE TABLE IF NOT EXISTS servers (
    urn           TEXT    PRIMARY KEY,
    name          TEXT    NOT NULL DEFAULT '',
    endpoint      TEXT    NOT NULL DEFAULT '',
    cert_pem      TEXT    NOT NULL DEFAULT '',
    cert_serial   TEXT    NOT NULL DEFAULT '',
    revoked       INTEGER NOT NULL DEFAULT 0,
    registered_at INTEGER NOT NULL,
    expires_at    INTEGER NOT NULL DEFAULT 0,
    last_seen_at  INTEGER NOT NULL DEFAULT 0
);

-- Pairing secrets (authority-side, single-use tokens).
CREATE TABLE IF NOT EXISTS pairing_secrets (
    id           TEXT    PRIMARY KEY,
    label        TEXT    NOT NULL DEFAULT '',
    hash_bcrypt  TEXT    NOT NULL,
    expires_at   INTEGER NOT NULL,
    used_at      INTEGER         -- NULL means not yet used
);

-- Note content cache (materialised plaintext for FTS and fast reads).
-- Separate table so secure note content is never stored here.
CREATE TABLE IF NOT EXISTS note_content (
    urn     TEXT PRIMARY KEY,
    content TEXT NOT NULL DEFAULT ''
);

-- Schema version tracking.
CREATE TABLE IF NOT EXISTS schema_version (
    version    INTEGER PRIMARY KEY,
    applied_at INTEGER NOT NULL
);

-- Projection version tracking.
-- Incremented when projection logic changes, forcing a full rebuild.
CREATE TABLE IF NOT EXISTS projection_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
`

// migrations holds the ordered set of additive SQL migrations.
// Index 0 is migration 1, index 1 is migration 2, etc.
var migrations = []string{
	// Migration 1: initial schema. Already applied via ddl above.
	// Listed here so schema_version tracking is consistent.
	"SELECT 1", // no-op; schema already created by ddl
}

// applySchema creates all tables/indexes and seeds the meta rows if they do
// not already exist. It is idempotent and safe to call on every startup.
func applySchema(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("sqlite schema: create tables: %w", err)
	}

	// Seed projection_meta if absent.
	_, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO projection_meta(key, value) VALUES ('projection_version', ?)`,
		fmt.Sprintf("%d", currentProjectionVersion),
	)
	if err != nil {
		return fmt.Errorf("sqlite schema: seed projection_meta: %w", err)
	}
	return nil
}

// schemaVersion returns the highest migration version recorded in
// schema_version, or 0 if the table is empty.
func schemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var v int
	err := db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM schema_version`,
	).Scan(&v)
	if err != nil {
		return 0, fmt.Errorf("sqlite schema: read version: %w", err)
	}
	return v, nil
}

// projectionVersion returns the projection logic version recorded in
// projection_meta, or 0 if absent.
func projectionVersion(ctx context.Context, db *sql.DB) (int, error) {
	var v string
	err := db.QueryRowContext(ctx,
		`SELECT value FROM projection_meta WHERE key = 'projection_version'`,
	).Scan(&v)
	if err != nil {
		return 0, fmt.Errorf("sqlite schema: read projection_version: %w", err)
	}
	n := 0
	for _, c := range v {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n, nil
}

// runMigrations applies any migrations whose version > appliedVersion.
func runMigrations(ctx context.Context, db *sql.DB, appliedVersion int) error {
	for i, sql := range migrations {
		version := i + 1
		if version <= appliedVersion {
			continue
		}
		if _, err := db.ExecContext(ctx, sql); err != nil {
			return fmt.Errorf("sqlite schema: migration %d: %w", version, err)
		}
		now := time.Now().UnixMilli()
		if _, err := db.ExecContext(ctx,
			`INSERT OR REPLACE INTO schema_version(version, applied_at) VALUES (?, ?)`,
			version, now,
		); err != nil {
			return fmt.Errorf("sqlite schema: record migration %d: %w", version, err)
		}
	}
	return nil
}

// setProjectionVersion updates the stored projection version after a rebuild.
func setProjectionVersion(ctx context.Context, db *sql.DB, v int) error {
	_, err := db.ExecContext(ctx,
		`INSERT OR REPLACE INTO projection_meta(key, value) VALUES ('projection_version', ?)`,
		fmt.Sprintf("%d", v),
	)
	if err != nil {
		return fmt.Errorf("sqlite schema: set projection_version: %w", err)
	}
	return nil
}

// integrityOK runs PRAGMA integrity_check and returns true if the database
// has no corruption.
func integrityOK(ctx context.Context, db *sql.DB) bool {
	rows, err := db.QueryContext(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			return false
		}
		if result != "ok" {
			return false
		}
	}
	return rows.Err() == nil
}
