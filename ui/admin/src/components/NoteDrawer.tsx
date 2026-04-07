import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  X,
  Lock,
  FileText,
  Clock,
  User,
  Hash,
  Layers,
  Link2,
  Anchor,
  ArrowRight,
  ArrowLeft,
  ExternalLink,
} from "lucide-react";
import {
  fetchNote,
  fetchNoteEvents,
  fetchAnchors,
  fetchBacklinks,
  fetchOutboundLinks,
  fetchExternalLinks,
} from "../api/client";
import type { NoteHeader } from "../api/types";

interface Props {
  note: NoteHeader;
  onClose: () => void;
}

function fmtDate(iso: string) {
  const d = new Date(iso);
  return (
    d.toLocaleDateString(undefined, {
      year: "numeric",
      month: "short",
      day: "numeric",
    }) +
    " " +
    d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" })
  );
}

function shortUrn(urn: string) {
  const parts = urn.split(":");
  if (parts.length >= 3) {
    const id = parts[parts.length - 1];
    return "…" + id.slice(-12);
  }
  return urn;
}

function Row({
  label,
  icon,
  children,
}: {
  label: string;
  icon: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <tr>
      <td>
        <span
          style={{
            display: "flex",
            alignItems: "center",
            gap: 6,
            color: "var(--text-secondary)",
          }}
        >
          {icon}
          {label}
        </span>
      </td>
      <td>{children}</td>
    </tr>
  );
}

// ─── LinkRow ─────────────────────────────────────────────────────────────────

function LinkRow({
  primary,
  anchor,
  label,
  created,
  direction,
}: {
  primary: string;
  anchor: string;
  label?: string;
  created?: string;
  direction: "in" | "out";
}) {
  return (
    <div
      style={{
        background: "var(--bg-elevated)",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius-sm)",
        padding: "6px 10px",
        display: "flex",
        flexDirection: "column",
        gap: 3,
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
        <span
          style={{
            fontSize: 10,
            color: direction === "in" ? "var(--green)" : "var(--accent)",
            flexShrink: 0,
            fontWeight: 700,
          }}
        >
          {direction === "in" ? "←" : "→"}
        </span>
        <span
          className="mono"
          title={primary}
          style={{
            fontSize: 11,
            color: "var(--text-primary)",
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
            flex: 1,
          }}
        >
          {shortUrn(primary)}
        </span>
        {anchor && (
          <span
            className="mono"
            title={anchor}
            style={{
              fontSize: 10,
              color: "var(--accent)",
              background: "var(--bg-primary)",
              border: "1px solid var(--border)",
              borderRadius: 4,
              padding: "1px 5px",
              flexShrink: 0,
              maxWidth: 130,
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
            }}
          >
            #{anchor}
          </span>
        )}
      </div>
      {(label || created) && (
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 8,
            paddingLeft: 16,
          }}
        >
          {label && (
            <span style={{ fontSize: 11, color: "var(--text-muted)" }}>
              {label}
            </span>
          )}
          {created && (
            <span
              style={{
                fontSize: 10,
                color: "var(--text-muted)",
                marginLeft: "auto",
              }}
            >
              {new Date(created).toLocaleDateString(undefined, {
                month: "short",
                day: "numeric",
                year: "numeric",
              })}
            </span>
          )}
        </div>
      )}
    </div>
  );
}

// ─── Main drawer ─────────────────────────────────────────────────────────────

