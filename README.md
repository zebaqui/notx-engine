# notx-engine

A simple, in-memory consumer and parser for the **notx** event-sourced document format.

## Installation

The easiest way to install `notx` on macOS is via [Homebrew](https://brew.sh):

```sh
brew tap zebaqui/notx
brew install notx
```

Or as a one-liner:

```sh
brew install zebaqui/notx/notx
```

Once installed, upgrade to the latest release at any time with:

```sh
brew upgrade notx
```

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

---

## Embedding the Engine in an iOS App

This is a step-by-step guide for embedding `notx-engine` into an iOS app using `gomobile bind`. The result is a native `.xcframework` that Swift can call directly — no HTTP, no IPC, just function calls.

### Prerequisites

- Go 1.21+
- Xcode 15+
- `gomobile` and `gobind` installed

```sh
go install golang.org/x/mobile/cmd/gomobile@latest
go install golang.org/x/mobile/cmd/gobind@latest
gomobile init
```

---

### Step 1 — Build the xcframework

From the root of `notx-engine`, run:

```sh
gomobile bind \
  -target ios \
  -o Notx.xcframework \
  github.com/zebaqui/notx-engine/mobile
```

This compiles the `mobile` package into `Notx.xcframework`. Only types and methods exported from the `mobile` package are bridged to Swift — nothing else is exposed.

This takes a minute or two the first time. The output file is self-contained and includes slices for the simulator and device.

---

### Step 2 — Add the framework to Xcode

1. In Xcode, select your app target → **General** tab.
2. Scroll to **Frameworks, Libraries, and Embedded Content**.
3. Click **+** → **Add Other…** → **Add Files…**
4. Select `Notx.xcframework`.
5. Set embed to **Embed & Sign**.

The framework will appear in your project navigator. You don't need to add it to any build phases manually — Xcode handles it.

---

### Step 3 — Implement the Platform protocol in Swift

The engine needs a `Platform` object to handle keychain, filesystem, and config operations. Create a Swift class that conforms to `MobilePlatform` (gomobile generates this name from the Go `Platform` interface):

```swift
import Notx

final class iOSPlatform: NSObject, MobilePlatform {

    // Return the app's Application Support directory.
    func dataDir() throws -> String {
        let url = try FileManager.default.url(
            for: .applicationSupportDirectory,
            in: .userDomainMask,
            appropriateFor: nil,
            create: true
        )
        let notxURL = url.appendingPathComponent("notx", isDirectory: true)
        try FileManager.default.createDirectory(at: notxURL, withIntermediateDirectories: true)
        return notxURL.path
    }

    // Config: backed by UserDefaults
    func getConfig(_ key: String) throws -> String {
        return UserDefaults.standard.string(forKey: key) ?? ""
    }

    func setConfig(_ key: String, value: String) throws {
        UserDefaults.standard.set(value, forKey: key)
    }

    // Key + cert operations: stub these out to return errors until
    // you wire up Secure Enclave / Keychain (needed for pairing only).
    func generateKey(_ alias: String) throws -> Data { throw PlatformError.notImplemented }
    func sign(_ alias: String, digest: Data) throws -> Data { throw PlatformError.notImplemented }
    func buildCSR(_ alias: String, commonName: String) throws -> Data { throw PlatformError.notImplemented }
    func publicKeyDER(_ alias: String) throws -> Data { throw PlatformError.notImplemented }
    func deleteKey(_ alias: String) throws { throw PlatformError.notImplemented }
    func hasKey(_ alias: String) throws -> Bool { return false }
    func storeCert(_ alias: String, certPEM: Data) throws { throw PlatformError.notImplemented }
    func loadCert(_ alias: String) throws -> Data { throw PlatformError.notImplemented }
    func deleteCert(_ alias: String) throws { throw PlatformError.notImplemented }
    func hasCert(_ alias: String) throws -> Bool { return false }
}

enum PlatformError: Error { case notImplemented }
```

For local mode (no pairing, no sync) the key/cert stubs are fine — the engine only calls them during the pairing and cert-renewal flows.

---

### Step 4 — Create the engine at app startup

In your `App` struct or `AppDelegate`, create one engine instance and keep it alive for the lifetime of the app:

```swift
import Notx

@main
struct MyApp: App {
    // Hold the engine alive for the full app session.
    private let engine: MobileEngine = {
        let platform = iOSPlatform()
        return try! MobileNew(platform)   // gomobile generates MobileNew from mobile.New
    }()

    var body: some Scene {
        WindowGroup {
            ContentView()
        }
    }
}
```

---

### Step 5 — Call engine methods from Swift

Once you have an engine instance, creating and listing data is straightforward:

```swift
// Create a project
let projectURN = try engine.createProject("My Project")

// Create a folder inside it
let folderURN = try engine.createFolder("Design", projectURN: projectURN)

// Create a note inside the folder
let noteURN = try engine.createNote("API spec", projectURN: projectURN, folderURN: folderURN)

// Write content (stored as an event in the SQLite index)
try engine.appendNoteContent(noteURN, content: "# API spec\n\nFirst draft.")

// Read it back
let content = try engine.getNoteContent(noteURN)

// List all projects
let projects = try engine.listProjects()   // returns [MobileProjectHeader]

// List notes filtered by project
let opts = MobileListOptions()
opts.projectURN = projectURN
let notes = try engine.listNotes(opts)    // returns MobileNoteList
```

All methods follow the `(value, error)` → Swift `throws` pattern that gomobile generates automatically.

---

### Step 6 — Replace the local EngineStore with the real engine

The app currently uses `EngineStore` (a `UserDefaults`-backed local-mode store) that mirrors the engine's API. Once the framework is linked, swap each method body in `EngineStore.swift` to delegate to the real engine:

```swift
// Before (local mode)
func createProject(name: String) -> ProjectItem {
    let project = ProjectItem(id: UUID(), name: name)
    projects.append(project)
    persist()
    return project
}

// After (real engine)
func createProject(name: String) -> ProjectItem {
    let urn = try! engine.createProject(name)
    let project = ProjectItem(id: uuidFrom(urn: urn), name: name)
    projects.append(project)
    return project
}
```

The views and navigation never need to change — they only talk to `EngineStore`.

---

### What each layer does

```
┌─────────────────────────────────┐
│         SwiftUI Views           │  NotesListView, MarkdownEditorView, …
├─────────────────────────────────┤
│          EngineStore            │  @Observable singleton, owns app state
├─────────────────────────────────┤
│       Notx.xcframework          │  gomobile-compiled mobile package
├─────────────────────────────────┤
│    SQLite index  +  .notx files │  Stored in Application Support/notx/
└─────────────────────────────────┘
```

- **Views** never touch the engine directly — they read from and write to `EngineStore`.
- **EngineStore** is the only place that calls engine methods — swap the bodies here to go from local mode to live engine.
- **The framework** handles all persistence, event sourcing, and full-text search.
- **SQLite + notes files** live in the app's sandboxed Application Support directory.
