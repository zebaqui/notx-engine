# NOTX Security Model — Implementation Plan

> **Status**: Pre-Implementation Planning
> **Version**: v1.0
> **Scope**: notx-engine + notx Platform
> **Source RFC**: Notx Security Model RFC v1.1 (Final – Dual Data Model)

---

## 1. Purpose

This document translates the Notx Security Model RFC into a concrete, step-by-step implementation plan for the notx-engine. It defines **what to build**, **in what order**, and **what the acceptance criteria are** — before a single line of code is written.

No implementation work should begin without referencing this document.

---

## 2. Core Concept: Two Parallel Data Models

The entire security model rests on one principle:

> **Security is explicit and opt-in at the data level.**

Every note in the system is classified as one of two types:

| Type | Label | Who can read plaintext | Synced by server | E2EE |
|------|-------|------------------------|------------------|------|
| Normal Note | `📝` | Server + client | Yes (automatic) | ❌ |
| Secure Note | `🔒` | Client only | No (explicit sharing) | ✅ |

These are **not** two security levels — they are two entirely different data pipelines with different storage, transport, sync, and search behaviors.

---

## 3. What Must Be Built

The implementation is broken into seven distinct areas. Each area is independent enough to be designed and reviewed separately, but all seven must be complete before the security model is considered implemented.

---

### 3.1 Note Type Classification

**Goal**: Every note in the system carries an explicit, immutable type declaration.

**What to build**:
- A `note_type` field added to the `.notx` file header
  - Value: `normal` (default) or `secure`
  - This field is **set at creation time and never changed**
  - Absence of the field defaults to `normal` (backward compatibility)
- A `NoteType` enum in the engine core (`NoteTypeNormal`, `NoteTypeSecure`)
- Type enforcement at the parser level — if `note_type: secure` is present, the engine activates the secure pipeline for that note

**File header example (normal note)**:
```
# notx/1.0
# note_urn:      notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
# note_type:     normal
# name:          My Meeting Notes
# head_sequence: 4
```

**File header example (secure note)**:
```
# notx/1.0
# note_urn:      notx:note:7b2c3d4e-5f6a-7b8c-9d0e-1f2a3b4c5d6e
# note_type:     secure
# name:          Private Journal
# head_sequence: 2
```

**Acceptance criteria**:
- [ ] Parser reads `note_type` and populates `NoteType` on the parsed note struct
- [ ] Missing `note_type` defaults to `NoteTypeNormal`
- [ ] Any value other than `normal` or `secure` is a parse error
- [ ] `note_type` is immutable — appending a new event cannot change it
- [ ] All existing test fixtures remain valid (backward compat)

---

### 3.2 Device Identity Model

**Goal**: Each device has a stable cryptographic identity used exclusively for secure notes.

**What to build**:
- A `DeviceIdentity` struct containing:
  - `DeviceID` — a `notx:device:<uuid>` URN (new URN object type)
  - `PublicKey` — Ed25519 public key (serialized as base64)
  - `PrivateKey` — Ed25519 private key (**in-memory only, never serialized to server**)
- A `device` URN object type registered in the URN spec
- Key generation logic (Ed25519 key pair)
- Local-only private key storage interface (pluggable: OS keychain, encrypted file, etc.)
- Public key registration flow: client sends only `DeviceID + PublicKey` to the server

**New URN object type**:

| Type | Description | Example |
|------|-------------|---------|
| `device` | A registered user device | `notx:device:4a5b6c7d-8e9f-0a1b-2c3d-4e5f6a7b8c9d` |

**Key rules (non-negotiable)**:
- Private keys are **never** sent to the server
- Private keys are **never** written into a `.notx` file
- Public keys are stored on the server (via Vault — see §3.6)
- Keys are only used for secure note encryption/decryption

**Acceptance criteria**:
- [ ] `DeviceIdentity` can be generated (new key pair)
- [ ] `DeviceIdentity` can be serialized for local storage (public key only in exportable form)
- [ ] `DeviceID` follows the `notx:device:<uuid>` URN format and validates correctly
- [ ] Private key never appears in any log, serialized struct, or network payload
- [ ] Unit tests confirm key generation produces valid Ed25519 key pairs

---

### 3.3 Encryption Layer (Secure Notes)

