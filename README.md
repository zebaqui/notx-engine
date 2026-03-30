# notx-engine

A simple, in-memory consumer and parser for the **notx** event-sourced document format.

## What is notx?

**notx** is a plain-text format for storing documents with complete history. Instead of saving full copies of a document each time it changes, notx saves only the changes — line-by-line edits that can be replayed to reconstruct any version.

Every `.notx` file is:

- **Complete** — Contains the entire history of every change made to the document
- **Portable** — Self-contained files that can be moved, shared, and imported anywhere
- **Auditable** — Every change is attributed to an author with a timestamp
- **Efficient** — Takes up much less space than storing full snapshots
- **Human-readable** — Plain text format that you can read and understand without special tools

## What is notx-engine?

**notx-engine** is a lightweight library for reading and working with `.notx` files. It lets you:

- Parse `.notx` files from disk or other sources
- Reconstruct the document at any point in its history
- Extract metadata about the document, authors, and versions
- Understand cross-instance document references

It's designed to be simple and focused — a building block you can use in larger applications rather than a complete backend system.

## Documentation

All notx documents and specifications are available here:

- **[NOTX_FORMAT.md](./docs/NOTX_FORMAT.md)** — The complete format specification
  - How `.notx` files are structured
  - The line-by-line change format (called "lane format")
  - How to read and parse files
  - How to replay events to get any version of the document

- **[NOTX_FILE_SEMANTICS.md](./docs/NOTX_FILE_SEMANTICS.md)** — How files work in practice
  - Metadata headers and what they contain
  - How versions and sequences work
  - Snapshot optimization for faster replay
  - Reading, writing, and recovering files

- **[NOTX_URN_SPEC.md](./docs/NOTX_URN_SPEC.md)** — Unique identifiers for everything
  - How notes, users, projects, and organizations are identified
  - The URN format: `<namespace>:<type>:<uuid>`
  - Entity metadata schemas
  - How to resolve references

- **[NOTX_NAMESPACE_CLARIFICATION.md](./docs/NOTX_NAMESPACE_CLARIFICATION.md)** — Understanding instances
  - The difference between the official notx platform and self-hosted instances
  - How instances can reference each other's documents
  - What data is shared and what stays private

- **[NOTX_SECURITY_MODEL.md](./docs/NOTX_SECURITY_MODEL.md)** — Security model implementation plan
  - The dual data model: Normal Notes vs. Secure Notes
  - End-to-end encryption for secure notes (device-only decryption)
  - Device identity, key management, and browser pairing
  - Phased implementation plan and acceptance criteria

- **[SERVER.md](./docs/SERVER.md)** — notx server operational reference
  - Configuration, storage layout, and directory structure
  - HTTP/JSON API routes and gRPC services
  - TLS, mTLS, and the dual-listener pairing architecture
  - Running the server, lifecycle, and graceful shutdown

- **[SERVER_PAIRING.md](./docs/SERVER_PAIRING.md)** — Server-to-server pairing design document
  - How two notx instances establish mutual mTLS trust
  - Authority CA bootstrap, pairing secret format, and protocol flow
  - Hard revocation via in-memory deny-set at the TLS handshake layer
  - Automatic certificate renewal on joining servers

- **[CLI.md](./docs/CLI.md)** — CLI command reference
  - All `notx` sub-commands and flags
  - `notx server pairing add-secret` for generating registration tokens
  - Configuration file seeding and gRPC client credential selection

## Security Model

notx supports two distinct note types with different security guarantees:

| Type        | Indicator | Encrypted at Rest | Server Can Read | Auto-Synced         | Searchable (Server) |
| ----------- | --------- | ----------------- | --------------- | ------------------- | ------------------- |
| Normal Note | 📝        | No                | Yes             | Yes                 | Yes                 |
| Secure Note | 🔒        | Yes (E2EE)        | No              | No (explicit share) | No                  |

**Security is explicit and opt-in at the data level.** Normal notes are platform-protected (TLS + access control). Secure notes are end-to-end encrypted — the server stores only ciphertext and can never read the content.

See [NOTX_SECURITY_MODEL.md](./docs/NOTX_SECURITY_MODEL.md) for the full implementation plan.

## Use Cases

- **Document version control** — View the complete history of changes without git
- **Audit trails** — Track who made what changes and when
- **Portable documents** — Export and import documents between different notx instances
- **Read-only archives** — Access document history without a database
- **Local-first applications** — Embed notx parsing in desktop or mobile apps

## Namespace Model

notx supports multiple independent instances, each with its own namespace:

- **Official Platform**: Uses the `notx` namespace
- **Self-Hosted Instances**: Each organization chooses its own namespace (e.g., `acme`, `mycompany`)

Documents can reference each other across instances. When they do, the system resolves basic metadata (names, authors, timestamps) for display, but never syncs or copies the actual document data.

See [NOTX_NAMESPACE_CLARIFICATION.md](./docs/NOTX_NAMESPACE_CLARIFICATION.md) for more details.

## Why notx?

notx is designed around how people actually work with documents:

- Store complete history without huge storage costs
- Understand who changed what and why
- Move documents between systems without losing information
- Build portable, shareable document archives
- Support open-ended integrations through federation

The format is simple, text-based, and designed to be understood and implemented by anyone.

## Resources

- **Format Specifications**: Start with [NOTX_FORMAT.md](./docs/NOTX_FORMAT.md)
- **Security Model**: See [NOTX_SECURITY_MODEL.md](./docs/NOTX_SECURITY_MODEL.md)
- **Issues & Questions**: [GitHub Issues](https://github.com/yourusername/notx-engine/issues)
