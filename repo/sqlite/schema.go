package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// currentSchemaVersion must match len(migrations).
// Bump it by 1 every time you add a migration to the slice below.
// NEVER edit or remove existing migrations — only append new ones.
const currentSchemaVersion = 7

// currentProjectionVersion is incremented when projection logic changes
// (i.e. existing rows need recomputing from the event log even though
// the schema itself has not changed).
const currentProjectionVersion = 1

// ddl is the baseline schema applied on first install (empty DB).
// It must always represent the fully up-to-date schema so that fresh
// installs skip all migrations.
// RULE: every structural change goes BOTH here (for new installs) AND
// as a migration below (for existing installs). Keep them in sync.
const ddl = `
-- Materialized note state derived from the event log.
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

-- Event log: one row per appended event on a note.
CREATE TABLE IF NOT EXISTS events (
    note_urn   TEXT    NOT NULL,
    sequence   INTEGER NOT NULL,
    author_urn TEXT    NOT NULL DEFAULT '',
    label      TEXT    NOT NULL DEFAULT '',
    payload    TEXT    NOT NULL DEFAULT '[]',
    created_at INTEGER NOT NULL,
    PRIMARY KEY (note_urn, sequence)
);

CREATE INDEX IF NOT EXISTS idx_events_note ON events(note_urn, sequence ASC);

-- Note content cache (materialised plaintext for FTS and fast reads).
-- Separate table so secure note content is never stored here.
CREATE TABLE IF NOT EXISTS note_content (
    urn     TEXT PRIMARY KEY,
    content TEXT NOT NULL DEFAULT ''
);

-- Full-text search over normal note content.
-- Standalone FTS5 table — no content= backing table.
-- Rows are inserted/updated explicitly by the provider on every AppendEvent.
CREATE VIRTUAL TABLE IF NOT EXISTS notes_fts USING fts5(
    urn    UNINDEXED,
    title,
    body
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

-- ── Link Spec: Anchor index ──────────────────────────────────────────────────

-- Server-side anchor index for fast cross-note lookups.
CREATE TABLE IF NOT EXISTS anchors (
    note_urn    TEXT    NOT NULL,
    anchor_id   TEXT    NOT NULL,
    line        INTEGER NOT NULL,
    char_start  INTEGER NOT NULL DEFAULT 0,
    char_end    INTEGER NOT NULL DEFAULT 0,
    preview     TEXT    NOT NULL DEFAULT '',
    status      TEXT    NOT NULL DEFAULT 'ok',
    updated_at  TEXT    NOT NULL,
    PRIMARY KEY (note_urn, anchor_id)
);

CREATE INDEX IF NOT EXISTS idx_anchors_note ON anchors(note_urn);

-- Server-side backlink index.
CREATE TABLE IF NOT EXISTS backlinks (
    source_urn     TEXT NOT NULL,
    target_urn     TEXT NOT NULL,
    target_anchor  TEXT NOT NULL,
    label          TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL,
    PRIMARY KEY (source_urn, target_urn, target_anchor)
);

CREATE INDEX IF NOT EXISTS idx_backlinks_target ON backlinks(target_urn, target_anchor);
CREATE INDEX IF NOT EXISTS idx_backlinks_source ON backlinks(source_urn);

-- External links index.
CREATE TABLE IF NOT EXISTS external_links (
    source_urn TEXT NOT NULL,
    uri        TEXT NOT NULL,
    label      TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    PRIMARY KEY (source_urn, uri)
);

CREATE INDEX IF NOT EXISTS idx_external_links_source ON external_links(source_urn);

-- ── Context Graph Layer ───────────────────────────────────────────────────────

-- Context bursts: one or more per event, one per contiguous changed-line window.
CREATE TABLE IF NOT EXISTS context_bursts (
    id          TEXT    PRIMARY KEY,
    note_urn    TEXT    NOT NULL,
    project_urn TEXT    NOT NULL DEFAULT '',
    folder_urn  TEXT    NOT NULL DEFAULT '',
    author_urn  TEXT    NOT NULL DEFAULT '',
    sequence    INTEGER NOT NULL,
    line_start  INTEGER NOT NULL,
    line_end    INTEGER NOT NULL,
    text        TEXT    NOT NULL,
    tokens      TEXT    NOT NULL DEFAULT '',
    truncated   INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_bursts_note    ON context_bursts(note_urn, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_bursts_project ON context_bursts(project_urn, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_bursts_created ON context_bursts(created_at DESC);

-- FTS5 over burst tokens (used by background BM25 scorer).
CREATE VIRTUAL TABLE IF NOT EXISTS context_bursts_fts USING fts5(
    id          UNINDEXED,
    note_urn    UNINDEXED,
    project_urn UNINDEXED,
    tokens,
    content='context_bursts',
    content_rowid='rowid'
);

-- Candidate relations: burst pairs from different notes that may be connected.
CREATE TABLE IF NOT EXISTS candidate_relations (
    id            TEXT    PRIMARY KEY,
    burst_a_id    TEXT    NOT NULL,
    burst_b_id    TEXT    NOT NULL,
    note_urn_a    TEXT    NOT NULL,
    note_urn_b    TEXT    NOT NULL,
    project_urn   TEXT    NOT NULL DEFAULT '',
    overlap_score REAL    NOT NULL,
    bm25_score    REAL    NOT NULL DEFAULT 0,
    status        TEXT    NOT NULL DEFAULT 'pending',
    created_at    INTEGER NOT NULL,
    reviewed_at   INTEGER,
    reviewed_by   TEXT    NOT NULL DEFAULT '',
    promoted_link TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_candidates_project_status
    ON candidate_relations(project_urn, status, bm25_score DESC, overlap_score DESC);
CREATE INDEX IF NOT EXISTS idx_candidates_notes
    ON candidate_relations(note_urn_a, note_urn_b, status);
CREATE INDEX IF NOT EXISTS idx_candidates_burst_a ON candidate_relations(burst_a_id);
CREATE INDEX IF NOT EXISTS idx_candidates_burst_b ON candidate_relations(burst_b_id);

-- Per-project context graph configuration overrides.
CREATE TABLE IF NOT EXISTS project_context_config (
    project_urn                   TEXT    PRIMARY KEY,
    burst_max_per_note_per_day    INTEGER,
    burst_max_per_project_per_day INTEGER,
    updated_at                    INTEGER NOT NULL
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

// migrations is the ordered list of additive SQL migrations.
//
// Rules:
//  1. NEVER edit or remove an existing entry — existing DBs have already run them.
//  2. ALWAYS append new entries at the end.
//  3. Bump currentSchemaVersion to match len(migrations) when you add one.
//  4. Mirror every structural change in ddl above so fresh installs are identical.
//
// Migration history:
//
//	v1 — initial schema (tables created via ddl; this is a no-op for existing DBs)
//	v2 — add events table and replace content-backed notes_fts with standalone FTS5
//	v3 — add anchors, backlinks tables (link spec)
//	v4 — add external_links table (link spec)
//	v5 — add context_bursts, context_bursts_fts, candidate_relations, project_context_config (context graph)
var migrations = []string{
	// v1: initial schema — ddl already handles this on new installs.
	"SELECT 1",

	// v2: add the events table (was missing from the original schema).
	//     Drop and recreate notes_fts as a standalone table so it no longer
	//     uses content='note_content' which referenced non-existent columns.
	`CREATE TABLE IF NOT EXISTS events (
		note_urn   TEXT    NOT NULL,
		sequence   INTEGER NOT NULL,
		author_urn TEXT    NOT NULL DEFAULT '',
		label      TEXT    NOT NULL DEFAULT '',
		payload    TEXT    NOT NULL DEFAULT '[]',
		created_at INTEGER NOT NULL,
		PRIMARY KEY (note_urn, sequence)
	);
	CREATE INDEX IF NOT EXISTS idx_events_note ON events(note_urn, sequence ASC);
	DROP TABLE IF EXISTS notes_fts;
	CREATE VIRTUAL TABLE IF NOT EXISTS notes_fts USING fts5(
		urn    UNINDEXED,
		title,
		body
	);`,

	// v3: link spec — anchors, backlinks tables
	`CREATE TABLE IF NOT EXISTS anchors (
    note_urn    TEXT    NOT NULL,
    anchor_id   TEXT    NOT NULL,
    line        INTEGER NOT NULL,
    char_start  INTEGER NOT NULL DEFAULT 0,
    char_end    INTEGER NOT NULL DEFAULT 0,
    preview     TEXT    NOT NULL DEFAULT '',
    status      TEXT    NOT NULL DEFAULT 'ok',
    updated_at  TEXT    NOT NULL,
    PRIMARY KEY (note_urn, anchor_id)
);
CREATE INDEX IF NOT EXISTS idx_anchors_note ON anchors(note_urn);
CREATE TABLE IF NOT EXISTS backlinks (
    source_urn     TEXT NOT NULL,
    target_urn     TEXT NOT NULL,
    target_anchor  TEXT NOT NULL,
    label          TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL,
    PRIMARY KEY (source_urn, target_urn, target_anchor)
);
CREATE INDEX IF NOT EXISTS idx_backlinks_target ON backlinks(target_urn, target_anchor);
CREATE INDEX IF NOT EXISTS idx_backlinks_source ON backlinks(source_urn);`,

	// v4: link spec — external_links table
	`CREATE TABLE IF NOT EXISTS external_links (
    source_urn TEXT NOT NULL,
    uri        TEXT NOT NULL,
    label      TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    PRIMARY KEY (source_urn, uri)
);
CREATE INDEX IF NOT EXISTS idx_external_links_source ON external_links(source_urn);`,

	// v5: context graph layer tables
	`CREATE TABLE IF NOT EXISTS context_bursts (
    id          TEXT    PRIMARY KEY,
    note_urn    TEXT    NOT NULL,
    project_urn TEXT    NOT NULL DEFAULT '',
    folder_urn  TEXT    NOT NULL DEFAULT '',
    author_urn  TEXT    NOT NULL DEFAULT '',
    sequence    INTEGER NOT NULL,
    line_start  INTEGER NOT NULL,
    line_end    INTEGER NOT NULL,
    text        TEXT    NOT NULL,
    tokens      TEXT    NOT NULL DEFAULT '',
    truncated   INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_bursts_note    ON context_bursts(note_urn, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_bursts_project ON context_bursts(project_urn, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_bursts_created ON context_bursts(created_at DESC);
CREATE VIRTUAL TABLE IF NOT EXISTS context_bursts_fts USING fts5(
    id          UNINDEXED,
    note_urn    UNINDEXED,
    project_urn UNINDEXED,
    tokens,
    content='context_bursts',
    content_rowid='rowid'
);
CREATE TABLE IF NOT EXISTS candidate_relations (
    id            TEXT    PRIMARY KEY,
    burst_a_id    TEXT    NOT NULL,
    burst_b_id    TEXT    NOT NULL,
    note_urn_a    TEXT    NOT NULL,
    note_urn_b    TEXT    NOT NULL,
    project_urn   TEXT    NOT NULL DEFAULT '',
    overlap_score REAL    NOT NULL,
    bm25_score    REAL    NOT NULL DEFAULT 0,
    status        TEXT    NOT NULL DEFAULT 'pending',
    created_at    INTEGER NOT NULL,
    reviewed_at   INTEGER,
    reviewed_by   TEXT    NOT NULL DEFAULT '',
    promoted_link TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_candidates_project_status
    ON candidate_relations(project_urn, status, bm25_score DESC, overlap_score DESC);
CREATE INDEX IF NOT EXISTS idx_candidates_notes
    ON candidate_relations(note_urn_a, note_urn_b, status);
CREATE INDEX IF NOT EXISTS idx_candidates_burst_a ON candidate_relations(burst_a_id);
CREATE INDEX IF NOT EXISTS idx_candidates_burst_b ON candidate_relations(burst_b_id);
CREATE TABLE IF NOT EXISTS project_context_config (
    project_urn                   TEXT    PRIMARY KEY,
    burst_max_per_note_per_day    INTEGER,
    burst_max_per_project_per_day INTEGER,
    updated_at                    INTEGER NOT NULL
);`,

	// v6: no-op — superseded by v7 which handles idempotent column addition.
	// Kept as a placeholder so existing DBs that partially applied v6 are
	// not broken (runMigrations skips versions already recorded).
	`SELECT 1`,

	// v7: add unique index on normalized burst pair (min,max) so that
	// INSERT OR IGNORE deduplicates candidates regardless of which note
	// triggered detection first.  The pair_key column stores
	// min(burst_a_id,burst_b_id)||':'||max(burst_a_id,burst_b_id) and is
	// populated by all insertion sites in Go before the INSERT.
	//
	// The ALTER TABLE is skipped when pair_key already exists (idempotent).
	// Steps:
	//   1. Add pair_key column if it does not already exist.
	//   2. Populate pair_key for every row where it is still empty.
	//   3. Delete the lower-priority duplicate within each pair — keep the
	//      row with the highest overlap_score (or earliest id on a tie).
	//   4. Create the unique index (IF NOT EXISTS — safe to re-run).
	`UPDATE candidate_relations
    SET pair_key = CASE
        WHEN burst_a_id <= burst_b_id
            THEN burst_a_id || ':' || burst_b_id
        ELSE burst_b_id || ':' || burst_a_id
    END
    WHERE pair_key = '' OR pair_key IS NULL;
DELETE FROM candidate_relations
    WHERE id IN (
        SELECT r.id
        FROM candidate_relations r
        JOIN candidate_relations k
          ON  k.pair_key = r.pair_key
          AND k.id != r.id
          AND (
                k.overlap_score > r.overlap_score
             OR (k.overlap_score = r.overlap_score AND k.id < r.id)
          )
    );
CREATE UNIQUE INDEX IF NOT EXISTS idx_candidates_pair_key
    ON candidate_relations(pair_key);`,
}

// applySchema creates all tables/indexes on a fresh DB and seeds meta rows.
// Safe to call on every startup — all statements use IF NOT EXISTS.
func applySchema(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("sqlite schema: create tables: %w", err)
	}
	_, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO projection_meta(key, value) VALUES ('projection_version', ?)`,
		fmt.Sprintf("%d", currentProjectionVersion),
	)
	if err != nil {
		return fmt.Errorf("sqlite schema: seed projection_meta: %w", err)
	}
	return nil
}

// schemaVersion returns the highest applied migration version, or 0.
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

// projectionVersion returns the stored projection logic version, or 0.
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
	for i, sqlStmt := range migrations {
		version := i + 1
		if version <= appliedVersion {
			continue
		}

		// v7 requires pair_key column to exist before running its SQL.
		// We add it here with a Go-level existence check so the ALTER is
		// idempotent even when a previous boot partially applied v6/v7.
		if version == 7 {
			if err := ensurePairKeyColumn(ctx, db); err != nil {
				return fmt.Errorf("sqlite schema: migration %d pre-step: %w", version, err)
			}
		}

		if _, err := db.ExecContext(ctx, sqlStmt); err != nil {
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

// ensurePairKeyColumn adds the pair_key column to candidate_relations if it
// does not already exist. Safe to call multiple times.
func ensurePairKeyColumn(ctx context.Context, db *sql.DB) error {
	// Check whether the column already exists by inspecting table_info.
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(candidate_relations)`)
	if err != nil {
		return fmt.Errorf("pragma table_info: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, colType, notNull, dfltValue, pk sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("scan table_info: %w", err)
		}
		if name.String == "pair_key" {
			return nil // already exists
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Column does not exist — add it.
	_, err = db.ExecContext(ctx,
		`ALTER TABLE candidate_relations ADD COLUMN pair_key TEXT NOT NULL DEFAULT ''`)
	if err != nil {
		return fmt.Errorf("alter table add pair_key: %w", err)
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

// integrityOK runs PRAGMA integrity_check and returns true if the DB is clean.
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
