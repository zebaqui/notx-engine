import { useState, useEffect } from "react";
import { useQuery } from "@tanstack/react-query";
import { HugeiconsIcon } from "@hugeicons/react";
import {
  Link01Icon,
  Refresh01Icon,
  AlertCircleIcon,
} from "@hugeicons/core-free-icons";
import {
  fetchAnchors,
  fetchExternalLinks,
  fetchRecentBacklinks,
} from "../api/client";
import type {
  AnchorRecord,
  BacklinkRecord,
  ExternalLinkRecord,
} from "../api/types";

// ─── Helpers ──────────────────────────────────────────────────────────────────

function shortUrn(urn: string) {
  const parts = urn.split(":");
  return parts.length >= 3 ? "…" + parts[parts.length - 1].slice(-8) : urn;
}

function fmtDate(iso?: string): string {
  if (!iso) return "—";
  return new Date(iso).toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
}

function truncate(s: string, max: number): string {
  return s.length > max ? s.slice(0, max) + "…" : s;
}

const ANCHOR_STATUS_BADGE: Record<AnchorRecord["status"], string> = {
  ok: "badge badge-green",
  broken: "badge badge-red",
  deprecated: "badge",
};

type Tab = "anchors" | "backlinks" | "external";

// ─── Page ─────────────────────────────────────────────────────────────────────

export default function LinksPage() {
  const [activeTab, setActiveTab] = useState<Tab>("anchors");

  return (
    <div className="page-stack">
      {/* ── Header ───────────────────────────────────────────────────────── */}
      <div className="section-header">
        <div>
          <div className="section-title">Link Inspector</div>
          <div className="section-sub">
            Browse anchors, backlinks, and external links
          </div>
        </div>
      </div>

      {/* ── Tab bar ──────────────────────────────────────────────────────── */}
      <div className="toolbar" style={{ gap: 4 }}>
        <TabButton
          label="Anchors"
          tab="anchors"
          active={activeTab}
          onClick={setActiveTab}
        />
        <TabButton
          label="Backlinks"
          tab="backlinks"
          active={activeTab}
          onClick={setActiveTab}
        />
        <TabButton
          label="External Links"
          tab="external"
          active={activeTab}
          onClick={setActiveTab}
        />
      </div>

      {/* ── Tab content ──────────────────────────────────────────────────── */}
      {activeTab === "anchors" && <AnchorsTab />}
      {activeTab === "backlinks" && <BacklinksTab />}
      {activeTab === "external" && <ExternalLinksTab />}
    </div>
  );
}

// ─── Anchors tab ─────────────────────────────────────────────────────────────