export default function NoteDrawer({ note, onClose }: Props) {
  const isSecure = note.note_type === "secure";
  const [linksTab, setLinksTab] = useState<"inbound" | "outbound" | "external">(
    "inbound",
  );

  // ── Queries ────────────────────────────────────────────────────────────────

  const detail = useQuery({
    queryKey: ["note", note.urn],
    queryFn: () => fetchNote(note.urn),
    enabled: !isSecure,
  });

  const eventsQuery = useQuery({
    queryKey: ["note-events", note.urn],
    queryFn: () => fetchNoteEvents(note.urn),
    enabled: !isSecure,
  });

  const anchorsQuery = useQuery({
    queryKey: ["note-anchors", note.urn],
    queryFn: () => fetchAnchors(note.urn),
  });

  const backlinksQuery = useQuery({
    queryKey: ["note-backlinks", note.urn],
    queryFn: () => fetchBacklinks(note.urn),
  });

  const outboundQuery = useQuery({
    queryKey: ["note-outbound", note.urn],
    queryFn: () => fetchOutboundLinks(note.urn),
  });

  const externalQuery = useQuery({
    queryKey: ["note-external", note.urn],
    queryFn: () => fetchExternalLinks(note.urn),
  });

  // ── Derived data ───────────────────────────────────────────────────────────

  const content = isSecure ? null : (detail.data?.content ?? null);
  const events = eventsQuery.data?.events ?? [];
  const eventCount = eventsQuery.data?.count ?? 0;

  const anchors = anchorsQuery.data?.anchors ?? [];
  const inbound = backlinksQuery.data?.backlinks ?? [];
  const outbound = outboundQuery.data?.links ?? [];
  const external = externalQuery.data?.links ?? [];
  const totalLinks = inbound.length + outbound.length + external.length;

  // ── Render ─────────────────────────────────────────────────────────────────

  return (
    <div
      className="drawer-overlay"
      onClick={(e) => e.target === e.currentTarget && onClose()}
    >
      <div className="drawer">
        {/* ── Header ──────────────────────────────────────────────────── */}
        <div className="drawer-header">
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: 10,
              minWidth: 0,
            }}
          >
            {isSecure ? (
              <Lock
                size={15}
                style={{ color: "var(--yellow)", flexShrink: 0 }}
              />
            ) : (
              <FileText
                size={15}
                style={{ color: "var(--accent)", flexShrink: 0 }}
              />
            )}
            <span
              className="drawer-title"
              style={{
                overflow: "hidden",
                textOverflow: "ellipsis",
                whiteSpace: "nowrap",
              }}
            >
              {note.name || "(untitled)"}
            </span>
          </div>
          <button className="close-btn" onClick={onClose} aria-label="Close">
            <X size={16} />
          </button>
        </div>

        {/* ── Body ────────────────────────────────────────────────────── */}
        <div className="drawer-body">
          {/* Status badges */}
          <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
            <span
              className={`badge ${isSecure ? "badge-yellow" : "badge-blue"}`}
            >
              <span className="badge-dot" />
              {isSecure ? "secure" : "normal"}
            </span>
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
          </div>

          {/* ── Metadata ──────────────────────────────────────────────── */}
          <div className="card" style={{ padding: "14px 16px" }}>
            <div className="card-title">Metadata</div>
            <table className="kv-table">
              <tbody>
                <Row label="URN" icon={<Hash size={12} />}>
                  <span
                    className="mono"
                    style={{ fontSize: 11, wordBreak: "break-all" }}
                  >
                    {note.urn}
                  </span>
                </Row>
                <Row label="Type" icon={<Layers size={12} />}>
                  <span className="mono">{note.note_type}</span>
                </Row>
                {note.project_urn && (
                  <Row label="Project URN" icon={<Hash size={12} />}>
                    <span
                      className="mono"
                      style={{ fontSize: 11, wordBreak: "break-all" }}
                    >
                      {note.project_urn}
                    </span>
                  </Row>
                )}
                {note.folder_urn && (
                  <Row label="Folder URN" icon={<Hash size={12} />}>
                    <span
                      className="mono"
                      style={{ fontSize: 11, wordBreak: "break-all" }}
                    >
                      {note.folder_urn}
                    </span>
                  </Row>
                )}
                <Row label="Created" icon={<Clock size={12} />}>
                  <span className="mono">{fmtDate(note.created_at)}</span>
                </Row>
                <Row label="Updated" icon={<Clock size={12} />}>
                  <span className="mono">{fmtDate(note.updated_at)}</span>
                </Row>
              </tbody>
            </table>
          </div>

          {/* ── Content preview ───────────────────────────────────────── */}
          {!isSecure && (
            <div className="card" style={{ padding: "14px 16px" }}>
              <div className="card-title">Content preview</div>
              {detail.isLoading ? (
                <div className="loading-center" style={{ padding: "20px 0" }}>
                  <div className="spinner" />
                </div>
              ) : detail.isError ? (
                <div className="error-banner">Failed to load content.</div>
              ) : (
                <div className="content-preview">
                  {content && content.trim().length > 0
                    ? content
                    : "(no content)"}
                </div>
              )}
            </div>
          )}

          {/* ── Anchors ───────────────────────────────────────────────── */}
          <div className="card" style={{ padding: "14px 16px" }}>
            <div
              style={{
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
                marginBottom: 10,
              }}
            >
              <div className="card-title" style={{ marginBottom: 0 }}>
                <span style={{ display: "flex", alignItems: "center", gap: 6 }}>
                  <Anchor size={13} style={{ color: "var(--accent)" }} />
                  Anchors
                </span>
              </div>
              {anchors.length > 0 && (
                <span className="badge badge-blue">{anchors.length}</span>
              )}
            </div>

            {anchorsQuery.isLoading ? (
              <div className="loading-center" style={{ padding: "12px 0" }}>
                <div className="spinner" />
              </div>
            ) : anchorsQuery.isError ? (
              <div className="error-banner" style={{ fontSize: 12 }}>
                Failed to load anchors.
              </div>
            ) : anchors.length === 0 ? (
              <div
                style={{
                  fontSize: 12,
                  color: "var(--text-muted)",
                  fontStyle: "italic",
                  padding: "4px 0",
                }}
              >
                No anchors declared in this note.
              </div>
            ) : (
              <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
                {anchors.map((a) => (
                  <div
                    key={a.anchor_id}
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: 8,
                      background: "var(--bg-elevated)",
                      border: "1px solid var(--border)",
                      borderRadius: "var(--radius-sm)",
                      padding: "6px 10px",
                    }}
                  >
                    <span
                      className="mono"
                      style={{
                        fontSize: 11,
                        color: "var(--accent)",
                        flexShrink: 0,
                      }}
                    >
                      #{a.anchor_id}
                    </span>
                    {a.preview && (
                      <span
                        style={{
                          fontSize: 11,
                          color: "var(--text-muted)",
                          overflow: "hidden",
                          textOverflow: "ellipsis",
                          whiteSpace: "nowrap",
                          flex: 1,
                          minWidth: 0,
                        }}
                        title={a.preview}
                      >
                        {a.preview}
                      </span>
                    )}
                    <span
                      className={`badge ${
                        a.status === "ok"
                          ? "badge-green"
                          : a.status === "broken"
                            ? "badge-red"
                            : "badge"
                      }`}
                      style={{ fontSize: 10, flexShrink: 0 }}
                    >
                      {a.status}
                    </span>
                  </div>
                ))}
              </div>
            )}
          </div>

          {/* ── Links ─────────────────────────────────────────────────── */}
          <div className="card" style={{ padding: "14px 16px" }}>
            <div
              style={{
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
                marginBottom: 10,
              }}
            >
              <div className="card-title" style={{ marginBottom: 0 }}>
                <span style={{ display: "flex", alignItems: "center", gap: 6 }}>
                  <Link2 size={13} style={{ color: "var(--accent)" }} />
                  Links
                </span>
              </div>
              {totalLinks > 0 && (
                <span className="badge badge-blue">{totalLinks}</span>
              )}
            </div>

            {/* Tab bar */}
            <div
              style={{
                display: "flex",
                gap: 2,
                borderBottom: "1px solid var(--border)",
                marginBottom: 10,
              }}
            >
              {[
                {
                  key: "inbound" as const,
                  label: "Inbound",
                  icon: <ArrowLeft size={11} />,
                  count: inbound.length,
                },
                {
                  key: "outbound" as const,
                  label: "Outbound",
                  icon: <ArrowRight size={11} />,
                  count: outbound.length,
                },
                {
                  key: "external" as const,
                  label: "External",
                  icon: <ExternalLink size={11} />,
                  count: external.length,
                },
              ].map(({ key, label, icon, count }) => (
                <button
                  key={key}
                  onClick={() => setLinksTab(key)}
                  style={{
                    background: "none",
                    border: "none",
                    cursor: "pointer",
                    padding: "5px 10px",
                    fontSize: 12,
                    color:
                      linksTab === key
                        ? "var(--accent)"
                        : "var(--text-secondary)",
                    borderBottom:
                      linksTab === key
                        ? "2px solid var(--accent)"
                        : "2px solid transparent",
                    marginBottom: -1,
                    display: "flex",
                    alignItems: "center",
                    gap: 5,
                    fontWeight: linksTab === key ? 600 : 400,
                    transition: "color 0.12s",
                  }}
                >
                  {icon}
                  {label}
                  {count > 0 && (
                    <span
                      style={{
                        background: "var(--bg-elevated)",
                        border: "1px solid var(--border)",
                        borderRadius: 9,
                        padding: "0 5px",
                        fontSize: 10,
                        fontWeight: 600,
                        color: "var(--text-muted)",
                        minWidth: 16,
                        textAlign: "center",
                      }}
                    >
                      {count}
                    </span>
                  )}
                </button>
              ))}
            </div>

            {/* Inbound */}
            {linksTab === "inbound" &&
              (backlinksQuery.isLoading ? (
                <div className="loading-center" style={{ padding: "12px 0" }}>
                  <div className="spinner" />
                </div>
              ) : backlinksQuery.isError ? (
                <div className="error-banner" style={{ fontSize: 12 }}>
                  Failed to load backlinks.
                </div>
              ) : inbound.length === 0 ? (
                <div
                  style={{
                    fontSize: 12,
                    color: "var(--text-muted)",
                    fontStyle: "italic",
                    padding: "4px 0",
                  }}
                >
                  No notes link into this note yet.
                </div>
              ) : (
                <div
                  style={{ display: "flex", flexDirection: "column", gap: 4 }}
                >
                  {inbound.map((bl, i) => (
                    <LinkRow
                      key={i}
                      primary={bl.source_urn}
                      anchor={bl.target_anchor}
                      label={bl.label}
                      created={bl.created_at}
                      direction="in"
                    />
                  ))}
                </div>
              ))}

            {/* Outbound */}
            {linksTab === "outbound" &&
              (outboundQuery.isLoading ? (
                <div className="loading-center" style={{ padding: "12px 0" }}>
                  <div className="spinner" />
                </div>
              ) : outboundQuery.isError ? (
                <div className="error-banner" style={{ fontSize: 12 }}>
                  Failed to load outbound links.
                </div>
              ) : outbound.length === 0 ? (
                <div
                  style={{
                    fontSize: 12,
                    color: "var(--text-muted)",
                    fontStyle: "italic",
                    padding: "4px 0",
                  }}
                >
                  This note doesn't link to any other notes yet.
                </div>
              ) : (
                <div
                  style={{ display: "flex", flexDirection: "column", gap: 4 }}
                >
                  {outbound.map((lk, i) => (
                    <LinkRow
                      key={i}
                      primary={lk.target_urn}
                      anchor={lk.target_anchor}
                      label={lk.label}
                      created={lk.created_at}
                      direction="out"
                    />
                  ))}
                </div>
              ))}

            {/* External */}
            {linksTab === "external" &&
              (externalQuery.isLoading ? (
                <div className="loading-center" style={{ padding: "12px 0" }}>
                  <div className="spinner" />
                </div>
              ) : externalQuery.isError ? (
                <div className="error-banner" style={{ fontSize: 12 }}>
                  Failed to load external links.
                </div>
              ) : external.length === 0 ? (
                <div
                  style={{
                    fontSize: 12,
                    color: "var(--text-muted)",
                    fontStyle: "italic",
                    padding: "4px 0",
                  }}
                >
                  No external URI links from this note.
                </div>
              ) : (
                <div
                  style={{ display: "flex", flexDirection: "column", gap: 4 }}
                >
                  {external.map((el, i) => (
                    <div
                      key={i}
                      style={{
                        background: "var(--bg-elevated)",
                        border: "1px solid var(--border)",
                        borderRadius: "var(--radius-sm)",
                        padding: "6px 10px",
                        display: "flex",
                        flexDirection: "column",
                        gap: 2,
                      }}
                    >
                      <a
                        href={el.uri}
                        target="_blank"
                        rel="noopener noreferrer"
                        style={{
                          fontSize: 12,
                          color: "var(--accent)",
                          textDecoration: "none",
                          overflow: "hidden",
                          textOverflow: "ellipsis",
                          whiteSpace: "nowrap",
                        }}
                        title={el.uri}
                      >
                        {el.uri}
                      </a>
                      {el.label && (
                        <span
                          style={{ fontSize: 11, color: "var(--text-muted)" }}
                        >
                          {el.label}
                        </span>
                      )}
                    </div>
                  ))}
                </div>
              ))}
          </div>

          {/* ── Event stream ──────────────────────────────────────────── */}
          <div className="card" style={{ padding: "14px 16px" }}>
            <div className="card-title">Event stream</div>
            {isSecure ? (
              <div
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: 8,
                  padding: "10px 0",
                  color: "var(--yellow)",
                  fontSize: 13,
                }}
              >
                <Lock size={13} />
                Event stream is encrypted — contents are not readable by the
                admin.
              </div>
            ) : eventsQuery.isLoading ? (
              <div className="loading-center" style={{ padding: "20px 0" }}>
                <div className="spinner" />
                Loading events…
              </div>
            ) : eventsQuery.isError ? (
              <div className="error-banner">Failed to load events.</div>
            ) : (
              <>
                <div style={{ marginBottom: 12, display: "flex", gap: 12 }}>
                  <span className="badge badge-blue">
                    <Hash size={10} />
                    {eventCount} {eventCount === 1 ? "event" : "events"}
                  </span>
                </div>
                {events.length > 0 && (
                  <div
                    style={{
                      display: "flex",
                      flexDirection: "column",
                      gap: 6,
                      maxHeight: 220,
                      overflowY: "auto",
                    }}
                  >
                    {events.map((ev) => (
                      <div
                        key={ev.urn}
                        style={{
                          background: "var(--bg-elevated)",
                          border: "1px solid var(--border)",
                          borderRadius: "var(--radius-sm)",
                          padding: "8px 10px",
                          fontSize: 12,
                        }}
                      >
                        <div
                          style={{
                            display: "flex",
                            justifyContent: "space-between",
                            marginBottom: 4,
                            color: "var(--text-secondary)",
                          }}
                        >
                          <span
                            style={{
                              display: "flex",
                              alignItems: "center",
                              gap: 4,
                            }}
                          >
                            <Hash size={10} />
                            <span className="mono">seq {ev.sequence}</span>
                          </span>
                          <span
                            style={{
                              display: "flex",
                              alignItems: "center",
                              gap: 4,
                            }}
                          >
                            <User size={10} />
                            <span
                              className="mono"
                              style={{
                                fontSize: 10,
                                maxWidth: 140,
                                overflow: "hidden",
                                textOverflow: "ellipsis",
                                whiteSpace: "nowrap",
                              }}
                            >
                              {ev.author_urn || "—"}
                            </span>
                          </span>
                          <span
                            style={{
                              display: "flex",
                              alignItems: "center",
                              gap: 4,
                            }}
                          >
                            <Clock size={10} />
                            <span className="mono">
                              {new Date(ev.created_at).toLocaleTimeString()}
                            </span>
                          </span>
                        </div>
                        <div
                          style={{
                            color: "var(--text-muted)",
                            fontFamily: "var(--font-mono)",
                            fontSize: 11,
                          }}
                        >
                          {ev.entries.length}{" "}
                          {ev.entries.length === 1 ? "entry" : "entries"}
                          {" · "}
                          {ev.entries.filter((e) => e.op === "set").length} set
                          {", "}
                          {
                            ev.entries.filter((e) => e.op === "delete").length
                          }{" "}
                          delete
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
