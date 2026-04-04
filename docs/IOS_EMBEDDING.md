# notx Mobile Embedding — Design & Integration Reference

> **Status**: Implemented — v2 architecture.
> Covers the Go engine architecture for mobile platforms and the complete
> Swift integration API for iOS. The `Platform` interface is designed to be
> portable; future platform targets (e.g. Android / Kotlin) follow the same
> contract with a platform-native implementation.

---

## Table of Contents

1. [Overview](#1-overview)
2. [Vocabulary](#2-vocabulary)
3. [Architecture](#3-architecture)
   - 3.1 [Responsibility Boundary](#31-responsibility-boundary)
   - 3.2 [Go Package Structure](#32-go-package-structure)
   - 3.3 [Data Flow Diagram](#33-data-flow-diagram)
4. [The Platform Interface (Go)](#4-the-platform-interface-go)
   - 4.1 [Key Operations](#41-key-operations)
   - 4.2 [Certificate Operations](#42-certificate-operations)
   - 4.3 [Configuration Operations](#43-configuration-operations)
   - 4.4 [Storage Location](#44-storage-location)
5. [Well-Known Aliases and Config Keys](#5-well-known-aliases-and-config-keys)
6. [Storage Model](#6-storage-model)
   - 6.1 [Source of Truth — `.notx` Event Log](#61-source-of-truth--notx-event-log)
   - 6.2 [Persistent Projection — SQLite](#62-persistent-projection--sqlite)
   - 6.3 [SQLite Schema](#63-sqlite-schema)
   - 6.4 [EnsureIndex Startup Sequence](#64-ensureindex-startup-sequence)
   - 6.5 [Projection Consistency Guarantees](#65-projection-consistency-guarantees)
   - 6.6 [Migration Strategy](#66-migration-strategy)
7. [Pairing Flow](#7-pairing-flow)
   - 7.1 [Why the Token Never Enters Go](#71-why-the-token-never-enters-go)
   - 7.2 [Initial Pairing](#72-initial-pairing)
   - 7.3 [Cert Renewal](#73-cert-renewal)
8. [gomobile Build Configuration](#8-gomobile-build-configuration)
9. [iOS Integration Guide (Swift)](#9-ios-integration-guide-swift)
   - 9.1 [Project Setup](#91-project-setup)
   - 9.2 [iOSPlatform — Key Operations](#92-iosplatform--key-operations)
   - 9.3 [iOSPlatform — Certificate Operations](#93-iosplatform--certificate-operations)
   - 9.4 [iOSPlatform — Configuration](#94-iosplatform--configuration)
   - 9.5 [iOSPlatform — Storage Location](#95-iosplatform--storage-location)
   - 9.6 [PairingCoordinator](#96-pairingcoordinator)
   - 9.7 [NotxEngineHost — Entry Point](#97-notxenginehost--entry-point)
   - 9.8 [File Protection and Backup Policy](#98-file-protection-and-backup-policy)
   - 9.9 [Background Tasks](#99-background-tasks)
   - 9.10 [Error Handling](#910-error-handling)
10. [iOS Storage Layout](#10-ios-storage-layout)
11. [Security Guarantees](#11-security-guarantees)
12. [SQLite Lifecycle Constraints on iOS](#12-sqlite-lifecycle-constraints-on-ios)
13. [Impact on Existing Desktop Code](#13-impact-on-existing-desktop-code)
14. [Open Questions](#14-open-questions)

---

## 1. Overview

The notx engine is written in Go. On mobile platforms it is embedded via
`gomobile bind`, which produces a native framework from a single exported Go
package (`mobile/`). The host application implements the `Platform` interface
in its native language and passes it to `Engine.New()` at startup. From that
point on, the Go engine handles all note storage, projection, and sync logic
while the host handles every platform-specific concern: key generation, signing,
certificate storage, configuration persistence, and the pairing RPC.

**Core design principles:**

- The Go engine contains **zero platform-specific code**. It imports no Apple
  SDK, no Android SDK, and makes no assumptions about the underlying OS.
- All security-sensitive operations (key generation, CSR signing, pairing token
  handling) are owned by the host platform. The Go engine is deliberately kept
  out of these flows.
- The pairing token **never crosses the Go/Swift bridge** in any form. It lives
  in the Keychain from the moment it is scanned and is deleted immediately after
  the pairing RPC completes — entirely in Swift.
- SQLite replaces Badger as the persistent projection layer on all mobile
  targets. It is crash-safe, lifecycle-aware, and a first-class embedded
  database on every mobile platform.

---

## 2. Vocabulary

| Term                 | Meaning                                                                                                 |
| -------------------- | ------------------------------------------------------------------------------------------------------- |
| **Platform**         | The Go interface (`mobile/platform.go`) that abstracts every OS-specific operation the engine needs     |
| **Platform impl**    | The host-language implementation of `Platform` (Swift on iOS, Kotlin on Android)                        |
| **gomobile bind**    | Go toolchain command that produces a native framework from a `gomobile`-compatible Go package           |
| **xcframework**      | Multi-architecture Apple framework bundle imported by Swift/Objective-C projects                        |
| **Secure Enclave**   | Apple's dedicated security processor; private keys stored here are hardware-bound and non-exportable    |
| **Keychain**         | Apple's system-level encrypted credential store, ACL-controlled per item                                |
| **App Group**        | iOS/macOS capability that lets multiple targets share a Keychain access group and container directory   |
| **Data Protection**  | iOS per-file encryption classes (`NSFileProtectionComplete`, etc.)                                      |
| **Pairing Token**    | The `NTXP-…` one-time secret used to bootstrap trust between the mobile client and the authority server |
| **Device Identity**  | The `urn:notx:device:<id>` URN + EC P-256 key pair that represents this device installation             |
| **Event Log**        | The `.notx` append-only file — the canonical portable source of truth for note content                  |
| **Projection Layer** | Go code that reads the event log and materialises note state into SQLite                                |
| **EnsureIndex**      | The deterministic startup sequence that opens or rebuilds the SQLite database                           |
| **Active Key Alias** | The config-stored string identifying which versioned Keychain key is currently in use                   |

---

## 3. Architecture

### 3.1 Responsibility Boundary

The Swift/Go bridge is a hard security and ownership line:

| Concern                          | Owner     | Rationale                                                                       |
| -------------------------------- | --------- | ------------------------------------------------------------------------------- |
| Pairing token lifecycle          | **Swift** | Must never enter Go memory; stored in Keychain immediately after scan           |
| Key generation (Secure Enclave)  | **Swift** | SE key references cannot be serialised across the bridge                        |
| CSR construction and signing     | **Swift** | Requires direct access to the SE key reference                                  |
| Initial pairing RPC              | **Swift** | Token and key handling must stay co-located                                     |
| Cert and token Keychain storage  | **Swift** | Keychain APIs are Apple-only                                                    |
| Note projection (event → SQLite) | **Go**    | Platform-agnostic business logic                                                |
| Sync logic                       | **Go**    | Platform-agnostic                                                               |
| Cert renewal orchestration       | **Go**    | Orchestrated by Go; signed by Swift via `Platform.BuildCSR`                     |
| Config persistence               | **Go**    | Calls `Platform.GetConfig` / `SetConfig`; Go never writes config files directly |

> **Hard rule**: Go must never construct a CSR, hold private key bytes, or
> receive the pairing token in any form. The layer that owns the private key
> (Secure Enclave / Swift) must own CSR construction and signing for the entire
> lifetime of the device identity.

### 3.2 Go Package Structure

```
notx-engine/
└── mobile/
    ├── engine.go       ← Engine struct — the single exported entry point
    ├── platform.go     ← Platform interface — the only coupling point to the OS
    ├── aliases.go      ← Well-known alias and config key constants
    ├── types.go        ← gomobile-compatible value types (NoteHeader, ListOptions, …)
    └── errors.go       ← Error code constants and sentinel errors
```

`gomobile bind` exports only the `mobile/` package. All other packages
(`repo/sqlite/`, `core/`, `ca/`, etc.) are internal Go and are never
directly visible to Swift.

**gomobile bind restrictions** that shape the entire API design:

- Exported method parameters must be Go primitive types, `[]byte`, or types
  declared in the bound package
- Interfaces implemented in Swift and passed into Go must be declared in the
  bound package — hence `Platform` lives in `mobile/`
- No `map` types in exported signatures
- No multiple return values beyond `(T, error)` or bare `error`
- `gomobile` automatically bridges the `(T, error)` pattern to Swift `throws`
- Slice element types in exported signatures must be pointer types

### 3.3 Data Flow Diagram

```
┌────────────────────────────────────────────────────────────────────┐
│                       iOS Application (Swift)                      │
│                                                                    │
│  ┌─────────────────────────┐   ┌──────────────────────────────┐   │
│  │    NotxEngineHost       │   │       iOSPlatform            │   │
│  │    (thin façade)        │   │                              │   │
│  │                         │   │  GenerateKey → Secure Enclave│   │
│  │  host.receivePairing()  │   │  Sign        → Secure Enclave│   │
│  │  host.connect()         │   │  BuildCSR    → Secure Enclave│   │
│  │  host.listNotes()       │   │  StoreCert   → Keychain      │   │
│  │  host.renewIfNeeded()   │   │  LoadCert    → Keychain      │   │
│  │                         │   │  GetConfig   → NSUserDefaults│   │
│  │  ┌─────────────────┐    │   │  DataDir     → App sandbox   │   │
│  │  │PairingCoordinator│   │   │                              │   │
│  │  │ (entirely Swift) │   │   │  [token stays here, in       │   │
│  │  │ token ─► Keychain│   │   │   Keychain, never crosses   │   │
│  │  │ CSR ─► SE sign   │   │   │   to Go]                    │   │
│  │  │ RPC ─► gRPC      │   │   │                              │   │
│  │  └─────────────────┘    │   └──────────────┬───────────────┘   │
│  └────────────┬────────────┘                  │ (passed at init)  │
└───────────────┼───────────────────────────────┼───────────────────┘
                │ gomobile bridge                │
                ▼                               ▼
┌────────────────────────────────────────────────────────────────────┐
│                  notx Go Engine (.xcframework)                     │
│                                                                    │
│  ┌────────────────────────────────────────────────────────────┐   │
│  │  mobile/engine.go                                          │   │
│  │  Engine — coordinator, holds Platform impl reference       │   │
│  └────────────────────────────────────────────────────────────┘   │
│                                                                    │
│  ┌──────────────┐  ┌───────────────────┐  ┌──────────────────┐   │
│  │ mobile/      │  │ repo/sqlite/      │  │ core / ca /      │   │
│  │ platform.go  │  │ provider.go       │  │ internal/…       │   │
│  │ (interface)  │  │ schema.go         │  │ (unchanged)      │   │
│  │              │  │ query.go          │  │                  │   │
│  └──────────────┘  └───────────────────┘  └──────────────────┘   │
│                                                                    │
│  ┌────────────────────────────────────────────────────────────┐   │
│  │  <DataDir>/notes/*.notx   +   <DataDir>/index.db (SQLite) │   │
│  └────────────────────────────────────────────────────────────┘   │
└────────────────────────────────────────────────────────────────────┘
```

---

## 4. The Platform Interface (Go)

Defined in `mobile/platform.go`. This is the **only** coupling point between
the Go engine and the host operating system. Implement it in Swift (iOS) or
Kotlin (Android) and pass the implementation to `Engine.New()` at startup.

All methods must be safe to call from multiple goroutines.

```go
// mobile/platform.go

// Platform abstracts every platform-specific operation the engine needs.
// Implement this in your host language and pass it to Engine.New() at startup.
// All methods must be goroutine-safe.
type Platform interface {

    // ── Key operations ───────────────────────────────────────────────────

    // GenerateKey creates a new EC P-256 key pair identified by alias and
    // returns the DER-encoded public key. On iOS the private key is created
    // inside the Secure Enclave and never leaves it. The alias is a stable
    // string identifier (see §5); if a key already exists under this alias
    // it must be deleted first.
    GenerateKey(alias string) ([]byte, error)

    // Sign produces an ECDSA signature over digest using the private key for
    // alias. On iOS this executes entirely inside the Secure Enclave.
    // Used only during cert renewal — Go provides the to-be-signed digest
    // and receives the raw signature bytes. Never use this to sign a CSR;
    // use BuildCSR for that.
    Sign(alias string, digest []byte) ([]byte, error)

    // BuildCSR constructs and signs a PKCS#10 Certificate Signing Request for
    // the key identified by alias using the platform's secure key reference.
    // Returns DER-encoded CSR bytes. Go treats the result as opaque — it
    // must not parse, inspect, or modify the bytes.
    // This is the only sanctioned path for CSR production; Go never
    // assembles a CSR itself.
    BuildCSR(alias string, commonName string) ([]byte, error)

    // PublicKeyDER returns the DER-encoded SubjectPublicKeyInfo for alias.
    PublicKeyDER(alias string) ([]byte, error)

    // DeleteKey removes all key material for alias. No-op if absent.
    DeleteKey(alias string) error

    // HasKey reports whether a key for alias exists.
    HasKey(alias string) (bool, error)

    // ── Certificate operations ───────────────────────────────────────────

    // StoreCert saves a PEM-encoded certificate under alias.
    // On iOS: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly.
    StoreCert(alias string, certPEM []byte) error

    // LoadCert returns the PEM-encoded certificate for alias.
    // Returns an error wrapping ErrNotFound if absent.
    LoadCert(alias string) ([]byte, error)

    // DeleteCert removes the certificate for alias. No-op if absent.
    DeleteCert(alias string) error

    // HasCert reports whether a cert for alias exists.
    HasCert(alias string) (bool, error)

    // ── Configuration ────────────────────────────────────────────────────

    // GetConfig returns the string value for key.
    // Returns ("", nil) if the key does not exist.
    GetConfig(key string) (string, error)

    // SetConfig persists a key-value configuration pair.
    SetConfig(key, value string) error

    // ── Storage location ─────────────────────────────────────────────────

    // DataDir returns the root directory where the engine stores note files
    // and index.db. The path must already exist and be writable.
    // On iOS: <Application Support>/notx/
    DataDir() (string, error)
}
```

### 4.1 Key Operations

`GenerateKey`, `Sign`, `BuildCSR`, `PublicKeyDER`, `DeleteKey`, `HasKey`.

These map to Secure Enclave operations on iOS. The private key is generated
inside the SE and is referenced by the `alias` string (stored as
`kSecAttrApplicationLabel`). It is never exported, serialised, or copied.

`BuildCSR` is the single point where a CSR can be produced. It is called by
Go during cert renewal and by Swift's `PairingCoordinator` during initial
pairing. In both cases the CSR bytes returned to Go are treated as an opaque
blob — Go forwards them to the authority RPC verbatim.

### 4.2 Certificate Operations

`StoreCert`, `LoadCert`, `DeleteCert`, `HasCert`.

Certificates (PEM-encoded) are stored as Keychain generic password items. The
accessibility class is `kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly` which
allows background cert renewal to read them after first unlock without an active
screen unlock.

### 4.3 Configuration Operations

`GetConfig`, `SetConfig`.

On iOS, these delegate to `UserDefaults` with an App Group suite name. The keys
stored here are non-sensitive identifiers and addresses (device URN, authority
address, active key alias). They are backed up to iCloud by default — see §10.

### 4.4 Storage Location

`DataDir` returns the root path under which the engine writes `.notx` files and
`index.db`. The engine appends its own subdirectory structure; `DataDir` just
provides the root.

---

## 5. Well-Known Aliases and Config Keys

Defined in `mobile/aliases.go`. Both Go and Swift must use these constants;
neither side hard-codes magic strings.

```go
// mobile/aliases.go

const (
    // Key aliases — versioned to support zero-downtime renewal.
    // Never look up a key by "v1" or "v2" directly; always read
    // ConfigKeyActiveKeyAlias to find the currently active alias.
    AliasDeviceKeyV1 = "notx.device.key.v1"
    AliasDeviceKeyV2 = "notx.device.key.v2"
    // Additional versions are allocated as "notx.device.key.v3", etc.
    // NextVersionAlias(current) returns the next alias in the sequence.

    // Certificate aliases.
    AliasDeviceCert      = "notx.device.cert"
    AliasAuthorityCACert = "notx.authority.ca.cert"

    // Configuration keys (stored via Platform.GetConfig / SetConfig).
    ConfigKeyDeviceURN      = "notx.device.urn"       // urn:notx:device:<id>
    ConfigKeyAuthorityAddr  = "notx.authority.addr"   // host:port
    ConfigKeyActiveKeyAlias = "notx.device.key.active" // e.g. "notx.device.key.v1"
)
```

The pairing token alias (`notx.pairing.token`) is a Swift-only constant
defined in `PairingCoordinator`. It never crosses the bridge.

**Key versioning rules:**

- On initial pairing, `AliasDeviceKeyV1` is used and
  `ConfigKeyActiveKeyAlias` is set to `"notx.device.key.v1"`.
- On each renewal, a new versioned alias is allocated. The old key is not
  deleted until the new cert is validated and the active alias config value
  has been atomically promoted to the new alias.
- `NextVersionAlias(current string) string` (exported from `mobile/aliases.go`)
  computes the next alias in the sequence for both Go and Swift callers.

---

## 6. Storage Model

### 6.1 Source of Truth — `.notx` Event Log

`.notx` files are append-only, event-sourced, self-describing, and portable.
They are unchanged from the desktop format. On mobile they live under
`<DataDir>/notes/<urn>.notx`.

The `.notx` file is the canonical source of truth. The SQLite database is
always a derived, rebuildable projection. If SQLite is lost or corrupt, the
engine replays every `.notx` file and rebuilds the database from scratch.

> **Core invariant**: SQLite must be fully derivable from `.notx` files at any
> time. Any operation that cannot satisfy this invariant is architecturally
> invalid.

### 6.2 Persistent Projection — SQLite

SQLite replaces Badger as the persistent projection layer on mobile. The
reasons for this choice:

| Property                | Badger                            | SQLite                                     |
| ----------------------- | --------------------------------- | ------------------------------------------ |
| `mmap` dependency       | Yes — problematic in iOS sandbox  | No                                         |
| Mobile lifecycle safety | No — built for long-lived servers | Yes — WAL is designed for killed processes |
| Corruption recovery     | Complex, server-oriented          | Automatic on next open                     |
| Schema evolution        | No schema concept                 | Additive `ALTER TABLE`                     |
| Query capability        | Key-prefix scans only             | Full SQL, FTS5                             |
| iOS support             | Untested                          | System library, used by Messages / Mail    |
| CGo requirement         | No                                | No (`modernc.org/sqlite`, pure Go)         |

The Go driver used is `modernc.org/sqlite` — a pure-Go transpiled port of the
official SQLite source. It passes the full SQLite test suite, requires no CGo,
and produces no Xcode build settings to manage.

### 6.3 SQLite Schema

```sql
-- Materialised note state (derived from .notx events)
CREATE TABLE notes (
    urn               TEXT    PRIMARY KEY,
    project_urn       TEXT    NOT NULL DEFAULT '',
    folder_urn        TEXT    NOT NULL DEFAULT '',
    note_type         TEXT    NOT NULL DEFAULT 'normal',
    title             TEXT    NOT NULL DEFAULT '',
    preview           TEXT    NOT NULL DEFAULT '',
    head_seq          INTEGER NOT NULL DEFAULT 0,
    deleted           INTEGER NOT NULL DEFAULT 0,
    created_at        INTEGER NOT NULL,          -- Unix milliseconds
    updated_at        INTEGER NOT NULL,          -- Unix milliseconds
    extra             TEXT    NOT NULL DEFAULT '{}' -- JSON for forward-compat fields
);

CREATE INDEX idx_notes_updated ON notes(updated_at DESC);
CREATE INDEX idx_notes_project ON notes(project_urn, deleted, updated_at DESC);
CREATE INDEX idx_notes_folder  ON notes(folder_urn,  deleted, updated_at DESC);

-- Full-text search (normal notes only; secure notes are never indexed)
CREATE VIRTUAL TABLE notes_fts USING fts5(
    urn  UNINDEXED,
    title,
    body,
    content='notes',
    content_rowid='rowid'
);

-- Materialised note content (for FTS and fast Get; secure notes have no row here)
CREATE TABLE note_content (
    urn     TEXT PRIMARY KEY,
    content TEXT NOT NULL DEFAULT ''
);

-- Projects, folders, users, devices, servers, pairing_secrets
-- (follow the same pattern: INTEGER timestamps, deleted flag, extra JSON)

-- Schema version tracking
CREATE TABLE schema_version (
    version    INTEGER PRIMARY KEY,
    applied_at INTEGER NOT NULL
);

-- Projection logic version (separate from schema version)
CREATE TABLE projection_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
-- Seeded: INSERT INTO projection_meta VALUES ('projection_version', '1');
```

WAL mode is applied immediately on every open:

```sql
PRAGMA journal_mode = WAL;
PRAGMA synchronous  = NORMAL;
PRAGMA foreign_keys = ON;
```

### 6.4 EnsureIndex Startup Sequence

Every time the engine starts it runs this deterministic sequence before
accepting any operations:

```
EnsureIndex(dbPath, notesDir):

  1. If index.db does not exist
       → open fresh DB, apply DDL, call rebuild(notesDir)

  2. Open existing DB, apply WAL pragmas, apply DDL (idempotent)

  3. If schema_version < currentSchemaVersion
       → run additive migrations in order

  4. If projection_meta.projection_version < currentProjectionVersion
       → call rebuild(notesDir)

  5. If PRAGMA integrity_check ≠ "ok"
       → close DB, delete index.db + -wal + -shm
       → open fresh DB, apply DDL, call rebuild(notesDir)

  6. Ready for normal operation
```

Rebuild is **always safe** and is not an error condition. It replays every
`.notx` file in `notesDir`, applying events in `(note_urn, sequence)` order,
and then records the new `projection_version` in `projection_meta`.

### 6.5 Projection Consistency Guarantees

**Idempotency** — Applying the same event twice produces the same result as
applying it once. Enforced by a sequence guard:

```
Apply event E to note N only if E.Sequence > notes.head_seq WHERE urn = N.urn
```

**Determinism** — Given the same ordered event sequence, projection always
produces identical SQLite state. No randomness, no `time.Now()` in projection
output. All timestamps come from the event record itself.

**Completeness** — Rebuild processes every event in every `.notx` file in
`(note_urn, sequence)` order. Skipping or reordering events is a bug.

**Security** — Secure note content (`note_type = 'secure'`) is never written
to `note_content` or `notes_fts`. This is a hard invariant enforced at every
write path in `repo/sqlite/provider.go`.

### 6.6 Migration Strategy

Migrations are **additive only**. The engine never drops or renames columns.

- Add columns: `ALTER TABLE notes ADD COLUMN new_field TEXT;`
- Use the `extra` JSON column for fields that are not yet promoted to
  first-class columns.
- If a migration requires non-trivial data transformation, increment
  `currentProjectionVersion` instead — this triggers a full rebuild from the
  `.notx` source of truth, which is always correct.

---

## 7. Pairing Flow

### 7.1 Why the Token Never Enters Go

In the original desktop design the `NTXP-…` token was passed as a Go `string`
parameter. This is not acceptable on mobile for three reasons:

1. The Swift/Go bridge copies `string` arguments into managed memory before Go
   sees them. ARC may retain that copy independently of Go's GC.
2. `runtime.GC()` on Go's mobile runtime is a hint, not a command. There is no
   guarantee of timely collection.
3. A token on the Go heap is visible to crash reporters, memory debuggers, and
   OS-level diagnostic dumps.

In v2 the token **never enters Go memory at all**. From QR scan to deletion
it lives only in the iOS Keychain under `WhenPasscodeSetThisDeviceOnly`.

### 7.2 Initial Pairing

The pairing flow is split at the security boundary. Swift owns every step that
touches the token or the key. Go receives only metadata after the RPC completes.

```
Admin (server)                         iOS App (Swift)
──────────────────────                 ─────────────────────────────────────
notxctl pairing create
  → NTXP-AAAAA-BBBBB-…
  → QR code PNG

shares QR out-of-band
                                       User taps "Pair with Server"
                                       Camera scans QR code

                                       ── Step 1: store token in Keychain ──
                                       Keychain.add(
                                         service:    "notx.pairing",
                                         account:    "notx.pairing.token",
                                         data:       tokenUTF8,
                                         accessible: WhenPasscodeSetThisDeviceOnly
                                       )
                                       ─────────────────────────────────────

                                       User taps "Connect"

                                       ── Step 2: consume token (read + delete) ──
                                       let token = Keychain.readAndDelete(
                                                     "notx.pairing.token"
                                                   )

                                       ── Step 3: generate key in Secure Enclave ──
                                       iOSPlatform.generateKey(AliasDeviceKeyV1)

                                       ── Step 4: build + sign CSR in Swift ──
                                       let csrDER = iOSPlatform.buildCSR(
                                                      alias:      AliasDeviceKeyV1,
                                                      commonName: deviceURN
                                                    )
                                       // SE key reference never leaves SE

                                       ── Step 5: call RegisterServer RPC ──
                                       let resp = grpc.RegisterServer(
                                                    serverUrn:     deviceURN,
                                                    csr:           csrDER,
                                                    pairingSecret: token,
                                                    serverName:    "notx-ios-…",
                                                    endpoint:      ""
                                                  )

Authority validates NTXP token  ◄─────────────────────────────────────────
Issues mTLS client cert         ──────────────────────────────────────────►

                                       ── Step 6: store certs in Keychain ──
                                       Keychain.storeCert(AliasDeviceCert,
                                                          resp.certificate)
                                       Keychain.storeCert(AliasAuthorityCACert,
                                                          resp.caCertificate)

                                       ── Step 7: notify Go (metadata only) ──
                                       engine.onPairingComplete(
                                         deviceURN:      deviceURN,
                                         authorityAddr:  authority,
                                         activeKeyAlias: AliasDeviceKeyV1
                                       )
                                       // Go stores three config values only.
                                       // No token, no cert, no key bytes cross.

                                       Pairing complete ✓
                                       Token: deleted from Keychain
                                       Key:   in Secure Enclave (non-exportable)
                                       Cert:  in Keychain
```

### 7.3 Cert Renewal

Renewal is Go-orchestrated but Swift-signed. Go manages the versioned alias
rotation; Swift performs key generation and CSR construction via `Platform`
calls.

```
BGAppRefreshTask / app foreground
         │
         ▼
engine.RenewIfNeeded(ctx)
         │
         ├─ read ConfigKeyActiveKeyAlias → activeAlias ("notx.device.key.v1")
         ├─ Platform.LoadCert(AliasDeviceCert) → certPEM
         ├─ parse NotAfter from certPEM
         ├─ if time.Until(NotAfter) > 7 days → return nil (nothing to do)
         │
         ├─ newAlias = NextVersionAlias(activeAlias) → "notx.device.key.v2"
         │
         ├─ Platform.GenerateKey(newAlias)
         │    Swift: creates new SE key under newAlias
         │    Old key (activeAlias) is untouched
         │
         ├─ Platform.BuildCSR(newAlias, deviceURN)
         │    Swift: builds and signs CSR using newAlias SE key ref
         │    Go: receives opaque DER bytes only
         │
         ├─ Platform.LoadCert(AliasDeviceCert)     → current client cert (mTLS)
         ├─ Platform.LoadCert(AliasAuthorityCACert) → CA cert (server verify)
         ├─ dial authority with mTLS (current cert still valid)
         ├─ RenewCertificate RPC → resp.Certificate
         │
         ├─ validate resp.Certificate:
         │    parse NotAfter → must be > now + 7 days
         │    if invalid:
         │      Platform.DeleteKey(newAlias)
         │      return ErrRenewal
         │
         ├─ Platform.StoreCert(AliasDeviceCert, resp.Certificate)
         │    overwrites the cert; new cert references newAlias key
         │
         ├─ Platform.SetConfig(ConfigKeyActiveKeyAlias, newAlias)
         │    ← atomic promotion: newAlias is now the active key
         │
         └─ Platform.DeleteKey(activeAlias)
              ← old key deleted only after promotion is confirmed
```

A crash at any point before promotion leaves the device with the original
valid key and cert. A crash after promotion but before old-key deletion leaves
an orphaned key that will be cleaned up on the next renewal cycle.

---

## 8. gomobile Build Configuration

### Target Package

Only `mobile/` is exported. All other packages remain internal Go.

### SQLite Driver

`modernc.org/sqlite` — pure Go, no CGo, no Xcode C compiler settings required.
If Apple policy or performance profiling later requires linking against the
system `libsqlite3.dylib`, the driver can be swapped inside `repo/sqlite/`
without changing any interface.

### Build Tags

Files that must not compile for iOS are guarded with:

```go
//go:build !ios
```

Applied to:

- `repo/index/index.go` — the Badger index (replaced by `repo/sqlite/` on iOS)

The `mobile/` and `repo/sqlite/` packages carry no build tag — they compile on
all platforms, allowing desktop tests to exercise the same SQLite code paths.

### Build Script

```bash
#!/usr/bin/env bash
# scripts/build_ios.sh
set -euo pipefail

# Requirements:
#   go install golang.org/x/mobile/cmd/gomobile@latest
#   gomobile init
#   Xcode 15+ with iOS 16+ SDK

PACKAGE="github.com/zebaqui/notx-engine/mobile"
OUTPUT="build/NotxEngine.xcframework"

mkdir -p build

gomobile bind \
  -target ios,iossimulator \
  -o "${OUTPUT}" \
  -iosversion 16.0 \
  -tags ios \
  "${PACKAGE}"

echo "Built ${OUTPUT}"
```

---

## 9. iOS Integration Guide (Swift)

This section provides the complete, production-ready Swift implementation for
all components. Copy these files into your Xcode project as a starting point.

### 9.1 Project Setup

**Capabilities required** (Xcode → Signing & Capabilities):

- Keychain Sharing — add the App Group ID (e.g. `group.com.example.notx`)
- App Groups — same ID

**Minimum deployment target**: iOS 16.0

**Add the framework** to your target:

1. Build `NotxEngine.xcframework` via `scripts/build_ios.sh`
2. Drag the framework into your Xcode project
3. Set "Embed & Sign" in the target's Frameworks, Libraries, and Embedded Content

**Import in Swift**:

```swift
import NotxEngine
```

All symbols from `mobile/` are available under the `Notx` prefix
(e.g. `NotxEngine`, `NotxPlatform`, `NotxAliasDeviceKeyV1`).

---

### 9.2 iOSPlatform — Key Operations

```swift
// iOSPlatform.swift (Key operations section)

import Foundation
import Security
import NotxEngine

final class iOSPlatform: NSObject, NotxPlatform {

    private let accessGroup: String

    init(accessGroup: String) {
        self.accessGroup = accessGroup
    }

    // MARK: - Key Operations (Secure Enclave)

    func generateKey(_ alias: String, error: NSErrorPointer) -> Data {
        // Delete any existing key under this alias first.
        deleteKey(alias, error: nil)

        var cfError: Unmanaged<CFError>?
        guard let access = SecAccessControlCreateWithFlags(
            nil,
            kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
            [.privateKeyUsage],       // no biometric — required for background renewal
            &cfError
        ) else {
            error?.pointee = cfError!.takeRetainedValue() as NSError
            return Data()
        }

        let attrs: [String: Any] = [
            kSecAttrKeyType as String:       kSecAttrKeyTypeECSECPrimeRandom,
            kSecAttrKeySizeInBits as String: 256,
            kSecAttrTokenID as String:       kSecAttrTokenIDSecureEnclave,
            kSecAttrAccessGroup as String:   accessGroup,
            kSecPrivateKeyAttrs as String: [
                kSecAttrIsPermanent as String:      true,
                kSecAttrApplicationLabel as String: aliasData(alias),
                kSecAttrAccessControl as String:    access,
            ],
        ]

        guard
            let privateKey = SecKeyCreateRandomKey(attrs as CFDictionary, &cfError),
            let publicKey  = SecKeyCopyPublicKey(privateKey),
            let pubDER     = SecKeyCopyExternalRepresentation(publicKey, &cfError) as Data?
        else {
            error?.pointee = cfError!.takeRetainedValue() as NSError
            return Data()
        }
        return pubDER
    }

    func sign(_ alias: String, digest: Data, error: NSErrorPointer) -> Data {
        guard let key = loadPrivateKey(alias) else {
            error?.pointee = NotxError.keyNotFound(alias)
            return Data()
        }
        var cfError: Unmanaged<CFError>?
        guard let sig = SecKeyCreateSignature(
            key,
            .ecdsaSignatureDigestX962SHA256,
            digest as CFData,
            &cfError
        ) as Data? else {
            error?.pointee = cfError!.takeRetainedValue() as NSError
            return Data()
        }
        return sig
    }

    func buildCSR(_ alias: String, commonName: String, error: NSErrorPointer) -> Data {
        guard let privateKey = loadPrivateKey(alias),
              let publicKey  = SecKeyCopyPublicKey(privateKey),
              let pubDER     = SecKeyCopyExternalRepresentation(
                                 publicKey, nil
                               ) as Data?
        else {
            error?.pointee = NotxError.keyNotFound(alias)
            return Data()
        }

        // Build a minimal PKCS#10 CSR structure, sign it with the SE key.
        // The DER encoding follows RFC 2986.
        do {
            let csrDER = try buildPKCS10CSR(
                publicKeyDER: pubDER,
                commonName:   commonName,
                signer: { tbs in
                    var cfErr: Unmanaged<CFError>?
                    guard let sig = SecKeyCreateSignature(
                        privateKey,
                        .ecdsaSignatureMessageX962SHA256,
                        tbs as CFData,
                        &cfErr
                    ) as Data? else {
                        throw cfErr!.takeRetainedValue() as Error
                    }
                    return sig
                }
            )
            return csrDER
        } catch {
            error?.pointee = error as NSError
            return Data()
        }
    }

    func publicKeyDER(_ alias: String, error: NSErrorPointer) -> Data {
        let query: [String: Any] = [
            kSecClass as String:                kSecClassKey,
            kSecAttrKeyClass as String:         kSecAttrKeyClassPrivate,
            kSecAttrApplicationLabel as String: aliasData(alias),
            kSecAttrAccessGroup as String:      accessGroup,
            kSecReturnRef as String:            true,
        ]
        var ref: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &ref) == errSecSuccess,
              let privateKey = ref,
              let publicKey  = SecKeyCopyPublicKey(privateKey as! SecKey),
              let der        = SecKeyCopyExternalRepresentation(publicKey, nil) as Data?
        else {
            error?.pointee = NotxError.keyNotFound(alias)
            return Data()
        }
        return der
    }

    func deleteKey(_ alias: String, error: NSErrorPointer) {
        let query: [String: Any] = [
            kSecClass as String:                kSecClassKey,
            kSecAttrApplicationLabel as String: aliasData(alias),
            kSecAttrAccessGroup as String:      accessGroup,
        ]
        SecItemDelete(query as CFDictionary)
        // Deletion of a non-existent key is not an error.
    }

    func hasKey(_ alias: String, error: NSErrorPointer) -> Bool {
        let query: [String: Any] = [
            kSecClass as String:                kSecClassKey,
            kSecAttrKeyClass as String:         kSecAttrKeyClassPrivate,
            kSecAttrApplicationLabel as String: aliasData(alias),
            kSecAttrAccessGroup as String:      accessGroup,
            kSecReturnRef as String:            false,
        ]
        return SecItemCopyMatching(query as CFDictionary, nil) == errSecSuccess
    }

    // MARK: - Private helpers

    private func loadPrivateKey(_ alias: String) -> SecKey? {
        let query: [String: Any] = [
            kSecClass as String:                kSecClassKey,
            kSecAttrKeyClass as String:         kSecAttrKeyClassPrivate,
            kSecAttrApplicationLabel as String: aliasData(alias),
            kSecAttrAccessGroup as String:      accessGroup,
            kSecReturnRef as String:            true,
        ]
        var ref: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &ref) == errSecSuccess else {
            return nil
        }
        return (ref as! SecKey)
    }

    private func aliasData(_ alias: String) -> Data {
        alias.data(using: .utf8)!
    }
}
```

> **Note on `buildPKCS10CSR`**: This helper must be implemented using a DER
> encoding library (e.g. `ASN1Encoder`, a vendored DER writer, or
> `CryptoKit` + manual DER construction). A reference implementation is
> provided in `NotxPairing/CSRBuilder.swift` in the example project. The
> function signature is:
>
> ```swift
> func buildPKCS10CSR(
>     publicKeyDER: Data,
>     commonName: String,
>     signer: (Data) throws -> Data
> ) throws -> Data
> ```

---

### 9.3 iOSPlatform — Certificate Operations

```swift
// iOSPlatform.swift (Certificate operations section)

extension iOSPlatform {

    // MARK: - Certificate Operations (Keychain generic password items)
    //
    // kSecAttrService  = "notx.certs"
    // kSecAttrAccount  = alias
    // kSecValueData    = PEM bytes (UTF-8)
    // kSecAttrAccessible = AfterFirstUnlockThisDeviceOnly
    //                    → allows background renewal without active unlock

    private var certService: String { "notx.certs" }

    func storeCert(_ alias: String, certPEM: Data, error: NSErrorPointer) {
        // Delete before add (idempotent update pattern).
        let deleteQuery: [String: Any] = [
            kSecClass as String:           kSecClassGenericPassword,
            kSecAttrService as String:     certService,
            kSecAttrAccount as String:     alias,
            kSecAttrAccessGroup as String: accessGroup,
        ]
        SecItemDelete(deleteQuery as CFDictionary)

        var addQuery = deleteQuery
        addQuery[kSecValueData as String]      = certPEM
        addQuery[kSecAttrAccessible as String] =
            kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly

        let status = SecItemAdd(addQuery as CFDictionary, nil)
        if status != errSecSuccess {
            error?.pointee = keychainError(status, context: "storeCert(\(alias))")
        }
    }

    func loadCert(_ alias: String, error: NSErrorPointer) -> Data {
        let query: [String: Any] = [
            kSecClass as String:           kSecClassGenericPassword,
            kSecAttrService as String:     certService,
            kSecAttrAccount as String:     alias,
            kSecAttrAccessGroup as String: accessGroup,
            kSecReturnData as String:      true,
            kSecMatchLimit as String:      kSecMatchLimitOne,
        ]
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        guard status == errSecSuccess, let data = result as? Data else {
            error?.pointee = keychainError(status, context: "loadCert(\(alias))")
            return Data()
        }
        return data
    }

    func deleteCert(_ alias: String, error: NSErrorPointer) {
        let query: [String: Any] = [
            kSecClass as String:           kSecClassGenericPassword,
            kSecAttrService as String:     certService,
            kSecAttrAccount as String:     alias,
            kSecAttrAccessGroup as String: accessGroup,
        ]
        SecItemDelete(query as CFDictionary)
    }

    func hasCert(_ alias: String, error: NSErrorPointer) -> Bool {
        let query: [String: Any] = [
            kSecClass as String:           kSecClassGenericPassword,
            kSecAttrService as String:     certService,
            kSecAttrAccount as String:     alias,
            kSecAttrAccessGroup as String: accessGroup,
            kSecReturnData as String:      false,
            kSecMatchLimit as String:      kSecMatchLimitOne,
        ]
        return SecItemCopyMatching(query as CFDictionary, nil) == errSecSuccess
    }

    // MARK: - Keychain error helper

    private func keychainError(_ status: OSStatus, context: String) -> NSError {
        let msg = SecCopyErrorMessageString(status, nil) as String? ?? "OSStatus \(status)"
        return NSError(
            domain: "notx.keychain",
            code: Int(status),
            userInfo: [NSLocalizedDescriptionKey: "\(context): \(msg)"]
        )
    }
}
```

---

### 9.4 iOSPlatform — Configuration

```swift
// iOSPlatform.swift (Configuration section)

extension iOSPlatform {

    // MARK: - Configuration (NSUserDefaults with App Group suite)
    //
    // The suite name is the App Group ID (e.g. "group.com.example.notx").
    // This allows a Share Extension or widget to read the same config values.

    private var defaults: UserDefaults {
        // UserDefaults(suiteName:) returns nil only if suiteName is invalid.
        UserDefaults(suiteName: accessGroup) ?? .standard
    }

    func getConfig(_ key: String, error: NSErrorPointer) -> String {
        return defaults.string(forKey: key) ?? ""
    }

    func setConfig(_ key: String, value: String, error: NSErrorPointer) {
        defaults.set(value, forKey: key)
        // UserDefaults.set is synchronous in-memory; the backing plist is
        // flushed to disk by the OS automatically. No explicit synchronize() needed.
    }
}
```

---

### 9.5 iOSPlatform — Storage Location

```swift
// iOSPlatform.swift (Storage location section)

extension iOSPlatform {

    // MARK: - Storage location

    func dataDir(_ error: NSErrorPointer) -> String {
        let fm = FileManager.default
        guard let appSupport = fm.urls(
            for: .applicationSupportDirectory,
            in: .userDomainMask
        ).first else {
            error?.pointee = NSError(
                domain: "notx.storage",
                code: -1,
                userInfo: [NSLocalizedDescriptionKey: "Application Support directory not found"]
            )
            return ""
        }

        let dir = appSupport.appendingPathComponent("notx", isDirectory: true)

        do {
            try fm.createDirectory(at: dir, withIntermediateDirectories: true)
        } catch let e as NSError {
            error?.pointee = e
            return ""
        }

        // The data directory itself is backed up to iCloud.
        // index.db is excluded from backup individually in applyFileProtection().
        return dir.path
    }

    // applyFileProtection must be called once after the engine opens,
    // after DataDir() has been resolved. It sets iOS Data Protection classes
    // on the notes directory and the SQLite files.
    //
    // Call this from NotxEngineHost.init() after engine.dataDir() succeeds.
    func applyFileProtection(dataDir: String) {
        let fm = FileManager.default
        let notesDir = (dataDir as NSString).appendingPathComponent("notes")
        let indexDB  = (dataDir as NSString).appendingPathComponent("index.db")
        let indexWAL = indexDB + "-wal"
        let indexSHM = indexDB + "-shm"

        // .notx files: locked when device is locked.
        setProtection(.complete, path: notesDir, recursive: true)

        // SQLite files: remain accessible for background sync after first unlock.
        for path in [indexDB, indexWAL, indexSHM] {
            setProtection(.completeUnlessOpen, path: path, recursive: false)
        }

        // Exclude index.db from iCloud backup — it is rebuildable.
        for path in [indexDB, indexWAL, indexSHM] {
            excludeFromBackup(path: path)
        }
    }

    private func setProtection(
        _ protection: FileProtectionType,
        path: String,
        recursive: Bool
    ) {
        let fm = FileManager.default
        let attrs = [FileAttributeKey.protectionKey: protection]
        try? fm.setAttributes(attrs, ofItemAtPath: path)

        if recursive, let enumerator = fm.enumerator(atPath: path) {
            for case let file as String in enumerator {
                let full = (path as NSString).appendingPathComponent(file)
                try? fm.setAttributes(attrs, ofItemAtPath: full)
            }
        }
    }

    private func excludeFromBackup(path: String) {
        var url = URL(fileURLWithPath: path)
        var values = URLResourceValues()
        values.isExcludedFromBackup = true
        try? url.setResourceValues(values)
    }
}
```

---

### 9.6 PairingCoordinator

This class owns the entire pairing and token lifecycle. The Go engine is
not involved until `pair()` calls `engine.onPairingComplete()` at the very end.

```swift
// PairingCoordinator.swift

import Foundation
import Security
import GRPC          // apple/grpc-swift
import NIOCore
import NotxEngine

enum PairingError: LocalizedError {
    case invalidToken
    case tokenNotFound
    case keyGenerationFailed(Error)
    case csrFailed(Error)
    case rpcFailed(Error)
    case certStoreFailed(Error)
    case pairingCompleteFailed(Error)

    var errorDescription: String? {
        switch self {
        case .invalidToken:               return "The pairing token is invalid."
        case .tokenNotFound:              return "No pairing token found. Scan the QR code first."
        case .keyGenerationFailed(let e): return "Key generation failed: \(e.localizedDescription)"
        case .csrFailed(let e):           return "CSR construction failed: \(e.localizedDescription)"
        case .rpcFailed(let e):           return "Pairing RPC failed: \(e.localizedDescription)"
        case .certStoreFailed(let e):     return "Certificate storage failed: \(e.localizedDescription)"
        case .pairingCompleteFailed(let e): return "Engine notification failed: \(e.localizedDescription)"
        }
    }
}

final class PairingCoordinator {

    private let platform: iOSPlatform
    private let engine: NotxEngine
    private let accessGroup: String

    // Keychain coordinates for the pairing token.
    // These are Swift-only constants — they never cross to Go.
    private let tokenService = "notx.pairing"
    private let tokenAccount = "notx.pairing.token"

    init(platform: iOSPlatform, engine: NotxEngine, accessGroup: String) {
        self.platform    = platform
        self.engine      = engine
        self.accessGroup = accessGroup
    }

    // MARK: - Token ingestion (call immediately after QR scan)

    /// Stores the scanned pairing token in the Keychain.
    /// Call this the instant the QR code is decoded — before any UI transition.
    func receivePairingToken(_ token: String) throws {
        guard let data = token.data(using: .utf8), !data.isEmpty else {
            throw PairingError.invalidToken
        }
        // Delete any stale token first (idempotent).
        deleteStoredToken()

        let query: [String: Any] = [
            kSecClass as String:           kSecClassGenericPassword,
            kSecAttrService as String:     tokenService,
            kSecAttrAccount as String:     tokenAccount,
            kSecAttrAccessGroup as String: accessGroup,
            kSecValueData as String:       data,
            // Most restrictive class: hardware-bound, passcode required,
            // never migrated, not in iCloud Keychain.
            kSecAttrAccessible as String:
                kSecAttrAccessibleWhenPasscodeSetThisDeviceOnly,
        ]
        let status = SecItemAdd(query as CFDictionary, nil)
        guard status == errSecSuccess else {
            throw keychainError(status, context: "receivePairingToken")
        }
    }

    // MARK: - Pairing execution (call when user taps "Connect")

    /// Performs the full pairing flow: token consume → key gen → CSR → RPC →
    /// cert store → engine notification.
    ///
    /// - Parameters:
    ///   - authority: host:port of the authority gRPC server (plain, not mTLS)
    ///   - deviceURN: the device's urn:notx:device:<id> URN
    func pair(authority: String, deviceURN: String) async throws {
        // 1. Consume token atomically (read then immediately delete).
        let tokenData = try consumeStoredToken()
        guard let token = String(data: tokenData, encoding: .utf8) else {
            throw PairingError.invalidToken
        }

        // 2. Generate key in Secure Enclave.
        var nsError: NSError?
        let pubKeyDER = platform.generateKey(NotxAliasDeviceKeyV1, error: &nsError)
        if let e = nsError { throw PairingError.keyGenerationFailed(e) }
        guard !pubKeyDER.isEmpty else {
            throw PairingError.keyGenerationFailed(
                NSError(domain: "notx.pairing", code: -1,
                        userInfo: [NSLocalizedDescriptionKey: "generateKey returned empty data"])
            )
        }

        // 3. Build and sign CSR entirely in Swift using the SE key reference.
        let csrDER = platform.buildCSR(
            NotxAliasDeviceKeyV1,
            commonName: deviceURN,
            error: &nsError
        )
        if let e = nsError { throw PairingError.csrFailed(e) }

        // 4. Call RegisterServer RPC over an insecure channel (bootstrap only).
        //    mTLS is not yet available — this channel uses plain TCP.
        //    The server validates the NTXP token to authorise the registration.
        let channel = try makeBootstrapChannel(authority: authority)
        defer { try? channel.close().wait() }

        let client = Notx_ServerPairingServiceNIOClient(channel: channel)
        var req = Notx_RegisterServerRequest()
        req.serverUrn     = deviceURN
        req.csr           = csrDER
        req.pairingSecret = token
        req.serverName    = "notx-ios-\(UIDevice.current.name)"
        req.endpoint      = ""  // clients have no listener

        let resp: Notx_RegisterServerResponse
        do {
            resp = try await client.registerServer(req).response.get()
        } catch {
            throw PairingError.rpcFailed(error)
        }

        // Token is already deleted from Keychain (consumed in step 1).

        // 5. Store certificates in Keychain.
        platform.storeCert(
            NotxAliasDeviceCert,
            certPEM: resp.certificate,
            error: &nsError
        )
        if let e = nsError { throw PairingError.certStoreFailed(e) }

        platform.storeCert(
            NotxAliasAuthorityCACert,
            certPEM: resp.caCertificate,
            error: &nsError
        )
        if let e = nsError { throw PairingError.certStoreFailed(e) }

        // 6. Notify the Go engine — metadata only, no secrets cross the bridge.
        var goError: NSError?
        engine.onPairingComplete(
            deviceURN,
            authorityAddr: authority,
            activeKeyAlias: NotxAliasDeviceKeyV1,
            error: &goError
        )
        if let e = goError { throw PairingError.pairingCompleteFailed(e) }
    }

    // MARK: - Token helpers

    private func consumeStoredToken() throws -> Data {
        let query: [String: Any] = [
            kSecClass as String:           kSecClassGenericPassword,
            kSecAttrService as String:     tokenService,
            kSecAttrAccount as String:     tokenAccount,
            kSecAttrAccessGroup as String: accessGroup,
            kSecReturnData as String:      true,
            kSecMatchLimit as String:      kSecMatchLimitOne,
        ]
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        guard status == errSecSuccess, let data = result as? Data else {
            throw PairingError.tokenNotFound
        }
        // Delete immediately — single-use token.
        deleteStoredToken()
        return data
    }

    private func deleteStoredToken() {
        let query: [String: Any] = [
            kSecClass as String:           kSecClassGenericPassword,
            kSecAttrService as String:     tokenService,
            kSecAttrAccount as String:     tokenAccount,
            kSecAttrAccessGroup as String: accessGroup,
        ]
        SecItemDelete(query as CFDictionary)
    }

    // MARK: - Channel helpers

    private func makeBootstrapChannel(authority: String) throws -> GRPCChannel {
        // authority is "host:port" — plain TCP for the bootstrap pairing call.
        let parts = authority.split(separator: ":", maxSplits: 1)
        let host  = String(parts.first ?? "localhost")
        let port  = Int(parts.last ?? "50051") ?? 50051

        return try GRPCChannelPool.with(
            target: .host(host, port: port),
            transportSecurity: .plaintext,
            eventLoopGroup: PlatformSupport.makeEventLoopGroup(loopCount: 1)
        )
    }

    private func keychainError(_ status: OSStatus, context: String) -> Error {
        let msg = SecCopyErrorMessageString(status, nil) as String? ?? "OSStatus \(status)"
        return NSError(
            domain: "notx.keychain",
            code: Int(status),
            userInfo: [NSLocalizedDescriptionKey: "\(context): \(msg)"]
        )
    }
}
```

---

### 9.7 NotxEngineHost — Entry Point

This is the single object your app creates at startup. Hold it alive for the
entire app session — it owns the engine and the database connection.

````swift
// NotxEngineHost.swift

import Foundation
import NotxEngine

/// NotxEngineHost is the app-level owner of the notx engine.
///
/// Create exactly one instance per app session, typically in your
/// AppDelegate or top-level @main struct, and inject it via the
/// environment or dependency container.
///
/// ```swift
/// @main
/// struct NotxApp: App {
///     @StateObject private var host = try! NotxEngineHost(
///         appGroupID: "group.com.example.notx"
///     )
///     var body: some Scene { ... }
/// }
/// ```
@MainActor
final class NotxEngineHost: ObservableObject {

    let platform:  iOSPlatform
    let engine:    NotxEngine
    let pairing:   PairingCoordinator

    /// Creates the host, opens the Go engine and SQLite database.
    ///
    /// - Parameter appGroupID: The App Group capability identifier, e.g.
    ///   `"group.com.example.notx"`. Must match the Keychain Sharing entry.
    init(appGroupID: String) throws {
        let platform = iOSPlatform(accessGroup: appGroupID)

        var nsError: NSError?
        let engine = NotxNewEngine(platform, error: &nsError)
        if let e = nsError { throw e }
        guard let engine else {
            throw NSError(
                domain: "notx.engine",
                code: -1,
                userInfo: [NSLocalizedDescriptionKey: "Engine.New returned nil without error"]
            )
        }

        self.platform = platform
        self.engine   = engine
        self.pairing  = PairingCoordinator(
            platform:    platform,
            engine:      engine,
            accessGroup: appGroupID
        )

        // Apply iOS file protection and backup exclusions.
        var dirError: NSError?
        let dataDir = engine.dataDir(&dirError)
        if let e = dirError { throw e }
        platform.applyFileProtection(dataDir: dataDir)
    }

    deinit {
        engine.close()
    }

    // MARK: - Pairing

    /// Call immediately after the QR code scanner decodes a token.
    func receivePairingToken(_ token: String) throws {
        try pairing.receivePairingToken(token)
    }

    /// Call when the user taps "Connect". Performs the full pairing flow.
    func connectToServer(authority: String) async throws {
        var nsError: NSError?
        let deviceURN = engine.ensureDeviceURN(&nsError)
        if let e = nsError { throw e }
        try await pairing.pair(authority: authority, deviceURN: deviceURN)
    }

    // MARK: - Pairing state

    var isPaired: Bool {
        var nsError: NSError?
        let result = engine.isPaired(&nsError)
        return nsError == nil && result
    }

    // MARK: - Cert renewal

    /// Call from BGAppRefreshTask or when the app foregrounds.
    /// Returns immediately if renewal is not yet needed.
    func renewCertIfNeeded() async throws {
        // engine.renewIfNeeded is a blocking Go call — run it off the main thread.
        try await Task.detached(priority: .utility) { [engine] in
            var nsError: NSError?
            engine.renewIfNeeded(&nsError)
            if let e = nsError { throw e }
        }.value
    }

    // MARK: - Note operations

    func createNote(name: String, projectURN: String = "", folderURN: String = "") throws -> String {
        var nsError: NSError?
        let urn = engine.createNote(name, projectURN: projectURN, folderURN: folderURN, error: &nsError)
        if let e = nsError { throw e }
        return urn
    }

    func listNotes(projectURN: String = "", folderURN: String = "") throws -> [NotxNoteHeader] {
        let opts = NotxListOptions()
        opts.projectURN = projectURN
        opts.folderURN  = folderURN
        opts.pageSize   = 100

        var nsError: NSError?
        let list = engine.listNotes(opts, error: &nsError)
        if let e = nsError { throw e }
        return list?.items as? [NotxNoteHeader] ?? []
    }

    func searchNotes(query: String) throws -> [NotxSearchResult] {
        let opts = NotxSearchOptions()
        opts.query    = query
        opts.pageSize = 20

        var nsError: NSError?
        let results = engine.searchNotes(opts, error: &nsError)
        if let e = nsError { throw e }
        return results?.results as? [NotxSearchResult] ?? []
    }

    func deleteNote(urn: String) throws {
        var nsError: NSError?
        engine.deleteNote(urn, error: &nsError)
        if let e = nsError { throw e }
    }

    // MARK: - Project operations

    func createProject(name: String) throws -> String {
        var nsError: NSError?
        let urn = engine.createProject(name, error: &nsError)
        if let e = nsError { throw e }
        return urn
    }

    func listProjects() throws -> [NotxProjectHeader] {
        var nsError: NSError?
        let items = engine.listProjects(&nsError)
        if let e = nsError { throw e }
        return items as? [NotxProjectHeader] ?? []
    }
}
````

---

### 9.8 File Protection and Backup Policy

| Path           | Data Protection Class                | iCloud Backup | Rationale                                      |
| -------------- | ------------------------------------ | ------------- | ---------------------------------------------- |
| `notes/*.notx` | `NSFileProtectionComplete`           | ✅ Yes        | User data; encrypted by iCloud with device key |
| `index.db`     | `NSFileProtectionCompleteUnlessOpen` | ❌ No         | Rebuildable; `isExcludedFromBackup = true`     |
| `index.db-wal` | `NSFileProtectionCompleteUnlessOpen` | ❌ No         | Same as above; must match journal class        |
| `index.db-shm` | `NSFileProtectionCompleteUnlessOpen` | ❌ No         | Same as above                                  |

**Why `CompleteUnlessOpen` for index.db**: Background cert renewal and sync
tasks need write access to the database after the device has been unlocked at
least once since boot, but without requiring an active screen unlock.
`NSFileProtectionComplete` would block all background I/O.

**The `-wal` and `-shm` files must have the same protection class as `index.db`**.
If they differ, iOS will refuse to open the WAL files when the database is
already open and the device locks mid-session.

| Keychain item           | Accessibility                    | iCloud   | Rationale                                    |
| ----------------------- | -------------------------------- | -------- | -------------------------------------------- |
| Device private key (SE) | `AfterFirstUnlockThisDeviceOnly` | ❌ Never | SE enforces `ThisDeviceOnly`; non-exportable |
| mTLS client cert        | `AfterFirstUnlockThisDeviceOnly` | ❌ No    | Background renewal needs it; device-bound    |
| Authority CA cert       | `AfterFirstUnlockThisDeviceOnly` | ❌ No    | Same reasoning                               |
| Pairing token           | `WhenPasscodeSetThisDeviceOnly`  | ❌ Never | Most restrictive; single-use; hardware-bound |

---

### 9.9 Background Tasks

Register a `BGAppRefreshTask` to drive cert renewal. The system budget is
approximately 30 seconds; a TLS handshake + gRPC round-trip should complete
well within this on a stable network connection.

```swift
// AppDelegate.swift (or equivalent)

import BackgroundTasks

// In application(_:didFinishLaunchingWithOptions:):
BGTaskScheduler.shared.register(
    forTaskWithIdentifier: "com.example.notx.certRenewal",
    using: nil
) { task in
    Task {
        do {
            try await host.renewCertIfNeeded()
            task.setTaskCompleted(success: true)
        } catch {
            // Non-fatal: the engine will retry on the next foreground or
            // the next BGAppRefreshTask invocation.
            task.setTaskCompleted(success: false)
        }
    }
    task.expirationHandler = {
        // The system is reclaiming the budget. Mark incomplete so the
        // scheduler reattempts later.
        task.setTaskCompleted(success: false)
    }
}

// Schedule the task (call this when the app moves to background):
func scheduleRenewal() {
    let request = BGAppRefreshTaskRequest(
        identifier: "com.example.notx.certRenewal"
    )
    request.earliestBeginDate = Date(timeIntervalSinceNow: 60 * 60 * 24) // 24h
    try? BGTaskScheduler.shared.submit(request)
}
```

Add `com.example.notx.certRenewal` to the `BGTaskSchedulerPermittedIdentifiers`
array in `Info.plist`.

---

### 9.10 Error Handling

The Go engine surfaces errors as `NSError` on the Swift side. The `domain`
field identifies the subsystem; the `code` field maps to one of the constants
in `mobile/errors.go`:

| Code | Constant           | Meaning                                   |
| ---- | ------------------ | ----------------------------------------- |
| 1001 | `ErrCodeNotFound`  | Key, cert, or config value does not exist |
| 1002 | `ErrCodeKeyExists` | Key already exists under that alias       |
| 1003 | `ErrCodeKeychain`  | Keychain API failure                      |
| 1004 | `ErrCodeCrypto`    | Secure Enclave / cryptographic failure    |
| 1005 | `ErrCodeConfig`    | Configuration persistence failure         |
| 1006 | `ErrCodeStorage`   | SQLite or file-system failure             |
| 1007 | `ErrCodePairing`   | Pairing flow failure                      |
| 1008 | `ErrCodeRenewal`   | Cert renewal failure                      |

```swift
// Example: distinguish not-found from other errors
var nsError: NSError?
let cert = platform.loadCert(NotxAliasDeviceCert, error: &nsError)
if let e = nsError {
    if e.code == 1001 { // ErrCodeNotFound
        // Device is not paired — show pairing UI
    } else {
        // Unexpected Keychain error — log and surface to user
    }
}
```

---

## 10. iOS Storage Layout

```
<Application Support>/notx/          ← Platform.DataDir()
├── notes/                            ← .notx event logs (one per note URN)
│   ├── urn:notx:note:<id-1>.notx      ← NSFileProtectionComplete
│   ├── urn:notx:note:<id-2>.notx
│   └── …
├── index.db                          ← SQLite (NSFileProtectionCompleteUnlessOpen)
├── index.db-wal                      ← WAL journal (same protection class)
└── index.db-shm                      ← Shared memory (same protection class)
```

The `index.db` triplet is excluded from iCloud backup. The `notes/` directory
is backed up (each `.notx` file is encrypted by iCloud with the device key).

---

## 11. Security Guarantees

| Property                    | Desktop                            | iOS (v2)                                                         |
| --------------------------- | ---------------------------------- | ---------------------------------------------------------------- |
| Private key storage         | PEM file, `0600` permissions       | Secure Enclave, hardware-bound                                   |
| Private key exportability   | Exportable PEM                     | Non-exportable (SE enforces)                                     |
| Private key access control  | OS user permission                 | Device passcode (no biometric — required for background renewal) |
| Pairing token storage       | Go `string` in memory              | Keychain, `WhenPasscodeSetThisDeviceOnly`                        |
| Token ever enters Go heap   | Yes (v1)                           | **No** — stays in Swift/Keychain                                 |
| Token durability            | Process lifetime                   | Survives app restart; deleted atomically on use                  |
| Certificate protection      | `0644` file                        | Keychain, `AfterFirstUnlockThisDeviceOnly`                       |
| Note file protection        | OS file permissions                | `NSFileProtectionComplete` (hardware AES)                        |
| Index protection            | OS file permissions                | `NSFileProtectionCompleteUnlessOpen`                             |
| Backup of private keys      | Possible if disk backed up         | Impossible (`ThisDeviceOnly`)                                    |
| Key migration to new device | Manual copy                        | Impossible; user must re-pair                                    |
| Memory zeroing of secrets   | `strings.Repeat` + GC (unreliable) | Keychain delete; no Go copy exists                               |

---

## 12. SQLite Lifecycle Constraints on iOS

These rules are mandatory. Violating any of them will produce data loss or
crashes under normal iOS lifecycle conditions.

**Rule 1 — Open only after first unlock**: `index.db` has
`NSFileProtectionCompleteUnlessOpen`. The engine must not attempt to open
the database until after the device has been unlocked at least once since
boot. Gate engine startup on a `UIApplication.isProtectedDataAvailable` check
or the `applicationProtectedDataDidBecomeAvailable` notification.

**Rule 2 — Hold the connection open**: Once the database is open, keep the
connection alive for the entire engine session. Do not close and reopen per
operation. Re-opening the WAL auxiliary files (`-wal`, `-shm`) while the
device is locked will fail.

**Rule 3 — WAL mode is mandatory**: Applied immediately after every
`sql.Open()`. Never switch to `DELETE` or `TRUNCATE` journal modes.

**Rule 4 — Single writer goroutine**: All SQLite writes are serialised through
a channel-based write queue in `repo/sqlite/provider.go`. Concurrent writes
produce `SQLITE_BUSY` in WAL mode; the single-writer pattern eliminates this
entirely.

**Rule 5 — WAL files share the protection class**: The `-wal` and `-shm` files
are created by SQLite automatically. Apply `NSFileProtectionCompleteUnlessOpen`
to all three files (including the ones SQLite creates lazily). The
`applyFileProtection` method in `iOSPlatform` handles this.

**Rule 6 — Test these scenarios explicitly**:

- Device lock occurs during an active write transaction
- App moves to background and device locks before WAL checkpoint completes
- App is OOM-killed mid-write and restarted

---

## 13. Impact on Existing Desktop Code

No existing desktop behaviour is modified. All changes are additive.

### Files modified

| File                  | Change                                                          |
| --------------------- | --------------------------------------------------------------- |
| `repo/index/index.go` | Added `//go:build !ios` guard — Badger excluded from iOS builds |
| `go.mod` / `go.sum`   | Added `modernc.org/sqlite v1.48.0`                              |

### New files

| File                      | Purpose                                                       |
| ------------------------- | ------------------------------------------------------------- |
| `mobile/engine.go`        | Exported `Engine` struct — entry point for Swift              |
| `mobile/platform.go`      | `Platform` interface — single coupling point to the OS        |
| `mobile/aliases.go`       | Well-known alias and config key constants                     |
| `mobile/types.go`         | gomobile-compatible value types                               |
| `mobile/errors.go`        | Error code constants and sentinel errors                      |
| `repo/sqlite/provider.go` | SQLite-backed implementation of all repository interfaces     |
| `repo/sqlite/schema.go`   | DDL, migration runner, integrity check, version tracking      |
| `repo/sqlite/query.go`    | Read-only query helpers with cursor-based pagination and FTS5 |
| `scripts/build_ios.sh`    | `gomobile bind` invocation for iOS + Simulator                |

### Desktop code path unchanged

The desktop continues to use:

- `repo/file/provider.go` + `repo/index/index.go` (Badger)
- `internal/clientconfig/config.go` with `~/.notx/config.json`
- `internal/serverclient/pairing.go` with full gRPC token flow

These are gated by the implicit absence of the `ios` build tag and are
never compiled into the mobile framework.

---

## 14. Open Questions

**Q1 — Biometric gating on the signing key**
The current design uses `kSecAccessControlDevicePasscode` (not biometric) on
the device key so that background cert renewal works. If Face ID / Touch ID is
required for every signing operation, background sync and renewal will fail
silently when the device is locked. Requires a product decision before Phase M4.

**Q2 — Share Extension and widget access**
A Share Extension needs Keychain and data directory access. This requires the
App Group capability on both the main target and the extension, with a
consistent `accessGroup` string. `iOSPlatform.init(accessGroup:)` already
parameterises this. Confirm the App Group ID before Phase M4.

**Q3 — SQLite as the universal index**
`repo/sqlite/` compiles on all platforms. The recommended long-term path is
to adopt SQLite as the default index everywhere, then remove Badger. This can
be done incrementally: iOS first, then desktop behind a feature flag, then
Badger removal. Badger removal is out of scope for the current phases.

**Q4 — Cert renewal via silent APNs push**
`BGAppRefreshTask` has a 30-second budget and an uncertain schedule. For more
reliable renewal, the authority server could send a silent APNs push when a
cert approaches expiry. This requires backend APNs integration and is a
separate feature.

**Q5 — Index rebuild progress callback**
When the database is missing or corrupt the engine replays all `.notx` files.
For a large corpus this may take several seconds. A progress callback should
be exposed (via a `ProgressDelegate` on `NotxEngineHost` or a `Platform`
extension method) so Swift can show a loading indicator. Deferred to Phase M6.

**Q6 — Cross-device migration**
Private keys are `ThisDeviceOnly` by design — they cannot migrate to a new
iPhone. The user must re-pair. The admin UI should support one-click
"re-issue pairing token for existing device URN" so the user retains the
same device identity after device replacement.

**Q7 — Minimum iOS version confirmation**
Secure Enclave with `kSecAttrTokenIDSecureEnclave` requires iOS 9+. The
current target of iOS 16+ eliminates all edge cases. Confirm with product
before Phase M3 cuts.
