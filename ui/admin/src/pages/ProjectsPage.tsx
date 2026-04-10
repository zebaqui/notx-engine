import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { HugeiconsIcon } from "@hugeicons/react";
import {
  FolderOpenIcon,
  Folder01Icon,
  Refresh01Icon,
  AlertCircleIcon,
  PlusSignIcon,
  Delete01Icon,
  PencilIcon,
  Tick01Icon,
  FolderAddIcon,
  ArrowLeft01Icon,
} from "@hugeicons/core-free-icons";
import {
  fetchProjects,
  fetchFolders,
  createProject,
  updateProject,
  deleteProject,
  createFolder,
  updateFolder,
  deleteFolder,
} from "../api/client";
import type { Project, Folder as FolderType } from "../api/types";

// ─── Helpers ──────────────────────────────────────────────────────────────────

function fmtDate(iso: string) {
  return new Date(iso).toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
}

function makeUrn(type: "proj" | "folder") {
  // Generate a simple UUID v4
  const hex = () =>
    Math.floor(Math.random() * 0x10000)
      .toString(16)
      .padStart(4, "0");
  const uuid = `${hex()}${hex()}-${hex()}-4${hex().slice(1)}-${(
    (Math.floor(Math.random() * 4) + 8).toString(16) + hex().slice(1)
  ).slice(0, 4)}-${hex()}${hex()}${hex()}`;
  return `notx:${type}:${uuid}`;
}

// ─── Modals ───────────────────────────────────────────────────────────────────

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
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  mono?: boolean;
}) {
  return (
    <input
      className="search-input"
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

// ─── Create / Edit Project Modal ──────────────────────────────────────────────

function ProjectModal({
  initial,
  onClose,
}: {
  initial?: Project;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const isEdit = Boolean(initial);

  const [urn, setUrn] = useState(initial?.urn ?? makeUrn("proj"));
  const [name, setName] = useState(initial?.name ?? "");
  const [description, setDescription] = useState(initial?.description ?? "");
  const [error, setError] = useState("");

  const createMut = useMutation({
    mutationFn: createProject,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["projects"] });
      qc.invalidateQueries({ queryKey: ["metrics"] });
      onClose();
    },
    onError: (e: any) => setError(e?.response?.data?.error ?? e.message),
  });

  const updateMut = useMutation({
    mutationFn: ({
      u,
      patch,
    }: {
      u: string;
      patch: Parameters<typeof updateProject>[1];
    }) => updateProject(u, patch),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["projects"] });
      onClose();
    },
    onError: (e: any) => setError(e?.response?.data?.error ?? e.message),
  });

  const isPending = createMut.isPending || updateMut.isPending;

  function handleSubmit() {
    setError("");
    if (!name.trim()) {
      setError("Name is required.");
      return;
    }
    if (!urn.trim()) {
      setError("URN is required.");
      return;
    }

    if (isEdit && initial) {
      updateMut.mutate({ u: initial.urn, patch: { name, description } });
    } else {
      createMut.mutate({ urn, name, description });
    }
  }

  return (
    <Modal title={isEdit ? "Edit project" : "New project"} onClose={onClose}>
      {!isEdit && (
        <Field label="URN" required>
          <TextInput
            value={urn}
            onChange={setUrn}
            placeholder="notx:proj:…"
            mono
          />
          <span
            style={{ fontSize: 11, color: "var(--text-muted)", marginTop: 3 }}
          >
            Auto-generated — you can customise it before saving.
          </span>
        </Field>
      )}
      <Field label="Name" required>
        <TextInput value={name} onChange={setName} placeholder="My project" />
      </Field>
      <Field label="Description">
        <TextInput
          value={description}
          onChange={setDescription}
          placeholder="Optional description"
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
          disabled={isPending}
        >
          Cancel
        </button>
        <button
          className="btn btn-primary"
          onClick={handleSubmit}
          disabled={isPending}
        >
          {isPending ? (
            <div className="spinner" style={{ width: 14, height: 14 }} />
          ) : (
            <HugeiconsIcon icon={Tick01Icon} size={14} strokeWidth={1.5} />
          )}
          {isEdit ? "Save changes" : "Create project"}
        </button>
      </div>
    </Modal>
  );
}

// ─── Create / Edit Folder Modal ───────────────────────────────────────────────