**Goal**: Secure note content is encrypted before it leaves the device and never decrypted on the server.

**What to build**:

#### 3.3.1 Encryption Scheme
- **Key agreement**: X25519 ECDH (Diffie-Hellman on Curve25519)
- **Symmetric encryption**: AES-256-GCM (authenticated encryption)
- **Key derivation**: HKDF-SHA256

The flow per secure note event:
1. Generate a random **Content Encryption Key (CEK)** (AES-256 key)
2. Encrypt the event payload with CEK using AES-256-GCM
3. For each recipient device, encrypt the CEK with that device's public key (X25519 ECDH + HKDF)
4. Store: `{ encrypted_payload, nonce, per_device_wrapped_keys[] }`

#### 3.3.2 Encrypted Event Format
Secure note events use a modified lane format. The content block is replaced with an encrypted blob:

```
# notx/1.0
# note_urn:   notx:note:7b2c3d4e-5f6a-7b8c-9d0e-1f2a3b4c5d6e
# note_type:  secure
# head_sequence: 2

1:2025-01-15T09:00:00Z:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
!encrypted
nonce: <base64-nonce>
payload: <base64-ciphertext>
key[notx:device:4a5b6c7d-...]: <base64-wrapped-cek>
key[notx:device:9f8e7d6c-...]: <base64-wrapped-cek>
```

- The `!encrypted` marker replaces the normal `N | content` line entries
- The `payload` is the AES-256-GCM ciphertext of the serialized line entries
- Each `key[device-urn]` entry is that device's ECDH-wrapped copy of the CEK
- The server stores this blob verbatim — it cannot read `payload`

#### 3.3.3 Decryption Flow
1. Device loads the encrypted event from the server
2. Device locates its `key[<own-device-urn>]` entry
3. Device uses its private key + sender's public key to derive shared secret (X25519)
4. Device uses HKDF to derive the CEK from the shared secret
5. Device decrypts `payload` with CEK (AES-256-GCM)
6. Device replays the decrypted line entries as normal

**Acceptance criteria**:
- [ ] Encrypt/decrypt round-trip produces identical line entries
- [ ] Encrypted blob is opaque — no plaintext content visible in the stored format
- [ ] A device without its `key[...]` entry cannot decrypt the payload
- [ ] The `!encrypted` marker causes the parser to activate the secure decryption path
- [ ] A parser without a private key (e.g., server-side) can store and relay the blob without error
- [ ] Nonce is unique per event (never reused)

---

### 3.4 Transport Security

**Goal**: All data in transit is protected regardless of note type.

**What to build**:
- Enforce **TLS 1.3** on all server connections
  - Minimum TLS version: 1.3
  - Reject connections below TLS 1.3
- **mTLS** for device-to-server connections
  - Each device presents its `DeviceID` certificate during the TLS handshake
  - Server validates device certificate against registered device list (via Vault)
- TLS configuration should be centralized in a `transport` package (not scattered per-handler)

**Acceptance criteria**:
- [ ] Server rejects TLS 1.2 and below connections
- [ ] Device connections use mTLS with device certificate validation
- [ ] A device not registered in Vault cannot establish a connection
- [ ] TLS config is centralized and not duplicated per endpoint

---

### 3.5 Sync Model Enforcement

**Goal**: Normal and secure notes have entirely separate sync pipelines.

**Normal notes sync**:
- Server-initiated, automatic, near real-time
- Server replicates to all authorized devices
- Server may read, index, and search content

**Secure notes sync**:
- **No automatic server sync**
- Sharing is **explicit**: the sending device re-encrypts the CEK for each recipient device's public key
- The server is a **relay only** — it receives an encrypted blob and forwards it; it never initiates secure note distribution

**What to build**:
- A `SyncPolicy` type with two variants: `AutoSync` (normal) and `ExplicitRelay` (secure)
- The note type determines the sync policy at creation — this cannot be changed
- Sync pipeline router: inspect `note_type` header and dispatch to the correct pipeline
- Secure note sharing API:
  - Input: `{ note_urn, target_device_urns[] }`
  - Action: re-wrap CEK for each target device, upload new key entries to server
  - Server stores the updated key entries alongside the existing ciphertext

