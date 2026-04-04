# notx Namespace & Identity Clarification

This document explains how **object identity**, **namespace**, and **server authority** work in the notx system. If you're coming from the old model where namespace was encoded in the URN, read the [What Changed](#what-changed-from-the-old-model) section first.

---

## The Core Principle

> **Namespace is a label. It is not identity.**

In the current notx model, every object has a **globally unique, immutable URN** that is independent of namespace, server, or location. Namespace is a separate metadata field — a logical grouping tag stored alongside the object, not encoded in its identifier.

---

## URN Format

All notx URNs follow this format:

```
urn:notx:<type>:<id>
```

| Component  | Description                                 | Example                      |
| ---------- | ------------------------------------------- | ---------------------------- |
| `urn:notx` | Scheme prefix — universal for all notx URNs | `urn:notx`                   |
| `<type>`   | Object type                                 | `note`, `usr`, `proj`, `srv` |
| `<id>`     | Globally unique identifier (ULID or UUID)   | `01HZX3K8J9X2M4P7R8T1Y6ZQ`   |

### Examples

```
urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ
urn:notx:usr:01HZUSER987654321ABCDEFGH
urn:notx:proj:01HZPROJ111222333444555AA
urn:notx:srv:01HZSERVER123456789ZZZZZZ
```

A URN never changes. It does not encode which server owns the object. It does not encode a namespace. It simply says: _"this is a notx note/user/project, and its unique ID is X."_

---

## The Object Data Model

Every notx object carries three identity-related fields:

```json
{
  "id":        "urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ",
  "authority": "urn:notx:srv:01HZSERVER123456789ZZZZZZ",
  "namespace": "acme",
  ...
}
```

| Field       | Type         | Description                                                              |
| ----------- | ------------ | ------------------------------------------------------------------------ |
| `id`        | URN          | Globally unique, immutable object identity. Never changes.               |
| `authority` | Server URN   | The server that owns and manages this object. This is how you find it.   |
| `namespace` | String label | Logical grouping tag for UI, filtering, and multi-tenancy. Not identity. |

### What each field does

- **`id`** — The object's permanent name in the universe. If the object moves servers, the `id` stays the same.
- **`authority`** — Points to the server URN responsible for this object. Used for routing and resolution. _This_ is the location anchor, not the namespace.
- **`namespace`** — A human-friendly label like `"acme"` or `"internal"`. Used to group objects logically. Has no bearing on where the object lives or who can resolve it.

---

## What Namespace IS

Namespace is a **logical grouping label**. Think of it like a folder name or a tenant tag. It is useful for:

- **Multi-tenancy within a cluster** — A cluster of servers all serving the `acme` namespace can use this field to scope queries and filter results.
- **UI and filtering** — Display notes "in the acme namespace" without caring which server they live on.
- **Routing hints** — A load balancer or gateway may use namespace to route requests to the right cluster, but this is advisory, not authoritative.
- **Human-readable context** — Helps operators and users understand which logical domain an object belongs to.

Namespace is **not** required to be globally unique. Two completely different servers — run by two completely different organizations — can both use the namespace `"acme"`. That is fine, because namespace is not identity.

---

## What Namespace Is NOT

| Namespace is NOT...              | Why                                                                                    |
| -------------------------------- | -------------------------------------------------------------------------------------- |
| Part of the URN                  | URNs are `urn:notx:<type>:<id>` — namespace appears nowhere in them                    |
| An instance identity anchor      | Servers are identified by their own server URN (`urn:notx:srv:<id>`), not by namespace |
| Required to be globally unique   | Multiple servers can share a namespace; authority distinguishes them                   |
| Used for cross-server resolution | Resolution uses the `authority` field and the routing table, not namespace             |
| A reserved keyword               | `"notx"` is the URN scheme prefix for all objects, not a namespace anyone owns         |

---

## How Identity Actually Works

Object identity in notx is a two-part system:

### 1. The Object's Own URN — what it is

```
urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ
```

