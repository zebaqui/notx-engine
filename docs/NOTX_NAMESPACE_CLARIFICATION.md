# notx Namespace Clarification

## The Core Distinction

notx uses **namespaces** to distinguish between:

1. **The official notx Platform** — A SaaS offering operated by notx
2. **Self-hosted notx Instances** — Private deployments run by organizations

Every URN (entity identifier) includes a namespace that encodes which instance owns the resource.

## The Official notx Platform

- **Namespace**: `notx` (reserved)
- **Operator**: notx Inc.
- **Scope**: SaaS multi-tenant platform
- **Examples**:
  ```
  notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
  notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
  notx:proj:3a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d
  ```

The namespace `notx` is **reserved** and can only be used for the official platform. No other instance, organization, or deployment may use the `notx` namespace.

## Self-Hosted Instances

- **Namespace**: Custom, chosen by the deployer (e.g., `acme`, `mycompany`, `internal`)
- **Operator**: The organization running the instance
- **Scope**: Private, independent from all other instances
- **Examples** (for organization "Acme Corp"):
  ```
  acme:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
  acme:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
  acme:proj:3a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d
  ```

Each self-hosted instance chooses its own namespace. This namespace uniquely identifies that instance globally. Two different organizations cannot both use the namespace `acme`—the first to register it owns it.

## Federation: What It Means

Federation means that instances can **reference each other's resources** and **resolve metadata** across instance boundaries. It does **not** mean data sharing or replication.

### What Federation Enables

1. **Cross-instance URN references** — A note on `acme` can reference a note on `mycompany` or `notx`
   ```
   # In acme:note:018e4f2a...
   node_links:
     requirements: "mycompany:note:7c3e9f1a..."
     api_docs: "notx:note:9c8d7e6f..."
   ```

2. **Metadata resolution for UX** — When displaying a link to `mycompany:note:...`, the `acme` instance can query `mycompany`'s metadata API to get the note's name, author, and timestamp
   ```
   Display: "See mycompany:note:7c3e9f1a (Requirements [by Alice Smith, Jan 15])"
   ```

3. **User metadata display** — When showing `mycompany:usr:...` as an author, display their name and avatar from their home instance

### What Federation Does NOT Enable

1. **No content fetching** — The `acme` instance never downloads the content (events, payload) of `mycompany:note:...`. The note's data remains on `mycompany` forever.

2. **No data replication** — Changes to a `mycompany:note:...` are not mirrored to `acme`. Cross-references are read-only.

3. **No access override** — If you don't have permission to view `mycompany:note:...` on `mycompany`, you cannot access it via `acme` either. Access control is per-instance.

4. **No data API keys** — The metadata resolution uses restrictive, read-only API keys that expose only:
   - Note: name, author_urn, created_at, updated_at (NO content, NO events)
   - User: name, profile_pic (NO email, NO organization)
   - Organization: name (NO members, NO private info)

5. **No mutual data sharing** — Two instances do not trade event logs or sync content. Instances remain completely independent sources of truth for their own data.

## Namespace Registration

### Why Register?

If your organization runs a self-hosted instance and wants to enable federation (optional), you register your namespace in a **namespace registry**. This registry is purely informational and enables:

- **Discovery** — Other instances can find your metadata API endpoint
- **UX improvement** — When showing cross-instance references, instances know how to resolve metadata
- **Validation** — Instances can verify that a namespace actually exists (prevents typos from silently failing)

### What Gets Registered?

```json
{
  "namespace": "acme",
  "instance_name": "Acme Corp notx",
  "metadata_api": "https://notes.acme.internal/api/v1/metadata",
  "public": false,
  "admin_email": "admin@acme.com"
}
```

- `namespace` — The unique identifier for your instance
- `instance_name` — Human-readable name
- `metadata_api` — The HTTPS endpoint for metadata queries (read-only)
- `public` — Whether other instances can discover you (may be private/internal)
- `admin_email` — Contact for registry issues

### What Does NOT Get Registered?

- User credentials or authentication tokens
- Database connection strings
- Private content or proprietary data
- Access control lists
- Event histories or note payloads

The registry is **metadata-only**. It reveals only that your instance exists and where to ask for safe, public metadata.

### Metadata API

The metadata API is accessed with a **restricted, read-only API key**. When instance A queries instance B's metadata API:

```
POST https://notes.acme.internal/api/v1/metadata
Authorization: Bearer <read-only-api-key>
Content-Type: application/json

{
  "urn": "acme:note:7c3e9f1a..."
}
```