**Acceptance criteria**:
- [ ] A secure note is never pushed to a device without an explicit share action
- [ ] The server sync pipeline refuses to process plaintext for `note_type: secure`
- [ ] Normal notes sync automatically without any client-side crypto
- [ ] The explicit share flow produces a new `key[device-urn]` entry for each target
- [ ] A device receiving a secure share can decrypt it using its private key

---

### 3.6 Vault Integration

**Goal**: HashiCorp Vault stores public keys and access policies — nothing more.

**What Vault stores**:
- Device public keys (indexed by `DeviceID` URN)
- Identity metadata (device name, owner URN, registration timestamp)
- Access policies (which devices can receive which secure notes)

**What Vault MUST NOT store**:
- Private keys (ever, under any circumstances)
- Decrypted secure note content
- Plaintext event payloads

**What to build**:
- `VaultClient` interface with the following operations:
  - `RegisterDevice(deviceID, publicKey, metadata)` — called on device registration
  - `GetPublicKey(deviceID) → publicKey` — called when wrapping CEK for a recipient
  - `ListDevicesForUser(userURN) → []DeviceID` — for share-to-all-my-devices flow
  - `GetAccessPolicy(noteURN) → []DeviceID` — for determining share targets
  - `RevokeDevice(deviceID)` — removes device from future shares
- Vault path conventions:
  - `secret/notx/devices/<device-id>/public_key`
  - `secret/notx/devices/<device-id>/metadata`
  - `secret/notx/policies/notes/<note-urn>/devices`

**Acceptance criteria**:
- [ ] `RegisterDevice` stores only public key + metadata — no private key accepted
- [ ] `GetPublicKey` returns the raw public key bytes for a given device
- [ ] Vault paths follow the defined convention
- [ ] Private key material is never passed to any `VaultClient` method (enforced by type system)
- [ ] Vault client is injectable (interface) to allow test doubles

---

### 3.7 Search Isolation

**Goal**: Server-side search only indexes normal notes. Secure notes are invisible to the server search index.

**What to build**:
- Search indexing pipeline must check `note_type` before indexing
  - `normal` → index content, make searchable server-side
  - `secure` → **never index**, store ciphertext only
- Search query results must never return secure note content
- Client-side search interface for secure notes:
  - After local decryption, the client builds a local in-memory index
  - Local search runs against the decrypted content entirely on-device
  - Local index is never sent to the server

**Acceptance criteria**:
- [ ] Indexer skips any event with `!encrypted` marker
- [ ] Server search results never include secure note content or titles
- [ ] Client-side search is scoped to locally decrypted notes only
- [ ] Accidentally passing a secure note to the indexer is a no-op (not an error, not stored)

---

### 3.8 Browser Pairing Model

**Goal**: Browsers access secure notes only after being paired as a device via a device-to-device key transfer.

**Normal notes in browser**:
- Access directly via session auth
- No pairing required
- No crypto required

**Secure notes in browser**:
- Browser must first be **paired** with an existing trusted device (e.g., mobile app)
- Pairing flow (inspired by WhatsApp Web):
  1. Browser generates a new `DeviceIdentity` (key pair) in-browser (WebCrypto API)
  2. Browser displays a pairing QR code containing its `DeviceID + PublicKey`
  3. Trusted device scans QR code, encrypts the user's identity material for the browser's public key
  4. Browser receives the wrapped keys, stores private key in IndexedDB (encrypted at rest)
  5. Browser is now registered as a device in Vault
- Server **never** provides keys to the browser directly — only device-to-device transfer

**What to build** (engine-side contracts only — UI is out of scope for this doc):
- Pairing initiation endpoint: `POST /devices/pair/initiate` → returns session token
- Pairing completion endpoint: `POST /devices/pair/complete` → accepts wrapped keys, registers device
- Pairing session TTL: 5 minutes (QR code expiry)
- Server validates that the pairing completion comes from a currently trusted device (not from any device)

**Acceptance criteria**:
- [ ] Server never sends a private key in any pairing response
- [ ] Pairing session expires after 5 minutes
- [ ] A browser that has not been paired cannot receive secure note content (server returns 403)
- [ ] A paired browser is treated identically to any other registered device
- [ ] Pairing requires an already-authenticated trusted device to complete

---

## 4. Implementation Phases

The work is sequenced to allow incremental delivery. Each phase builds on the previous.

### Phase 1 — Foundation (No Crypto)
Build the classification and type system without any encryption yet.