This is globally unique and immutable. It is generated once at object creation and never changes, regardless of where the object moves or who serves it.

### 2. The Authority Field — where to find it

```json
"authority": "urn:notx:srv:01HZSERVER123456789ZZZZZZ"
```

This is a server URN pointing to the server responsible for the object. Server URNs _are_ globally unique — each server generates its own unique URN at startup or registration. If you encounter an object and need to fetch its content or verify its state, you look up the authority server.

Together: **`id` tells you what the object is; `authority` tells you who owns it.**

---

## Clusters and Namespace Sharing

Multiple servers **can and often will** share a namespace. This is by design, not a collision.

**Example: Acme Corp runs a three-node notx cluster**

All three servers serve the `"acme"` namespace, but each has its own unique server URN:

| Server      | Server URN                              | Namespace |
| ----------- | --------------------------------------- | --------- |
| acme-node-1 | `urn:notx:srv:01HZSVR1AAAAAAAAAAAAAAAA` | `acme`    |
| acme-node-2 | `urn:notx:srv:01HZSVR2BBBBBBBBBBBBBBBB` | `acme`    |
| acme-node-3 | `urn:notx:srv:01HZSVR3CCCCCCCCCCCCCCCC` | `acme`    |

A note owned by `acme-node-2` looks like this:

```json
{
  "id": "urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ",
  "authority": "urn:notx:srv:01HZSVR2BBBBBBBBBBBBBBBB",
  "namespace": "acme",
  "name": "Q3 Roadmap"
}
```

The namespace `"acme"` is shared by all three nodes. The `authority` field is what uniquely distinguishes which node owns any given object. If you need to fetch that note, you resolve `urn:notx:srv:01HZSVR2BBBBBBBBBBBBBBBB` — not the namespace.

---

## The Routing Table

Each notx node maintains a **routing table** that maps server URNs to their network endpoints. This is how cross-server resolution works.

```
Routing Table (on acme-node-1)
──────────────────────────────────────────────────────────────────
Server URN                                  │ Endpoint
────────────────────────────────────────────┼─────────────────────
urn:notx:srv:01HZSVR2BBBBBBBBBBBBBBBB       │ https://acme-2.internal:4000
urn:notx:srv:01HZSVR3CCCCCCCCCCCCCCCC       │ https://acme-3.internal:4000
urn:notx:srv:01HZEXTERNAL99999999999        │ https://notes.mycompany.io
urn:notx:srv:01HZPLATFORM00000000000        │ https://platform.notx.io
──────────────────────────────────────────────────────────────────
```

Resolution is always: **`object.authority` → routing table → endpoint → fetch**. Namespace is not consulted.

---

## Federation: Cross-Server Resolution

Federation is the ability for one notx node to reference and resolve objects owned by a different server.

### How it works (step by step)

Suppose `acme-node-1` is rendering a note that contains a reference to an object owned by an external server:

**The referenced object:**

```json
{
  "id": "urn:notx:note:01HZEXTERNAL555555555555",
  "authority": "urn:notx:srv:01HZEXTERNAL99999999999",
  "namespace": "mycompany",
  "name": "Vendor Requirements"
}
```

**Resolution steps on `acme-node-1`:**

1. Node encounters `urn:notx:note:01HZEXTERNAL555555555555` in a cross-reference.
2. Node looks up this object's `authority`: `urn:notx:srv:01HZEXTERNAL99999999999`.
3. Node consults its routing table → finds endpoint `https://notes.mycompany.io`.
4. Node sends a metadata query to `https://notes.mycompany.io` for the object.
5. Response includes name, author URN, timestamps — enough for display.
6. Node renders the reference: _"Vendor Requirements (by Alice, Jan 15)"_

**If the remote server is unreachable:**

The node gracefully degrades and displays the raw URN:

```
urn:notx:note:01HZEXTERNAL555555555555
```

Notice that at **no point** was the namespace `"mycompany"` used for routing or resolution. The routing was done entirely through the `authority` server URN and the routing table.

### What federation enables

