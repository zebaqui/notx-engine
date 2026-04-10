import { useState, useEffect, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { HugeiconsIcon } from "@hugeicons/react";
import {
  Search01Icon,
  Refresh01Icon,
  AlertCircleIcon,
  Note01Icon,
  LockIcon,
} from "@hugeicons/core-free-icons";
import { fetchNotes, searchNotes } from "../api/client";
import type { NoteHeader } from "../api/types";
import NoteDrawer from "../components/NoteDrawer";

function fmtDate(iso: string) {
  const d = new Date(iso);
  return d.toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
}

function useDebounced(value: string, delay: number): string {
  const [debounced, setDebounced] = useState(value);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    if (timerRef.current) clearTimeout(timerRef.current);
    timerRef.current = setTimeout(() => setDebounced(value), delay);
    return () => {
      if (timerRef.current) clearTimeout(timerRef.current);
    };
  }, [value, delay]);

  return debounced;
}

const PAGE_SIZE = 20;

export default function NotesPage() {
  const [searchRaw, setSearchRaw] = useState("");
  const [pageToken, setPageToken] = useState("");
  const [tokenHistory, setTokenHistory] = useState<string[]>([""]);
  const [pageIndex, setPageIndex] = useState(0);
  const [includeDeleted, setIncludeDeleted] = useState(false);
  const [selectedNote, setSelectedNote] = useState<NoteHeader | null>(null);

  const searchQuery = useDebounced(searchRaw, 350);
  const isSearching = searchQuery.trim().length > 0;

  // ── List query ────────────────────────────────────────────────────────────
  const listQuery = useQuery({
    queryKey: ["notes", "list", pageToken, includeDeleted],
    queryFn: () =>
      fetchNotes({
        page_size: PAGE_SIZE,
        page_token: pageToken,
        include_deleted: includeDeleted,
      }),
    enabled: !isSearching,
    placeholderData: (prev: any) => prev,
  });

  // ── Search query ──────────────────────────────────────────────────────────
  const searchResultQuery = useQuery({
    queryKey: ["notes", "search", searchQuery],
    queryFn: () => searchNotes({ q: searchQuery, page_size: PAGE_SIZE }),
    enabled: isSearching,
    placeholderData: (prev: any) => prev,
  });

  const isLoading = isSearching
    ? searchResultQuery.isLoading
    : listQuery.isLoading;
  const isError = isSearching ? searchResultQuery.isError : listQuery.isError;

  const notes: NoteHeader[] = isSearching
    ? ((searchResultQuery.data as any)?.results ?? []).map((r: any) => r.note)
    : ((listQuery.data as any)?.notes ?? []);

  const nextPageToken: string = isSearching
    ? ((searchResultQuery.data as any)?.next_page_token ?? "")
    : ((listQuery.data as any)?.next_page_token ?? "");

  const hasNextPage = nextPageToken !== "";
  const hasPrevPage = pageIndex > 0;

  // ── Pagination helpers ────────────────────────────────────────────────────
  function goNext() {
    const newHistory = [...tokenHistory];
    if (newHistory.length <= pageIndex + 1) newHistory.push(nextPageToken);
    else newHistory[pageIndex + 1] = nextPageToken;
    setTokenHistory(newHistory);
    setPageToken(nextPageToken);
    setPageIndex((i) => i + 1);
  }

  function goPrev() {
    const prevIdx = pageIndex - 1;
    setPageToken(tokenHistory[prevIdx] ?? "");
    setPageIndex(prevIdx);
  }

  function resetPagination() {
    setPageToken("");
    setPageIndex(0);
    setTokenHistory([""]);
  }

  function handleSearchChange(e: React.ChangeEvent<HTMLInputElement>) {
    setSearchRaw(e.target.value);
    resetPagination();
  }

  function clearSearch() {
    setSearchRaw("");
    resetPagination();
  }

  function handleRefresh() {
    if (isSearching) searchResultQuery.refetch();
    else listQuery.refetch();
  }

  return (
    <div className="page-stack">
      {/* ── Header ────────────────────────────────────────────────────────── */}
      <div className="section-header">
        <div>
          <div className="section-title">Notes</div>
          <div className="section-sub">
            {isSearching
              ? `Search results for "${searchQuery}"`
              : "All notes stored in the server"}
          </div>
        </div>
        <div className="topbar-right">
          <button
            className="btn btn-ghost"
            onClick={handleRefresh}
            disabled={isLoading}
          >
            <HugeiconsIcon
              icon={Refresh01Icon}
              size={13}
              strokeWidth={1.5}
              className={isLoading ? "spin-icon" : ""}
            />
            Refresh
          </button>
        </div>
      </div>

      {/* ── Toolbar ───────────────────────────────────────────────────────── */}
      <div className="toolbar">
        <div
          style={{
            position: "relative",
            display: "flex",
            alignItems: "center",
          }}
        >
          <HugeiconsIcon
            icon={Search01Icon}
            size={13}
            strokeWidth={1.5}
            style={{
              position: "absolute",
              left: 10,
              color: "var(--text-muted)",
              pointerEvents: "none",
            }}
          />
          <input
            className="search-input"
            style={{ paddingLeft: 30 }}
            type="text"
            placeholder="Full-text search notes…"
            value={searchRaw}
            onChange={handleSearchChange}
          />
          {searchRaw && (
            <button
              onClick={clearSearch}
              style={{
                position: "absolute",
                right: 8,
                background: "none",
                border: "none",
                cursor: "pointer",
                color: "var(--text-muted)",
                display: "flex",
                alignItems: "center",
                padding: 2,
              }}
            >
              <span style={{ fontSize: 14, lineHeight: 1 }}>×</span>
            </button>
          )}
        </div>

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
            onChange={(e) => {
              setIncludeDeleted(e.target.checked);
              resetPagination();
            }}
            style={{ accentColor: "var(--accent)", cursor: "pointer" }}
          />
          Include deleted
        </label>
      </div>

      {/* ── Error ─────────────────────────────────────────────────────────── */}
      {isError && (
        <div className="error-banner">
          <HugeiconsIcon icon={AlertCircleIcon} size={14} strokeWidth={1.5} />
          {isSearching
            ? "Search failed. Make sure the server is running."
            : "Could not load notes. Make sure the server is running on :4060."}
        </div>
      )}

      {/* ── Table ─────────────────────────────────────────────────────────── */}
      <div className="card" style={{ padding: 0, overflow: "hidden" }}>
        {isLoading ? (
          <div className="loading-center">
            <div className="spinner" />
            {isSearching ? "Searching…" : "Loading notes…"}
          </div>
        ) : notes.length === 0 ? (
          <div className="empty-state">
            <HugeiconsIcon
              icon={Note01Icon}
              size={26}
              strokeWidth={1.5}
              style={{ opacity: 0.3 }}
            />
            <span>{isSearching ? "No results found." : "No notes found."}</span>
            {!includeDeleted && (
              <span style={{ fontSize: 12 }}>
                Try enabling "Include deleted" to see soft-deleted notes.
              </span>
            )}
          </div>
        ) : (
          <table className="data-table">
            <thead>
              <tr>
                <th style={{ paddingLeft: 20 }}>Name</th>
                <th>URN</th>
                <th>Type</th>
                <th>Status</th>
                <th>Created</th>
                <th>Updated</th>
              </tr>
            </thead>
            <tbody>
              {notes.map((note) => (
                <NoteRow
                  key={note.urn}
                  note={note}
                  onClick={() => setSelectedNote(note)}
                />
              ))}
            </tbody>
          </table>
        )}
      </div>

      {/* ── Pagination ────────────────────────────────────────────────────── */}
      {!isSearching && !isLoading && notes.length > 0 && (
        <div className="pagination">
          <span className="last-updated">Page {pageIndex + 1}</span>
          <button
            className="btn btn-ghost"
            onClick={goPrev}
            disabled={!hasPrevPage}
            style={{ padding: "6px 10px" }}
          >
            <span style={{ fontSize: 15, lineHeight: 1 }}>‹</span>
            Prev
          </button>
          <button
            className="btn btn-ghost"
            onClick={goNext}
            disabled={!hasNextPage}
            style={{ padding: "6px 10px" }}
          >
            Next
            <span style={{ fontSize: 15, lineHeight: 1 }}>›</span>
          </button>
        </div>
      )}

      {/* ── Detail drawer ─────────────────────────────────────────────────── */}
      {selectedNote && (
        <NoteDrawer note={selectedNote} onClose={() => setSelectedNote(null)} />
      )}
    </div>
  );
}