- [ ] Add `note_type` header field to `.notx` format spec (update `NOTX_FORMAT.md`)
- [ ] Add `NoteType` enum and parsing to `core/note.go`
- [ ] Add `device` URN object type to `core/urn.go` (update `NOTX_URN_SPEC.md`)
- [ ] Update parser to recognize `note_type` header
- [ ] Update all existing test fixtures to be explicitly `note_type: normal`
- [ ] Confirm all existing tests pass

**Exit criteria**: The engine can distinguish normal vs. secure notes at parse time. No crypto yet.

---

### Phase 2 — Device Identity
Build key generation and device registration, no note encryption yet.

- [ ] Implement `DeviceIdentity` struct (Ed25519 key pair)
- [ ] Implement `DeviceID` URN generation
- [ ] Implement local private key storage interface
- [ ] Implement `VaultClient` interface + mock
- [ ] Implement `RegisterDevice` flow (public key → Vault)
- [ ] Unit tests for key generation and Vault registration

**Exit criteria**: A device can be created, its public key registered, and its private key stays local.

---

### Phase 3 — Encryption Core
Build the encrypt/decrypt pipeline for secure note events.

- [ ] Implement X25519 key agreement + HKDF-SHA256 key derivation
- [ ] Implement AES-256-GCM encrypt/decrypt
- [ ] Implement CEK generation and per-device wrapping
- [ ] Implement `!encrypted` event format (serialization + parsing)
- [ ] Round-trip tests: encrypt on device A, decrypt on device B
- [ ] Confirm server-side parser stores encrypted blob without error

**Exit criteria**: Secure note events can be encrypted on one device and decrypted on another. Server remains blind.

---

### Phase 4 — Sync and Share
Build the two sync pipelines and the explicit share flow.

- [ ] Implement `SyncPolicy` and pipeline router
- [ ] Implement normal note auto-sync pipeline
- [ ] Implement secure note relay (server stores+forwards ciphertext only)
- [ ] Implement explicit share API (CEK re-wrapping for new recipients)
- [ ] Integration tests: normal note syncs automatically, secure note requires explicit share

**Exit criteria**: The two pipelines are fully separate. Secure notes cannot accidentally enter the normal sync path.

---

### Phase 5 — Transport Security
Harden all connections.

- [ ] Configure TLS 1.3 minimum on all server endpoints
- [ ] Implement mTLS device certificate validation
- [ ] Integrate device certificate with Vault registration
- [ ] Tests: reject TLS 1.2 connections, reject unregistered device certificates

**Exit criteria**: All connections are TLS 1.3+. Device connections use mTLS.

---

### Phase 6 — Search Isolation
Keep secure notes out of the server search index.

- [ ] Add `note_type` check to indexing pipeline
- [ ] Add `!encrypted` check to event indexer
- [ ] Implement client-side search interface contract
- [ ] Tests: secure notes produce no search results server-side

**Exit criteria**: Server search is completely blind to secure note content.

---

### Phase 7 — Browser Pairing
Enable browser access to secure notes via device pairing.

- [ ] Implement pairing initiation endpoint
- [ ] Implement pairing completion endpoint (with TTL enforcement)
- [ ] Implement paired browser treated as full device
- [ ] Tests: unpaired browser gets 403, paired browser can decrypt

**Exit criteria**: Browsers can access secure notes only after device-to-device pairing.

---

## 5. Non-Negotiable Rules

These rules apply to every phase and every pull request touching the security model. They are not guidelines — they are hard constraints.

| # | Rule |
|---|------|
| 1 | **Never store private keys on the server.** Not in Vault, not in the database, not in logs. |
| 2 | **Never store secure note plaintext on the server.** The server only ever sees ciphertext. |
| 3 | **Never imply normal notes are end-to-end encrypted.** In code comments, UI strings, or documentation. |
| 4 | **Never allow the server to initiate a secure note decrypt.** Decryption happens on-device only. |
| 5 | **Never index secure note content.** Not even the title. The `name` field in the header is the only server-visible metadata. |
| 6 | **note_type is immutable.** Once set at creation, it cannot be changed by any event or API call. |
| 7 | **Browser pairing is device-to-device.** The server never participates in key transfer, only in device registration after the fact. |

---

## 6. Security Guarantees by Note Type