function FolderModal({
  projects,
  initial,
  defaultProjectUrn,
  onClose,
}: {
  projects: Project[];
  initial?: FolderType;
  defaultProjectUrn?: string;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const isEdit = Boolean(initial);

  const [urn, setUrn] = useState(initial?.urn ?? makeUrn("folder"));
  const [projectUrn, setProjectUrn] = useState(
    initial?.project_urn ?? defaultProjectUrn ?? projects[0]?.urn ?? "",
  );
  const [name, setName] = useState(initial?.name ?? "");
  const [description, setDescription] = useState(initial?.description ?? "");
  const [error, setError] = useState("");

  const createMut = useMutation({
    mutationFn: createFolder,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["folders"] });
      qc.invalidateQueries({ queryKey: ["metrics"] });
      onClose();
    },
    onError: (e: any) => setError(e?.response?.data?.error ?? e.message),
  });

  const updateMut = useMutation({
    mutationFn: ({
      u,
      patch,
    }: {
      u: string;
      patch: Parameters<typeof updateFolder>[1];
    }) => updateFolder(u, patch),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["folders"] });
      onClose();
    },
    onError: (e: any) => setError(e?.response?.data?.error ?? e.message),
  });

  const isPending = createMut.isPending || updateMut.isPending;

  function handleSubmit() {
    setError("");
    if (!name.trim()) {
      setError("Name is required.");
      return;
    }
    if (!urn.trim()) {
      setError("URN is required.");
      return;
    }
    if (!projectUrn.trim()) {
      setError("A project must be selected.");
      return;
    }

    if (isEdit && initial) {
      updateMut.mutate({ u: initial.urn, patch: { name, description } });
    } else {
      createMut.mutate({ urn, project_urn: projectUrn, name, description });
    }
  }

  return (
    <Modal title={isEdit ? "Edit folder" : "New folder"} onClose={onClose}>
      {!isEdit && (
        <Field label="URN" required>
          <TextInput
            value={urn}
            onChange={setUrn}
            placeholder="notx:folder:…"
            mono
          />
          <span
            style={{ fontSize: 11, color: "var(--text-muted)", marginTop: 3 }}
          >
            Auto-generated — you can customise it before saving.
          </span>
        </Field>
      )}
      {!isEdit && (
        <Field label="Project" required>
          <select
            value={projectUrn}
            onChange={(e) => setProjectUrn(e.target.value)}
            className="search-input"
            style={{ width: "100%", cursor: "pointer" }}
          >
            {projects
              .filter((p) => !p.deleted)
              .map((p) => (
                <option key={p.urn} value={p.urn}>
                  {p.name}
                </option>
              ))}
          </select>
        </Field>
      )}
      <Field label="Name" required>
        <TextInput value={name} onChange={setName} placeholder="My folder" />
      </Field>
      <Field label="Description">
        <TextInput
          value={description}
          onChange={setDescription}
          placeholder="Optional description"
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
          disabled={isPending}
        >
          Cancel
        </button>
        <button
          className="btn btn-primary"
          onClick={handleSubmit}
          disabled={isPending}
        >
          {isPending ? (
            <div className="spinner" style={{ width: 14, height: 14 }} />
          ) : (
            <HugeiconsIcon icon={Tick01Icon} size={14} strokeWidth={1.5} />
          )}
          {isEdit ? "Save changes" : "Create folder"}
        </button>
      </div>
    </Modal>
  );
}

// ─── Confirm Delete Modal ─────────────────────────────────────────────────────