function AnchorsTab() {
  const [noteUrn, setNoteUrn] = useState("");
  const [loadedUrn, setLoadedUrn] = useState("");

  const query = useQuery({
    queryKey: ["anchors", loadedUrn],
    queryFn: () => fetchAnchors(loadedUrn),
    enabled: loadedUrn.trim() !== "",
  });

  const anchors: AnchorRecord[] = query.data?.anchors ?? [];

  function handleLoad() {
    const trimmed = noteUrn.trim();
    if (trimmed) setLoadedUrn(trimmed);
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
      {/* Input row */}
      <div className="toolbar" style={{ gap: 8 }}>
        <input
          className="search-input"
          style={{ flex: 1 }}
          placeholder="Note URN, e.g. notx:note:xxxxxxxx-…"
          value={noteUrn}
          onChange={(e) => setNoteUrn(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && handleLoad()}
        />
        <button
          className="btn btn-primary"
          onClick={handleLoad}
          disabled={!noteUrn.trim() || query.isFetching}
        >
          Load
        </button>
      </div>

      {/* States */}
      {!loadedUrn && (
        <div className="empty-state">
          <HugeiconsIcon
            icon={Link01Icon}
            size={26}
            strokeWidth={1.5}
            style={{ opacity: 0.3, marginBottom: 8 }}
          />
          Enter a note URN to inspect its anchors
        </div>
      )}

      {loadedUrn && query.isLoading && (
        <div className="loading-center">
          <div className="spinner" /> Loading…
        </div>
      )}

      {loadedUrn && query.isError && (
        <div className="error-banner">
          <HugeiconsIcon icon={AlertCircleIcon} size={15} strokeWidth={1.5} />
          {query.error instanceof Error
            ? query.error.message
            : "Failed to load anchors"}
        </div>
      )}

      {loadedUrn &&
        !query.isLoading &&
        !query.isError &&
        anchors.length === 0 && (
          <div className="empty-state">No results found</div>
        )}

      {anchors.length > 0 && (
        <div style={{ overflowX: "auto" }}>
          <table className="data-table">
            <thead>
              <tr>
                <th>Anchor ID</th>
                <th>Line</th>
                <th>Char</th>
                <th>Status</th>
                <th>Preview</th>
                <th>Updated</th>
              </tr>
            </thead>
            <tbody>
              {anchors.map((a) => (
                <tr key={a.anchor_id}>
                  <td
                    className="urn-cell mono"
                    title={a.anchor_id}
                    style={{ fontSize: 11 }}
                  >
                    {shortUrn(a.anchor_id)}
                  </td>
                  <td style={{ fontSize: 12, fontFamily: "var(--font-mono)" }}>
                    {a.line}
                  </td>
                  <td
                    style={{
                      fontSize: 12,
                      color: "var(--text-muted)",
                      fontFamily: "var(--font-mono)",
                    }}
                  >
                    {a.char_start}
                    {a.char_end !== undefined ? `–${a.char_end}` : ""}
                  </td>
                  <td>
                    <span className={ANCHOR_STATUS_BADGE[a.status] ?? "badge"}>
                      {a.status}
                    </span>
                  </td>
                  <td
                    style={{
                      maxWidth: 260,
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                      fontSize: 12,
                      color: "var(--text-secondary)",
                    }}
                    title={a.preview}
                  >
                    {a.preview ?? "—"}
                  </td>
                  <td style={{ fontSize: 12, color: "var(--text-muted)" }}>
                    {fmtDate(a.updated_at)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

// ─── Backlinks tab ────────────────────────────────────────────────────────────

function BacklinksTab() {
  const [noteUrnFilter, setNoteUrnFilter] = useState("");
  const [labelFilter, setLabelFilter] = useState("");
  // Debounced values actually sent to the API
  const [debouncedNote, setDebouncedNote] = useState("");
  const [debouncedLabel, setDebouncedLabel] = useState("");

  // Debounce: update query params 300ms after user stops typing
  useEffect(() => {
    const t = setTimeout(() => {
      setDebouncedNote(noteUrnFilter.trim());
      setDebouncedLabel(labelFilter.trim());
    }, 300);
    return () => clearTimeout(t);
  }, [noteUrnFilter, labelFilter]);

  const query = useQuery({
    queryKey: ["backlinks-recent", debouncedNote, debouncedLabel],
    queryFn: () =>
      fetchRecentBacklinks({
        note_urn: debouncedNote || undefined,
        label: debouncedLabel || undefined,
        limit: 50,
      }),
  });

  const backlinks: BacklinkRecord[] = query.data?.backlinks ?? [];
  const hasFilters = debouncedNote !== "" || debouncedLabel !== "";

  function clearFilters() {
    setNoteUrnFilter("");
    setLabelFilter("");
    setDebouncedNote("");
    setDebouncedLabel("");
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
      {/* Filter bar */}
      <div className="toolbar" style={{ flexWrap: "wrap", gap: 8 }}>
        <input
          className="search-input"
          style={{ flex: "1 1 260px", minWidth: 200 }}
          placeholder="Filter by note URN (source or target)…"
          value={noteUrnFilter}
          onChange={(e) => setNoteUrnFilter(e.target.value)}
        />
        <input
          className="search-input"
          style={{ flex: "1 1 160px", minWidth: 120 }}
          placeholder="Filter by label…"
          value={labelFilter}
          onChange={(e) => setLabelFilter(e.target.value)}
        />
        {hasFilters && (
          <button className="btn btn-ghost" onClick={clearFilters}>
            <span style={{ fontSize: 14 }}>×</span> Clear
          </button>
        )}
        <button
          className="btn btn-ghost"
          onClick={() => query.refetch()}
          disabled={query.isFetching}
        >
          <HugeiconsIcon
            icon={Refresh01Icon}
            size={13}
            strokeWidth={1.5}
            className={query.isFetching ? "spin-icon" : ""}
          />
          Refresh
        </button>
      </div>

      {/* Count + filter status */}
      {!query.isLoading && (
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <span style={{ fontSize: 12, color: "var(--text-muted)" }}>
            Showing {backlinks.length} link{backlinks.length !== 1 ? "s" : ""}
            {backlinks.length === 50 ? " (latest 50)" : ""}
          </span>
          {hasFilters && (
            <span className="badge badge-blue">
              <span className="badge-dot" />
              filtered
            </span>
          )}
        </div>
      )}

      {/* Loading */}
      {query.isLoading && (
        <div className="loading-center">
          <div className="spinner" /> Loading backlinks…
        </div>
      )}

      {/* Error */}
      {query.isError && (
        <div className="error-banner">
          <HugeiconsIcon icon={AlertCircleIcon} size={15} strokeWidth={1.5} />
          {query.error instanceof Error
            ? query.error.message
            : "Failed to load backlinks"}
        </div>
      )}

      {/* Empty */}
      {!query.isLoading && !query.isError && backlinks.length === 0 && (
        <div className="empty-state">
          {hasFilters
            ? "No backlinks match the current filters"
            : "No backlinks recorded yet"}
        </div>
      )}

      {/* Table */}
      {backlinks.length > 0 && (
        <div style={{ overflowX: "auto" }}>
          <table className="data-table">
            <thead>
              <tr>
                <th>Source</th>
                <th>Target</th>
                <th>Anchor</th>
                <th>Label</th>
                <th>Created</th>
              </tr>
            </thead>
            <tbody>
              {backlinks.map((bl, i) => (
                <tr key={i}>
                  <td className="urn-cell" title={bl.source_urn}>
                    {shortUrn(bl.source_urn)}
                  </td>
                  <td className="urn-cell" title={bl.target_urn}>
                    {shortUrn(bl.target_urn)}
                  </td>
                  <td
                    className="mono"
                    title={bl.target_anchor}
                    style={{
                      fontSize: 11,
                      color: "var(--text-muted)",
                      maxWidth: 140,
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                    }}
                  >
                    {bl.target_anchor}
                  </td>
                  <td style={{ fontSize: 12 }}>{bl.label || "—"}</td>
                  <td style={{ fontSize: 12, color: "var(--text-muted)" }}>
                    {fmtDate(bl.created_at)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

// ─── External links tab ───────────────────────────────────────────────────────

function ExternalLinksTab() {
  const [sourceUrn, setSourceUrn] = useState("");
  const [loadedUrn, setLoadedUrn] = useState("");

  const query = useQuery({
    queryKey: ["external-links", loadedUrn],
    queryFn: () => fetchExternalLinks(loadedUrn),
    enabled: loadedUrn.trim() !== "",
  });

  const links: ExternalLinkRecord[] = query.data?.links ?? [];

  function handleLoad() {
    const trimmed = sourceUrn.trim();
    if (trimmed) setLoadedUrn(trimmed);
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
      {/* Input row */}
      <div className="toolbar" style={{ gap: 8 }}>
        <input
          className="search-input"
          style={{ flex: 1 }}
          placeholder="Source note URN, e.g. notx:note:xxxxxxxx-…"
          value={sourceUrn}
          onChange={(e) => setSourceUrn(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && handleLoad()}
        />
        <button
          className="btn btn-primary"
          onClick={handleLoad}
          disabled={!sourceUrn.trim() || query.isFetching}
        >
          Load
        </button>
      </div>

      {/* States */}
      {!loadedUrn && (
        <div className="empty-state">
          <HugeiconsIcon
            icon={Link01Icon}
            size={26}
            strokeWidth={1.5}
            style={{ opacity: 0.3, marginBottom: 8 }}
          />
          Enter a note URN to inspect its external links
        </div>
      )}

      {loadedUrn && query.isLoading && (
        <div className="loading-center">
          <div className="spinner" /> Loading…
        </div>
      )}

      {loadedUrn && query.isError && (
        <div className="error-banner">
          <HugeiconsIcon icon={AlertCircleIcon} size={15} strokeWidth={1.5} />
          {query.error instanceof Error
            ? query.error.message
            : "Failed to load external links"}
        </div>
      )}

      {loadedUrn &&
        !query.isLoading &&
        !query.isError &&
        links.length === 0 && (
          <div className="empty-state">No results found</div>
        )}

      {links.length > 0 && (
        <div style={{ overflowX: "auto" }}>
          <table className="data-table">
            <thead>
              <tr>
                <th>URI</th>
                <th>Label</th>
                <th>Created</th>
              </tr>
            </thead>
            <tbody>
              {links.map((el, i) => (
                <tr key={i}>
                  <td
                    style={{
                      maxWidth: 360,
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                    }}
                    title={el.uri}
                  >
                    <a
                      href={el.uri}
                      target="_blank"
                      rel="noopener noreferrer"
                      style={{
                        color: "var(--accent)",
                        textDecoration: "none",
                        fontSize: 12,
                        fontFamily: "var(--font-mono)",
                      }}
                    >
                      {truncate(el.uri, 60)}
                    </a>
                  </td>
                  <td style={{ fontSize: 12 }}>{el.label ?? "—"}</td>
                  <td style={{ fontSize: 12, color: "var(--text-muted)" }}>
                    {fmtDate(el.created_at)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

// ─── Sub-components ───────────────────────────────────────────────────────────

function TabButton({
  label,
  tab,
  active,
  onClick,
}: {
  label: string;
  tab: Tab;
  active: Tab;
  onClick: (t: Tab) => void;
}) {
  const isActive = tab === active;
  return (
    <button
      className={isActive ? "btn btn-primary" : "btn btn-ghost"}
      onClick={() => onClick(tab)}
      style={{ minWidth: 110 }}
    >
      {label}
    </button>
  );
}