Response:
```json
{
  "urn": "acme:note:7c3e9f1a...",
  "name": "Requirements",
  "author_urn": "acme:usr:2d3e4f5a...",
  "created_at": "2025-01-15T09:00:00Z",
  "updated_at": "2025-01-15T14:30:00Z"
}
```

**Critically**: There is no `content`, `events`, `payload`, or `head_sequence` in the response. The requesting instance learns only enough to display a helpful link. It cannot access the actual data.

## Examples

### Scenario: Acme and MyCompany Both Run notx

**Acme's instance:**
- Namespace: `acme`
- Registered in the registry
- Metadata API at: `https://notes.acme.internal/api/v1/metadata`

**MyCompany's instance:**
- Namespace: `mycompany`
- Registered in the registry
- Metadata API at: `https://notes.mycompany.io/api/v1/metadata`

**Acme's note references MyCompany's:**

```
# acme:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
# name: Vendor Integration Plan

See requirements: mycompany:note:7c3e9f1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
```

**When Acme displays this note:**

1. Parser encounters the URN `mycompany:note:7c3e9f1a...`
2. Acme queries its registry for `mycompany` → gets `metadata_api = https://notes.mycompany.io/api/v1/metadata`
3. Acme sends a metadata query with its restricted API key
4. MyCompany responds: `{ urn: "mycompany:note:...", name: "Requirements", author_urn: "mycompany:usr:...", created_at: "2025-01-15..." }`
5. Acme caches this for 1 hour
6. Acme displays: "See requirements: [Requirements](by Alice Smith, Jan 15)"
7. The actual content of `mycompany:note:...` is **never** fetched, cached, or accessible to Acme

**If MyCompany is unreachable:**

Acme gracefully degrades and displays the raw URN:
```
See requirements: mycompany:note:7c3e9f1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
```

### Scenario: Two Acme Offices (Both on Same Instance)

If two teams within Acme both use the same self-hosted instance (`acme`), they can:

- Reference each other's notes freely (same URN namespace)
- Access each other's content (if permissions allow)
- Edit and replicate notes

This is **not** federation—both teams share the same instance. Federation only applies across instance boundaries.

## The Anonymous Author Sentinel

Unauthenticated or unknown edits use an instance-specific sentinel:

- On `notx` platform: `notx:usr:anon`
- On `acme` instance: `acme:usr:anon`
- On `mycompany` instance: `mycompany:usr:anon`

This sentinel is **not** resolvable—it has no record in the user table. It simply means "the author of this edit is unknown".

## Key Principles

1. **Instance Autonomy** — Each instance is completely independent. Data never flows between instances automatically.

2. **Namespace = Home** — The namespace in a URN is the definitive statement of which instance owns the resource and where its authoritative copy lives.

3. **Read-Only References** — Cross-instance URN references are read-only pointers. The referenced resource lives on its home instance forever.

4. **Metadata ≠ Data** — Federation exposes only safe, public metadata (names, timestamps, authors). Never content or events.

5. **No Data Sharing** — Even if instance A has permission to view instance B's notes, there is no mechanism for A to download or cache B's event logs. A can only query B's metadata API.

6. **Privacy Boundary** — The metadata API uses API keys and can be made private. Instance A can choose to allow or deny metadata queries from instance B.

## Implementation Checklist

For a self-hosted notx instance that wants federation:

- [ ] Choose a unique namespace (e.g., `acme`)
- [ ] Configure namespace in the notx server config
- [ ] Implement the metadata API endpoint (read-only, restricted API keys)
- [ ] Register the namespace and metadata API URL in the registry (optional but recommended)
- [ ] Generate restricted read-only API keys for peer instances
- [ ] Document the metadata API contract (which fields are exposed)
- [ ] Test cross-instance URN resolution (query other instances' metadata APIs)

For a standalone instance with no federation:

- [ ] Choose a namespace
- [ ] Disable federation (metadata API is unreachable)
- [ ] Cross-instance URN references still work (graceful degradation: raw URNs displayed)

## Relationship to `.notx` Files

When exporting a note to a `.notx` file:

```
# notx/1.0
# note_urn:      acme:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
# name:          Vendor Integration
# project_urn:   acme:proj:3a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d
# created_at:    2025-01-15T09:00:00Z
# head_sequence: 4

1:2025-01-15T09:00:00Z:acme:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
1 | See requirements: mycompany:note:7c3e9f1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
```

The file carries the namespace everywhere. When imported into another instance, the namespace tells the importer:

- This note originated on the `acme` instance
- The event author is `acme:usr:7f3e9c1a...` (lives on acme)
- The cross-reference is to `mycompany:note:...` (lives on mycompany)

The importing instance respects these URNs as foreign references and does not claim ownership.