| Capability                                                | Supported |
| --------------------------------------------------------- | --------- |
| Cross-server URN references                               | ✅ Yes    |
| Metadata resolution for display (name, author, timestamp) | ✅ Yes    |
| Graceful degradation when remote is unreachable           | ✅ Yes    |
| Content/event-log fetching from remote servers            | ❌ No     |
| Data replication between servers                          | ❌ No     |
| Access control override across servers                    | ❌ No     |

---

## The Anonymous Author Sentinel

Unauthenticated or unknown edits are attributed to a single global sentinel:

```
urn:notx:usr:anon
```

Because namespace is no longer part of URN identity, there is one universal anonymous sentinel across the entire system. It is not resolvable to a user record — it simply means _"the author of this change is unknown."_

The old per-namespace sentinels (`acme:usr:anon`, `notx:usr:anon`, etc.) are replaced by this single global value.

---

## Platform Role

The notx platform (if used) is an optional orchestration layer. It provides:

- **Identity registry** — A directory of known server URNs and their endpoints.
- **Discovery** — Helps nodes bootstrap their routing tables.
- **Federation bridge** — Assists in resolving objects across servers that don't have direct routing table entries for each other.

The platform is **not** a special privileged namespace owner. It is identified by its own server URN, just like any other server:

```
urn:notx:srv:01HZPLATFORM00000000000
```

The platform does not "own" all data. It does not require a reserved namespace. The string `"notx"` in a URN (`urn:notx:...`) is the universal scheme prefix for the notx protocol — it is not a namespace, and it does not belong to any single organization or operator.

A fully self-hosted deployment with no connection to the notx platform is completely valid. It simply manages its own routing table manually or via a self-hosted registry.

---

## What Changed from the Old Model

| Concept                  | Old Model                                    | New Model                                     |
| ------------------------ | -------------------------------------------- | --------------------------------------------- |
| URN format               | `acme:note:018e4f2a-...`                     | `urn:notx:note:01HZX3K8J9...`                 |
| Namespace location       | Encoded in the URN                           | Separate metadata field on the object         |
| Namespace uniqueness     | Required to be globally unique               | Not required — multiple servers can share one |
| Instance identity anchor | Namespace                                    | Server URN (`urn:notx:srv:<id>`)              |
| Cross-server resolution  | Namespace prefix → registry lookup           | `authority` field → routing table lookup      |
| `notx` string meaning    | Reserved namespace for the official platform | Universal URN scheme prefix for all objects   |
| Anonymous author         | `acme:usr:anon` (per-instance)               | `urn:notx:usr:anon` (global)                  |

---

## Quick Reference

**✅ Correct — namespace as metadata, URN is pure identity:**

```json
{
  "id": "urn:notx:note:01HZX3K8J9X2M4P7R8T1Y6ZQ",
  "authority": "urn:notx:srv:01HZSERVER123456789ZZZZZZ",
  "namespace": "acme"
}
```

**❌ Incorrect — namespace encoded in the URN (old model, do not use):**

```json
{
  "urn": "acme:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a"
}
```

**✅ To resolve a cross-server reference:**

```
object.authority → routing_table[authority] → endpoint → fetch
```

**❌ To resolve a cross-server reference (old model, do not use):**

```
urn.namespace → namespace_registry[namespace] → endpoint → fetch
```

---

## Summary

1. **URNs are globally unique and immutable** — `urn:notx:<type>:<id>`. They do not encode namespace or server location.
2. **Namespace is metadata** — a logical grouping label stored on the object, not in its identity.
3. **Authority is the location anchor** — `"authority": "urn:notx:srv:<id>"` tells you which server owns the object.
4. **Multiple servers can share a namespace** — that's fine, because namespace isn't identity.
5. **Federation uses the routing table** — `authority → endpoint`, not `namespace → registry`.
6. **Server URNs are globally unique** — they are the true identity anchors for servers.
7. **The notx platform is optional** — it is one server among many, identified by its server URN, not by owning a special namespace.
