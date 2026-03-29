import { useQuery } from "@tanstack/react-query";
import { X, Lock, FileText, Clock, User, Hash, Layers } from "lucide-react";
import { fetchNote, fetchNoteEvents } from "../api/client";
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

export default function NoteDrawer({ note, onClose }: Props) {
  const isSecure = note.note_type === "secure";

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

  const content = isSecure ? null : (detail.data?.content ?? null);
  const events = eventsQuery.data?.events ?? [];
  const eventCount = eventsQuery.data?.count ?? 0;

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
            {note.deleted && (
              <span className="badge badge-red">
                <span className="badge-dot" />
                deleted
              </span>
            )}
            {!note.deleted && (
              <span className="badge badge-green">
                <span className="badge-dot" />
                active
              </span>
            )}
          </div>

          {/* Metadata table */}
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

          {/* Content preview (normal notes only) */}
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

          {/* Event stream */}
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