// ─── Row ──────────────────────────────────────────────────────────────────────

function NoteRow({ note, onClick }: { note: NoteHeader; onClick: () => void }) {
  return (
    <tr
      onClick={onClick}
      style={{ cursor: "pointer" }}
      title="Click to view details"
    >
      <td className="name-cell" style={{ paddingLeft: 20 }}>
        <span style={{ display: "flex", alignItems: "center", gap: 7 }}>
          {note.note_type === "secure" ? (
            <HugeiconsIcon
              icon={LockIcon}
              size={12}
              strokeWidth={1.5}
              style={{ color: "var(--yellow)", flexShrink: 0 }}
            />
          ) : (
            <HugeiconsIcon
              icon={Note01Icon}
              size={12}
              strokeWidth={1.5}
              style={{ color: "var(--accent)", flexShrink: 0 }}
            />
          )}
          {note.name || (
            <span style={{ color: "var(--text-muted)", fontStyle: "italic" }}>
              (untitled)
            </span>
          )}
        </span>
      </td>
      <td className="urn-cell">{note.urn}</td>
      <td>
        <span
          className={`badge ${
            note.note_type === "secure" ? "badge-yellow" : "badge-blue"
          }`}
        >
          {note.note_type}
        </span>
      </td>
      <td>
        {note.deleted ? (
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
      <td style={{ whiteSpace: "nowrap" }}>{fmtDate(note.created_at)}</td>
      <td style={{ whiteSpace: "nowrap" }}>{fmtDate(note.updated_at)}</td>
    </tr>
  );
}