function ConfirmDeleteModal({
  label,
  name,
  onConfirm,
  onClose,
  isPending,
}: {
  label: string;
  name: string;
  onConfirm: () => void;
  onClose: () => void;
  isPending: boolean;
}) {
  return (
    <Modal title={`Delete ${label}`} onClose={onClose}>
      <p
        style={{
          fontSize: 13,
          color: "var(--text-secondary)",
          lineHeight: 1.7,
        }}
      >
        Are you sure you want to soft-delete{" "}
        <strong style={{ color: "var(--text-primary)" }}>{name}</strong>? It can
        be restored later via the API.
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

// ─── Projects section ─────────────────────────────────────────────────────────

const PAGE_SIZE = 20;

function ProjectsSection({
  onSelectProject,
}: {
  onSelectProject: (p: Project) => void;
}) {
  const qc = useQueryClient();
  const [pageToken, setPageToken] = useState("");
  const [tokenHistory, setTokenHistory] = useState<string[]>([""]);
  const [pageIndex, setPageIndex] = useState(0);
  const [includeDeleted, setIncludeDeleted] = useState(false);
  const [showCreate, setShowCreate] = useState(false);
  const [editing, setEditing] = useState<Project | null>(null);
  const [deleting, setDeleting] = useState<Project | null>(null);

  const query = useQuery({
    queryKey: ["projects", "list", pageToken, includeDeleted],
    queryFn: () =>
      fetchProjects({
        page_size: PAGE_SIZE,
        page_token: pageToken,
        include_deleted: includeDeleted,
      }),
    placeholderData: (prev: any) => prev,
  });

  const deleteMut = useMutation({
    mutationFn: (urn: string) => deleteProject(urn),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["projects"] });
      qc.invalidateQueries({ queryKey: ["metrics"] });
      setDeleting(null);
    },
  });

  const projects: Project[] = query.data?.projects ?? [];
  const nextToken = query.data?.next_page_token ?? "";

  function goNext() {
    const h = [...tokenHistory];
    if (h.length <= pageIndex + 1) h.push(nextToken);
    else h[pageIndex + 1] = nextToken;
    setTokenHistory(h);
    setPageToken(nextToken);
    setPageIndex((i) => i + 1);
  }

  function goPrev() {
    const prev = pageIndex - 1;
    setPageToken(tokenHistory[prev] ?? "");
    setPageIndex(prev);
  }

  return (
    <div>
      {/* Header */}
      <div className="section-header">
        <div>
          <div
            className="section-title"
            style={{ display: "flex", alignItems: "center", gap: 8 }}
          >
            <HugeiconsIcon
              icon={FolderOpenIcon}
              size={16}
              strokeWidth={1.5}
              style={{ color: "var(--accent)" }}
            />
            Projects
          </div>
          <div className="section-sub">Logical groupings for notes</div>
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
            <HugeiconsIcon icon={PlusSignIcon} size={14} strokeWidth={1.5} />
            New project
          </button>
        </div>
      </div>

      {query.isError && (
        <div className="error-banner" style={{ marginBottom: 16 }}>
          <HugeiconsIcon icon={AlertCircleIcon} size={14} strokeWidth={1.5} />
          Could not load projects. Make sure the server is running.
        </div>
      )}

      {/* Table */}
      <div className="card" style={{ padding: 0, overflow: "hidden" }}>
        {query.isLoading ? (
          <div className="loading-center">
            <div className="spinner" />
            Loading projects…
          </div>
        ) : projects.length === 0 ? (
          <div className="empty-state">
            <HugeiconsIcon
              icon={FolderOpenIcon}
              size={28}
              strokeWidth={1.5}
              style={{ opacity: 0.3 }}
            />
            <span>No projects found.</span>
            <span style={{ fontSize: 12 }}>
              Create your first project to get started.
            </span>
          </div>
        ) : (
          <table className="data-table">
            <thead>
              <tr>
                <th style={{ paddingLeft: 20 }}>Name</th>
                <th>URN</th>
                <th>Description</th>
                <th>Status</th>
                <th>Created</th>
                <th style={{ width: 96 }} />
              </tr>
            </thead>
            <tbody>
              {projects.map((p) => (
                <tr
                  key={p.urn}
                  style={{ cursor: "pointer" }}
                  onClick={() => onSelectProject(p)}
                  title="Click to view folders"
                >
                  <td className="name-cell" style={{ paddingLeft: 20 }}>
                    <span
                      style={{ display: "flex", alignItems: "center", gap: 7 }}
                    >
                      <HugeiconsIcon
                        icon={FolderOpenIcon}
                        size={12}
                        strokeWidth={1.5}
                        style={{ color: "var(--accent)", flexShrink: 0 }}
                      />
                      {p.name}
                    </span>
                  </td>
                  <td className="urn-cell">{p.urn}</td>
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
                    {p.description || <span style={{ opacity: 0.4 }}>—</span>}
                  </td>
                  <td>
                    {p.deleted ? (
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
                  <td style={{ whiteSpace: "nowrap" }}>
                    {fmtDate(p.created_at)}
                  </td>
                  <td onClick={(e) => e.stopPropagation()}>
                    <div style={{ display: "flex", gap: 4 }}>
                      <button
                        className="btn btn-ghost"
                        style={{ padding: "4px 8px" }}
                        title="Edit"
                        onClick={() => setEditing(p)}
                      >
                        <HugeiconsIcon
                          icon={PencilIcon}
                          size={12}
                          strokeWidth={1.5}
                        />
                      </button>
                      {!p.deleted && (
                        <button
                          className="btn btn-ghost"
                          style={{ padding: "4px 8px", color: "var(--red)" }}
                          title="Delete"
                          onClick={() => setDeleting(p)}
                        >
                          <HugeiconsIcon
                            icon={Delete01Icon}
                            size={12}
                            strokeWidth={1.5}
                          />
                        </button>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {/* Pagination */}
      {!query.isLoading && projects.length > 0 && (
        <div className="pagination">
          <span className="last-updated">Page {pageIndex + 1}</span>
          <button
            className="btn btn-ghost"
            onClick={goPrev}
            disabled={pageIndex === 0}
            style={{ padding: "6px 10px" }}
          >
            <span style={{ fontSize: 13 }}>‹</span> Prev
          </button>
          <button
            className="btn btn-ghost"
            onClick={goNext}
            disabled={!nextToken}
            style={{ padding: "6px 10px" }}
          >
            Next <span style={{ fontSize: 13 }}>›</span>
          </button>
        </div>
      )}

      {/* Modals */}
      {showCreate && <ProjectModal onClose={() => setShowCreate(false)} />}
      {editing && (
        <ProjectModal initial={editing} onClose={() => setEditing(null)} />
      )}
      {deleting && (
        <ConfirmDeleteModal
          label="project"
          name={deleting.name}
          onConfirm={() => deleteMut.mutate(deleting.urn)}
          onClose={() => setDeleting(null)}
          isPending={deleteMut.isPending}
        />
      )}
    </div>
  );
}

// ─── Folders section ──────────────────────────────────────────────────────────

function FoldersSection({
  projects,
  selectedProject,
  onBack,
}: {
  projects: Project[];
  selectedProject: Project;
  onBack: () => void;
}) {
  const qc = useQueryClient();
  const [includeDeleted, setIncludeDeleted] = useState(false);
  const [pageToken, setPageToken] = useState("");
  const [tokenHistory, setTokenHistory] = useState<string[]>([""]);
  const [pageIndex, setPageIndex] = useState(0);
  const [showCreate, setShowCreate] = useState(false);
  const [editing, setEditing] = useState<FolderType | null>(null);
  const [deleting, setDeleting] = useState<FolderType | null>(null);

  const query = useQuery({
    queryKey: [
      "folders",
      "list",
      selectedProject.urn,
      pageToken,
      includeDeleted,
    ],
    queryFn: () =>
      fetchFolders({
        project_urn: selectedProject.urn,
        page_size: PAGE_SIZE,
        page_token: pageToken,
        include_deleted: includeDeleted,
      }),
    placeholderData: (prev: any) => prev,
  });

  const deleteMut = useMutation({
    mutationFn: (urn: string) => deleteFolder(urn),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["folders"] });
      qc.invalidateQueries({ queryKey: ["metrics"] });
      setDeleting(null);
    },
  });

  const folders: FolderType[] = query.data?.folders ?? [];
  const nextToken = query.data?.next_page_token ?? "";

  function goNext() {
    const h = [...tokenHistory];
    if (h.length <= pageIndex + 1) h.push(nextToken);
    else h[pageIndex + 1] = nextToken;
    setTokenHistory(h);
    setPageToken(nextToken);
    setPageIndex((i) => i + 1);
  }

  function goPrev() {
    const prev = pageIndex - 1;
    setPageToken(tokenHistory[prev] ?? "");
    setPageIndex(prev);
  }

  return (
    <div>
      {/* Breadcrumb header */}
      <div className="section-header">
        <div>
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: 6,
              fontSize: 13,
              color: "var(--text-secondary)",
              marginBottom: 4,
              cursor: "pointer",
            }}
            onClick={onBack}
          >
            <HugeiconsIcon icon={ArrowLeft01Icon} size={13} strokeWidth={1.5} />
            <span style={{ color: "var(--accent)" }}>Projects</span>
          </div>
          <div
            className="section-title"
            style={{ display: "flex", alignItems: "center", gap: 8 }}
          >
            <HugeiconsIcon
              icon={Folder01Icon}
              size={16}
              strokeWidth={1.5}
              style={{ color: "var(--accent)" }}
            />
            {selectedProject.name}
          </div>
          <div className="section-sub">
            Folders inside this project ·{" "}
            <span className="mono" style={{ fontSize: 11 }}>
              {selectedProject.urn}
            </span>
          </div>
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
            <HugeiconsIcon icon={FolderAddIcon} size={14} strokeWidth={1.5} />
            New folder
          </button>
        </div>
      </div>

      {query.isError && (
        <div className="error-banner" style={{ marginBottom: 16 }}>
          <HugeiconsIcon icon={AlertCircleIcon} size={14} strokeWidth={1.5} />
          Could not load folders.
        </div>
      )}

      {/* Table */}
      <div className="card" style={{ padding: 0, overflow: "hidden" }}>
        {query.isLoading ? (
          <div className="loading-center">
            <div className="spinner" />
            Loading folders…
          </div>
        ) : folders.length === 0 ? (
          <div className="empty-state">
            <HugeiconsIcon
              icon={Folder01Icon}
              size={28}
              strokeWidth={1.5}
              style={{ opacity: 0.3 }}
            />
            <span>No folders in this project.</span>
            <span style={{ fontSize: 12 }}>
              Create a folder to organise notes.
            </span>
          </div>
        ) : (
          <table className="data-table">
            <thead>
              <tr>
                <th style={{ paddingLeft: 20 }}>Name</th>
                <th>URN</th>
                <th>Description</th>
                <th>Status</th>
                <th>Created</th>
                <th style={{ width: 96 }} />
              </tr>
            </thead>
            <tbody>
              {folders.map((f) => (
                <tr key={f.urn}>
                  <td className="name-cell" style={{ paddingLeft: 20 }}>
                    <span
                      style={{ display: "flex", alignItems: "center", gap: 7 }}
                    >
                      <HugeiconsIcon
                        icon={Folder01Icon}
                        size={12}
                        strokeWidth={1.5}
                        style={{ color: "var(--accent)", flexShrink: 0 }}
                      />
                      {f.name}
                    </span>
                  </td>
                  <td className="urn-cell">{f.urn}</td>
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
                    {f.description || <span style={{ opacity: 0.4 }}>—</span>}
                  </td>
                  <td>
                    {f.deleted ? (
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
                  <td style={{ whiteSpace: "nowrap" }}>
                    {fmtDate(f.created_at)}
                  </td>
                  <td>
                    <div style={{ display: "flex", gap: 4 }}>
                      <button
                        className="btn btn-ghost"
                        style={{ padding: "4px 8px" }}
                        title="Edit"
                        onClick={() => setEditing(f)}
                      >
                        <HugeiconsIcon
                          icon={PencilIcon}
                          size={12}
                          strokeWidth={1.5}
                        />
                      </button>
                      {!f.deleted && (
                        <button
                          className="btn btn-ghost"
                          style={{ padding: "4px 8px", color: "var(--red)" }}
                          title="Delete"
                          onClick={() => setDeleting(f)}
                        >
                          <HugeiconsIcon
                            icon={Delete01Icon}
                            size={12}
                            strokeWidth={1.5}
                          />
                        </button>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {/* Pagination */}
      {!query.isLoading && folders.length > 0 && (
        <div className="pagination">
          <span className="last-updated">Page {pageIndex + 1}</span>
          <button
            className="btn btn-ghost"
            onClick={goPrev}
            disabled={pageIndex === 0}
            style={{ padding: "6px 10px" }}
          >
            <span style={{ fontSize: 13 }}>‹</span> Prev
          </button>
          <button
            className="btn btn-ghost"
            onClick={goNext}
            disabled={!nextToken}
            style={{ padding: "6px 10px" }}
          >
            Next <span style={{ fontSize: 13 }}>›</span>
          </button>
        </div>
      )}

      {/* Modals */}
      {showCreate && (
        <FolderModal
          projects={projects}
          defaultProjectUrn={selectedProject.urn}
          onClose={() => setShowCreate(false)}
        />
      )}
      {editing && (
        <FolderModal
          projects={projects}
          initial={editing}
          onClose={() => setEditing(null)}
        />
      )}
      {deleting && (
        <ConfirmDeleteModal
          label="folder"
          name={deleting.name}
          onConfirm={() => deleteMut.mutate(deleting.urn)}
          onClose={() => setDeleting(null)}
          isPending={deleteMut.isPending}
        />
      )}
    </div>
  );
}

// ─── Page ─────────────────────────────────────────────────────────────────────

export default function ProjectsPage() {
  const [selectedProject, setSelectedProject] = useState<Project | null>(null);

  // Keep a live projects list available for the folder modal's project selector
  const projectsQuery = useQuery({
    queryKey: ["projects", "list", "", false],
    queryFn: () => fetchProjects({ page_size: 200 }),
  });
  const allProjects: Project[] = projectsQuery.data?.projects ?? [];

  return (
    <div className="page-stack">
      {selectedProject ? (
        <FoldersSection
          projects={allProjects}
          selectedProject={selectedProject}
          onBack={() => setSelectedProject(null)}
        />
      ) : (
        <ProjectsSection onSelectProject={setSelectedProject} />
      )}
    </div>
  );
}