### Normal Notes

| Guarantee | Applies |
|-----------|---------|
| Encrypted in transit (TLS 1.3) | ✅ |
| Access-controlled (auth + authz) | ✅ |
| Server can read plaintext | ✅ (by design) |
| Server-side indexing and search | ✅ |
| End-to-end encrypted | ❌ |

### Secure Notes

| Guarantee | Applies |
|-----------|---------|
| Encrypted in transit (TLS 1.3) | ✅ |
| End-to-end encrypted (E2EE) | ✅ |
| Server cannot read plaintext | ✅ |
| Private keys never leave device | ✅ |
| Server-side indexing and search | ❌ (by design) |
| Automatic server sync | ❌ (explicit sharing only) |

---

## 7. Affected Files and New Files

### New files to create

| File | Purpose |
|------|---------|
| `core/security.go` | `NoteType` enum, `DeviceIdentity`, `SecurityPolicy` |
| `core/crypto.go` | X25519 ECDH, HKDF, AES-256-GCM encrypt/decrypt |
| `core/device.go` | Device ID generation, local key storage interface |
| `internal/vault/client.go` | `VaultClient` interface |
| `internal/vault/mock.go` | In-memory mock for tests |
| `internal/sync/router.go` | `SyncPolicy` and pipeline router |
| `internal/sync/normal.go` | Normal note auto-sync pipeline |
| `internal/sync/secure.go` | Secure note relay pipeline |
| `internal/pairing/pairing.go` | Browser pairing flow |
| `internal/search/indexer.go` | Note-type-aware search indexer |

### Existing files to modify

| File | Change |
|------|--------|
| `core/note.go` | Add `NoteType` field to `Note` struct |
| `core/parser.go` | Parse `note_type` header field |
| `core/parser_v1.go` | Handle `!encrypted` event marker |
| `core/urn.go` | Add `device` URN object type |
| `docs/NOTX_FORMAT.md` | Document `note_type` header field and `!encrypted` event format |
| `docs/NOTX_URN_SPEC.md` | Document `device` URN type |
| `docs/NOTX_FILE_SEMANTICS.md` | Document secure note file semantics |
| `README.md` | Add security model overview and link to this doc |

---

## 8. Open Questions (Must Resolve Before Phase 3)

These questions must have agreed answers before encryption work begins.

| # | Question | Why It Matters |
|---|----------|----------------|
| 1 | Where does the browser store the private key? (IndexedDB + AES-GCM wrap? WebCrypto non-extractable key?) | Determines browser security posture |
| 2 | What happens when a device is lost? Is there a secure note recovery mechanism? | If private key is gone, secure notes on that device are unrecoverable. Is that acceptable? |
| 3 | Does the `name` field in a secure note header stay plaintext? | Server can see note titles if yes. If no, search and display are harder. |
| 4 | What is the CEK rotation policy? (Per-event? Per-session? On device revocation?) | Impacts forward secrecy guarantees |
| 5 | Does self-hosted mode change the encryption requirement? | RFC says self-hosted users "may treat all notes as trusted" — does this mean E2EE can be disabled? |

---

## 9. What This Document Is NOT

- **Not a UI spec** — UX indicators (📝 / 🔒) are required by the RFC but designed separately
- **Not an API spec** — HTTP endpoints are described by intent here; full API contracts are separate
- **Not a cryptography audit** — The scheme described (X25519 + AES-256-GCM + HKDF) is standard and well-reviewed, but this document is not a formal security proof
- **Not final** — Open questions in §8 may require revisions to phases 3–4

---

## 10. References

| Document | Role |
|----------|------|
| `notx_secure_model_rfc.md` (v1.1) | Source of truth for security requirements |
| [`NOTX_FORMAT.md`](./NOTX_FORMAT.md) | Base file format — extended by this model |
| [`NOTX_URN_SPEC.md`](./NOTX_URN_SPEC.md) | URN scheme — extended with `device` type |
| [`NOTX_FILE_SEMANTICS.md`](./NOTX_FILE_SEMANTICS.md) | File semantics — extended with secure note behavior |
| [`NOTX_NAMESPACE_CLARIFICATION.md`](./NOTX_NAMESPACE_CLARIFICATION.md) | Instance model — security model respects instance boundaries |
```

Now let me create that file and update the README:
