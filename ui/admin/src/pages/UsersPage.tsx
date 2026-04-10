import { useState, useMemo } from "react";
import {
  useQuery,
  useMutation,
  useQueryClient,
  keepPreviousData,
} from "@tanstack/react-query";
import { HugeiconsIcon } from "@hugeicons/react";
import {
  UserGroupIcon,
  Refresh01Icon,
  AlertCircleIcon,
  Delete01Icon,
  PencilEdit01Icon,
  UserAdd01Icon,
} from "@hugeicons/core-free-icons";
import { fetchUsers, createUser, updateUser, deleteUser } from "../api/client";
import type { User } from "../api/types";

// ─── Helpers ──────────────────────────────────────────────────────────────────

function fmtDate(iso: string) {
  return new Date(iso).toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
}

function fmtDateTime(iso: string) {
  return new Date(iso).toLocaleString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function makeUserUrn() {
  const hex = () =>
    Math.floor(Math.random() * 0x10000)
      .toString(16)
      .padStart(4, "0");
  const uuid = `${hex()}${hex()}-${hex()}-4${hex().slice(1)}-${(
    (Math.floor(Math.random() * 4) + 8).toString(16) + hex().slice(1)
  ).slice(0, 4)}-${hex()}${hex()}${hex()}`;
  return `notx:usr:${uuid}`;
}

/** Show only the last 8 hex chars of the UUID portion of a URN. */
function shortUrn(urn: string) {
  const parts = urn.split(":");
  const uuid = parts[parts.length - 1] ?? urn;
  return "…" + uuid.replace(/-/g, "").slice(-8);
}

// ─── Shared sub-components ────────────────────────────────────────────────────

function Modal({
  title,
  onClose,
  children,
}: {
  title: string;
  onClose: () => void;
  children: React.ReactNode;
}) {
  return (
    <div
      className="drawer-overlay"
      style={{ justifyContent: "center", alignItems: "center" }}
      onClick={(e) => e.target === e.currentTarget && onClose()}
    >
      <div
        className="card"
        style={{
          width: 460,
          padding: 24,
          display: "flex",
          flexDirection: "column",
          gap: 20,
          maxHeight: "90vh",
          overflowY: "auto",
        }}
      >
        <div
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
          }}
        >
          <span
            style={{
              fontSize: 14,
              fontWeight: 600,
              color: "var(--text-primary)",
            }}
          >
            {title}
          </span>
          <button className="close-btn" onClick={onClose}>
            <span style={{ fontSize: 14 }}>×</span>
          </button>
        </div>
        {children}
      </div>
    </div>
  );
}

function Field({
  label,
  required,
  children,
}: {
  label: string;
  required?: boolean;
  children: React.ReactNode;
}) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
      <label
        style={{
          fontSize: 12,
          fontWeight: 600,
          color: "var(--text-secondary)",
          textTransform: "uppercase",
          letterSpacing: "0.6px",
        }}
      >
        {label}
        {required && (
          <span style={{ color: "var(--red)", marginLeft: 3 }}>*</span>
        )}
      </label>
      {children}
    </div>
  );
}

function TextInput({
  value,
  onChange,
  placeholder,
  mono,
  type,
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  mono?: boolean;
  type?: string;
}) {
  return (
    <input
      className="search-input"
      type={type ?? "text"}
      style={{
        width: "100%",
        fontFamily: mono ? "var(--font-mono)" : undefined,
        fontSize: mono ? 12 : undefined,
      }}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      placeholder={placeholder}
    />
  );
}

// ─── Create User Modal ────────────────────────────────────────────────────────

function UserCreateModal({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();

  const [urn, setUrn] = useState(makeUserUrn());
  const [displayName, setDisplayName] = useState("");
  const [email, setEmail] = useState("");
  const [error, setError] = useState("");

  const createMut = useMutation({
    mutationFn: createUser,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["users"] });
      onClose();
    },
    onError: (e: unknown) =>
      setError(
        (e as { response?: { data?: { error?: string } }; message?: string })
          ?.response?.data?.error ?? (e as Error).message,
      ),
  });

  function handleSubmit() {
    setError("");
    if (!displayName.trim()) {
      setError("Display name is required.");
      return;
    }
    if (!urn.trim()) {
      setError("URN is required.");
      return;
    }
    createMut.mutate({
      urn,
      display_name: displayName,
      email: email.trim() || undefined,
    });
  }

  return (
    <Modal title="New user" onClose={onClose}>
      <Field label="URN" required>
        <TextInput
          value={urn}
          onChange={setUrn}
          placeholder="notx:usr:…"
          mono
        />
        <span
          style={{ fontSize: 11, color: "var(--text-muted)", marginTop: 3 }}
        >
          Auto-generated — you can customise it before saving.
        </span>
      </Field>
      <Field label="Display name" required>
        <TextInput
          value={displayName}
          onChange={setDisplayName}
          placeholder="Jane Smith"
        />
      </Field>
      <Field label="Email">
        <TextInput
          value={email}
          onChange={setEmail}
          placeholder="jane@example.com"
          type="email"
        />
      </Field>

      {error && (
        <div className="error-banner" style={{ padding: "10px 14px" }}>
          <HugeiconsIcon icon={AlertCircleIcon} size={13} strokeWidth={1.5} />
          {error}
        </div>
      )}

      <div style={{ display: "flex", gap: 10, justifyContent: "flex-end" }}>
        <button
          className="btn btn-ghost"
          onClick={onClose}
          disabled={createMut.isPending}
        >
          Cancel
        </button>
        <button
          className="btn btn-primary"
          onClick={handleSubmit}
          disabled={createMut.isPending}
        >
          {createMut.isPending ? (
            <div className="spinner" style={{ width: 14, height: 14 }} />
          ) : (
            <span style={{ fontSize: 13 }}>✓</span>
          )}
          Create user
        </button>
      </div>
    </Modal>
  );
}

// ─── Confirm Delete Modal ─────────────────────────────────────────────────────

function ConfirmDeleteModal({
  user,
  onConfirm,
  onClose,
  isPending,
}: {
  user: User;
  onConfirm: () => void;
  onClose: () => void;
  isPending: boolean;
}) {
  return (
    <Modal title="Delete user" onClose={onClose}>
      <p
        style={{
          fontSize: 13,
          color: "var(--text-secondary)",
          lineHeight: 1.7,
        }}
      >
        Are you sure you want to soft-delete{" "}
        <strong style={{ color: "var(--text-primary)" }}>
          {user.display_name}
        </strong>
        ? The user will be marked as deleted but can be restored later via the
        API.
      </p>
      <div style={{ display: "flex", gap: 10, justifyContent: "flex-end" }}>
        <button
          className="btn btn-ghost"
          onClick={onClose}
          disabled={isPending}
        >
          Cancel
        </button>
        <button
          className="btn"
          onClick={onConfirm}
          disabled={isPending}
          style={{
            background: "var(--red-dim)",
            color: "var(--red)",
            borderColor: "var(--red)",
          }}
        >
          {isPending ? (
            <div className="spinner" style={{ width: 14, height: 14 }} />
          ) : (
            <HugeiconsIcon icon={Delete01Icon} size={13} strokeWidth={1.5} />
          )}
          Delete
        </button>
      </div>
    </Modal>
  );
}

// ─── User Detail / Edit Side Panel ───────────────────────────────────────────

function UserPanel({
  user,
  onClose,
  onDeleted,
}: {
  user: User;
  onClose: () => void;
  onDeleted: () => void;
}) {
  const qc = useQueryClient();

  const [editing, setEditing] = useState(false);
  const [displayName, setDisplayName] = useState(user.display_name);
  const [email, setEmail] = useState(user.email ?? "");
  const [editError, setEditError] = useState("");
  const [confirmDelete, setConfirmDelete] = useState(false);

  const updateMut = useMutation({
    mutationFn: ({
      u,
      patch,
    }: {
      u: string;
      patch: Parameters<typeof updateUser>[1];
    }) => updateUser(u, patch),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["users"] });
      setEditing(false);
    },
    onError: (e: unknown) =>
      setEditError(
        (e as { response?: { data?: { error?: string } }; message?: string })
          ?.response?.data?.error ?? (e as Error).message,
      ),
  });

  const deleteMut = useMutation({
    mutationFn: (urn: string) => deleteUser(urn),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["users"] });
      setConfirmDelete(false);
      onDeleted();
    },
  });

  function handleSave() {
    setEditError("");
    if (!displayName.trim()) {
      setEditError("Display name is required.");
      return;
    }
    updateMut.mutate({
      u: user.urn,
      patch: {
        display_name: displayName,
        email: email.trim() || undefined,
      },
    });
  }

  function handleCancelEdit() {
    setDisplayName(user.display_name);
    setEmail(user.email ?? "");
    setEditError("");
    setEditing(false);
  }

  return (
    <>
      {/* Side panel */}
      <div
        style={{
          position: "fixed",
          inset: 0,
          zIndex: 40,
          display: "flex",
          justifyContent: "flex-end",
        }}
        onClick={(e) => e.target === e.currentTarget && onClose()}
      >
        {/* dim backdrop */}
        <div
          style={{
            position: "absolute",
            inset: 0,
            background: "rgba(0,0,0,0.45)",
          }}
          onClick={onClose}
        />

        <div
          className="card"
          style={{
            position: "relative",
            zIndex: 1,
            width: 400,
            height: "100%",
            borderRadius: "12px 0 0 12px",
            borderRight: "none",
            display: "flex",
            flexDirection: "column",
            overflow: "hidden",
          }}
        >
          {/* Panel header */}
          <div
            style={{
              padding: "18px 20px",
              borderBottom: "1px solid var(--border)",
              display: "flex",
              alignItems: "center",
              justifyContent: "space-between",
              flexShrink: 0,
            }}
          >
            <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
              <HugeiconsIcon
                icon={UserGroupIcon}
                size={15}
                strokeWidth={1.5}
                style={{ color: "var(--accent)", flexShrink: 0 }}
              />
              <span
                style={{
                  fontSize: 14,
                  fontWeight: 600,
                  color: "var(--text-primary)",
                }}
              >
                {user.display_name}
              </span>
            </div>
            <button className="close-btn" onClick={onClose}>
              <span style={{ fontSize: 14 }}>×</span>
            </button>
          </div>

          {/* Panel body */}
          <div
            style={{
              flex: 1,
              overflowY: "auto",
              padding: "20px",
              display: "flex",
              flexDirection: "column",
              gap: 20,
            }}
          >
            {/* Status badge */}
            <div>
              {user.deleted ? (
                <span className="badge badge-red">
                  <span className="badge-dot" />
                  deleted
                </span>
              ) : (
                <span className="badge badge-green">
                  <span className="badge-dot" />
                  active
                </span>
              )}
            </div>

            {/* Edit form */}
            {editing ? (
              <div
                style={{
                  display: "flex",
                  flexDirection: "column",
                  gap: 16,
                }}
              >
                <Field label="Display name" required>
                  <TextInput
                    value={displayName}
                    onChange={setDisplayName}
                    placeholder="Jane Smith"
                  />
                </Field>
                <Field label="Email">
                  <TextInput
                    value={email}
                    onChange={setEmail}
                    placeholder="jane@example.com"
                    type="email"
                  />
                </Field>

                {editError && (
                  <div
                    className="error-banner"
                    style={{ padding: "10px 14px" }}
                  >
                    <HugeiconsIcon
                      icon={AlertCircleIcon}
                      size={13}
                      strokeWidth={1.5}
                    />
                    {editError}
                  </div>
                )}

                <div style={{ display: "flex", gap: 8 }}>
                  <button
                    className="btn btn-primary"
                    onClick={handleSave}
                    disabled={updateMut.isPending}
                    style={{ flex: 1 }}
                  >
                    {updateMut.isPending ? (
                      <div
                        className="spinner"
                        style={{ width: 14, height: 14 }}
                      />
                    ) : (
                      <span style={{ fontSize: 13 }}>✓</span>
                    )}
                    Save changes
                  </button>
                  <button
                    className="btn btn-ghost"
                    onClick={handleCancelEdit}
                    disabled={updateMut.isPending}
                  >
                    Cancel
                  </button>
                </div>
              </div>
            ) : (
              /* Read-only detail fields */
              <div
                style={{
                  display: "flex",
                  flexDirection: "column",
                  gap: 16,
                }}
              >
                <DetailRow label="Display name" value={user.display_name} />
                <DetailRow
                  label="Email"
                  value={user.email || undefined}
                  empty="—"
                />
                <DetailRow label="URN" value={user.urn} mono />
                <DetailRow
                  label="Created"
                  value={fmtDateTime(user.created_at)}
                />
                <DetailRow
                  label="Updated"
                  value={fmtDateTime(user.updated_at)}
                />
              </div>
            )}
          </div>

          {/* Panel footer — action buttons */}
          {!editing && (
            <div
              style={{
                padding: "14px 20px",
                borderTop: "1px solid var(--border)",
                display: "flex",
                gap: 8,
                flexShrink: 0,
              }}
            >
              <button
                className="btn btn-ghost"
                onClick={() => setEditing(true)}
                style={{ flex: 1 }}
              >
                <HugeiconsIcon
                  icon={PencilEdit01Icon}
                  size={13}
                  strokeWidth={1.5}
                />
                Edit
              </button>
              {!user.deleted && (
                <button
                  className="btn btn-ghost"
                  onClick={() => setConfirmDelete(true)}
                  style={{ color: "var(--red)" }}
                >
                  <HugeiconsIcon
                    icon={Delete01Icon}
                    size={13}
                    strokeWidth={1.5}
                  />
                  Delete
                </button>
              )}
            </div>
          )}
        </div>
      </div>

      {/* Confirm delete modal — rendered on top of the panel */}
      {confirmDelete && (
        <ConfirmDeleteModal
          user={user}
          onConfirm={() => deleteMut.mutate(user.urn)}
          onClose={() => setConfirmDelete(false)}
          isPending={deleteMut.isPending}
        />
      )}
    </>
  );
}

function DetailRow({
  label,
  value,
  mono,
  empty = "—",
}: {
  label: string;
  value?: string;
  mono?: boolean;
  empty?: string;
}) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
      <span
        style={{
          fontSize: 11,
          fontWeight: 600,
          color: "var(--text-muted)",
          textTransform: "uppercase",
          letterSpacing: "0.6px",
        }}
      >
        {label}
      </span>
      <span
        style={{
          fontSize: 13,
          color: value ? "var(--text-primary)" : "var(--text-muted)",
          fontFamily: mono ? "var(--font-mono)" : undefined,
          wordBreak: "break-all",
        }}
      >
        {value ?? empty}
      </span>
    </div>
  );
}

// ─── Page ─────────────────────────────────────────────────────────────────────

const PAGE_SIZE = 25;

export default function UsersPage() {
  const [includeDeleted, setIncludeDeleted] = useState(false);
  const [search, setSearch] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [selected, setSelected] = useState<User | null>(null);

  // We fetch a generous page and do client-side filtering/search.
  // For very large deployments a server-side search endpoint would be preferred.
  const query = useQuery({
    queryKey: ["users", "list", includeDeleted],
    queryFn: () =>
      fetchUsers({ page_size: PAGE_SIZE, include_deleted: includeDeleted }),
    placeholderData: keepPreviousData,
  });

  const allUsers = useMemo<User[]>(
    () => query.data?.users ?? [],
    [query.data?.users],
  );

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return allUsers;
    return allUsers.filter(
      (u) =>
        u.display_name.toLowerCase().includes(q) ||
        (u.email ?? "").toLowerCase().includes(q) ||
        u.urn.toLowerCase().includes(q),
    );
  }, [allUsers, search]);

  // Keep the selected user in sync with fresh query data (e.g. after edit)
  const liveSelected = selected
    ? (allUsers.find((u) => u.urn === selected.urn) ?? selected)
    : null;

  return (
    <div className="page-stack">
      {/* ── Header ───────────────────────────────────────────────────── */}
      <div className="section-header">
        <div>
          <div
            className="section-title"
            style={{ display: "flex", alignItems: "center", gap: 8 }}
          >
            <HugeiconsIcon
              icon={UserGroupIcon}
              size={16}
              strokeWidth={1.5}
              style={{ color: "var(--accent)" }}
            />
            Users
          </div>
          <div className="section-sub">Manage user accounts</div>
        </div>

        <div className="topbar-right">
          <label
            style={{
              display: "flex",
              alignItems: "center",
              gap: 7,
              cursor: "pointer",
              fontSize: 13,
              color: "var(--text-secondary)",
              userSelect: "none",
            }}
          >
            <input
              type="checkbox"
              checked={includeDeleted}
              onChange={(e) => setIncludeDeleted(e.target.checked)}
              style={{ accentColor: "var(--accent)", cursor: "pointer" }}
            />
            Include deleted
          </label>

          <button
            className="btn btn-ghost"
            onClick={() => query.refetch()}
            disabled={query.isLoading}
          >
            <HugeiconsIcon
              icon={Refresh01Icon}
              size={14}
              strokeWidth={1.5}
              className={query.isLoading ? "spin-icon" : ""}
            />
            Refresh
          </button>

          <button
            className="btn btn-primary"
            onClick={() => setShowCreate(true)}
          >
            <HugeiconsIcon icon={UserAdd01Icon} size={14} strokeWidth={1.5} />
            New user
          </button>
        </div>
      </div>

      {/* ── Search bar ───────────────────────────────────────────────── */}
      <div style={{ marginBottom: 12 }}>
        <input
          className="search-input"
          style={{ width: "100%", maxWidth: 400 }}
          placeholder="Filter by name, email or URN…"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
      </div>

      {/* ── Error banner ─────────────────────────────────────────────── */}
      {query.isError && (
        <div className="error-banner" style={{ marginBottom: 16 }}>
          <HugeiconsIcon icon={AlertCircleIcon} size={14} strokeWidth={1.5} />
          Could not load users. Make sure the server is running.
        </div>
      )}

      {/* ── Table ────────────────────────────────────────────────────── */}
      <div className="card" style={{ padding: 0, overflow: "hidden" }}>
        {query.isLoading ? (
          <div className="loading-center">
            <div className="spinner" />
            Loading users…
          </div>
        ) : filtered.length === 0 ? (
          <div className="empty-state">
            <HugeiconsIcon
              icon={UserGroupIcon}
              size={28}
              strokeWidth={1.5}
              style={{ opacity: 0.3 }}
            />
            <span>
              {search ? "No users match your filter." : "No users found."}
            </span>
            {!search && (
              <span style={{ fontSize: 12 }}>
                Create your first user to get started.
              </span>
            )}
          </div>
        ) : (
          <table className="data-table">
            <thead>
              <tr>
                <th style={{ paddingLeft: 20 }}>Display name</th>
                <th>URN</th>
                <th>Email</th>
                <th>Status</th>
                <th>Created</th>
                <th style={{ width: 72 }} />
              </tr>
            </thead>
            <tbody>
              {filtered.map((u) => (
                <tr
                  key={u.urn}
                  style={{
                    cursor: "pointer",
                    opacity: u.deleted ? 0.6 : 1,
                  }}
                  onClick={() => setSelected(u)}
                  title="Click to view / edit"
                >
                  {/* Display name */}
                  <td className="name-cell" style={{ paddingLeft: 20 }}>
                    <span
                      style={{
                        display: "flex",
                        alignItems: "center",
                        gap: 7,
                      }}
                    >
                      <HugeiconsIcon
                        icon={UserGroupIcon}
                        size={12}
                        strokeWidth={1.5}
                        style={{
                          color: "var(--accent)",
                          flexShrink: 0,
                        }}
                      />
                      {u.display_name}
                    </span>
                  </td>

                  {/* URN — truncated */}
                  <td className="urn-cell" title={u.urn}>
                    {shortUrn(u.urn)}
                  </td>

                  {/* Email */}
                  <td
                    style={{
                      maxWidth: 200,
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                      color: "var(--text-muted)",
                      fontSize: 12,
                    }}
                  >
                    {u.email ?? <span style={{ opacity: 0.4 }}>—</span>}
                  </td>

                  {/* Status badge */}
                  <td>
                    {u.deleted ? (
                      <span className="badge badge-red">
                        <span className="badge-dot" />
                        deleted
                      </span>
                    ) : (
                      <span className="badge badge-green">
                        <span className="badge-dot" />
                        active
                      </span>
                    )}
                  </td>

                  {/* Created date */}
                  <td style={{ whiteSpace: "nowrap" }}>
                    {fmtDate(u.created_at)}
                  </td>

                  {/* Action buttons — stop row click from firing */}
                  <td onClick={(e) => e.stopPropagation()}>
                    <div style={{ display: "flex", gap: 4 }}>
                      <button
                        className="btn btn-ghost"
                        style={{ padding: "4px 8px" }}
                        title="Edit"
                        onClick={() => setSelected(u)}
                      >
                        <HugeiconsIcon
                          icon={PencilEdit01Icon}
                          size={12}
                          strokeWidth={1.5}
                        />
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {/* Results count */}
      {!query.isLoading && filtered.length > 0 && (
        <div className="pagination">
          <span className="last-updated">
            {filtered.length} user{filtered.length !== 1 ? "s" : ""}
            {search ? " matching filter" : ""}
          </span>
        </div>
      )}

      {/* ── Modals & panels ──────────────────────────────────────────── */}
      {showCreate && <UserCreateModal onClose={() => setShowCreate(false)} />}

      {liveSelected && (
        <UserPanel
          key={liveSelected.urn}
          user={liveSelected}
          onClose={() => setSelected(null)}
          onDeleted={() => setSelected(null)}
        />
      )}
    </div>
  );
}
