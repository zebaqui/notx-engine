import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { HugeiconsIcon } from "@hugeicons/react";
import {
  Refresh01Icon,
  AlertCircleIcon,
  Clock01Icon,
  CheckmarkCircle01Icon,
  Cancel01Icon,
} from "@hugeicons/core-free-icons";
import {
  fetchSyncStatus,
  fetchSyncLog,
  fetchSyncPending,
  triggerSync,
} from "../api/client";
import type { SyncLogEntry } from "../api/types";

// ─── Helpers ──────────────────────────────────────────────────────────────────

function timeSince(iso: string): string {
  const diff = Math.round((Date.now() - new Date(iso).getTime()) / 1000);
  if (diff < 5) return "just now";
  if (diff < 60) return `${diff}s ago`;
  if (diff < 3600) return `${Math.round(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.round(diff / 3600)}h ago`;
  return `${Math.round(diff / 86400)}d ago`;
}

function fmtLogTime(iso: string): string {
  const d = new Date(iso);
  const month = d.toLocaleString("en-US", { month: "short" });
  const day = String(d.getDate()).padStart(2, "0");
  const h = String(d.getHours()).padStart(2, "0");
  const m = String(d.getMinutes()).padStart(2, "0");
  const s = String(d.getSeconds()).padStart(2, "0");
  return `${month} ${day} · ${h}:${m}:${s}`;
}

function fmtDate(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleDateString("en-US", {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
}

function truncateUrnShort(urn: string): string {
  const parts = urn.split(":");
  if (parts.length >= 3) {
    const uuid = parts[parts.length - 1];
    return `…${uuid.slice(-8)}`;
  }
  return urn.slice(-8);
}

// ─── Connection dot ───────────────────────────────────────────────────────────

function ConnDot({ connected }: { connected: boolean }) {
  const color = connected ? "#22c55e" : "#ef4444";
  return (
    <span
      style={{
        display: "inline-block",
        width: 8,
        height: 8,
        borderRadius: "50%",
        background: color,
        boxShadow: `0 0 6px ${color}`,
        flexShrink: 0,
      }}
    />
  );
}

// ─── Row ──────────────────────────────────────────────────────────────────────

function InfoRow({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: 8,
        padding: "6px 0",
        borderBottom: "1px solid var(--border)",
      }}
    >
      <span
        style={{
          fontSize: 11,
          fontWeight: 600,
          textTransform: "uppercase",
          letterSpacing: "0.04em",
          color: "var(--text-muted)",
          width: 130,
          flexShrink: 0,
        }}
      >
        {label}
      </span>
      <span style={{ fontSize: 12, color: "var(--text-secondary)" }}>
        {children}
      </span>
    </div>
  );
}

// ─── SyncPage ─────────────────────────────────────────────────────────────────

export default function SyncPage() {
  const qc = useQueryClient();
  const [logEntries, setLogEntries] = useState<SyncLogEntry[]>([]);
  const [nextPageToken, setNextPageToken] = useState<string | undefined>();
  const [loadingMore, setLoadingMore] = useState(false);
  const [pendingOpen, setPendingOpen] = useState(false);

  // ── Status query ────────────────────────────────────────────────────────────
  const statusQuery = useQuery({
    queryKey: ["sync-status"],
    queryFn: fetchSyncStatus,
    refetchInterval: 5_000,
  });

  // ── Pending query ───────────────────────────────────────────────────────────
  const pendingQuery = useQuery({
    queryKey: ["sync-pending"],
    queryFn: fetchSyncPending,
    refetchInterval: 10_000,
  });

  // ── Log query ───────────────────────────────────────────────────────────────
  const logQuery = useQuery({
    queryKey: ["sync-log"],
    queryFn: async () => {
      const res = await fetchSyncLog(50, "");
      setLogEntries(res.entries ?? []);
      setNextPageToken(res.next_page_token);
      return res;
    },
    refetchInterval: 10_000,
  });

  // ── Trigger sync mutation ────────────────────────────────────────────────────
  const syncMut = useMutation({
    mutationFn: triggerSync,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["sync-status"] });
      qc.invalidateQueries({ queryKey: ["sync-log"] });
      qc.invalidateQueries({ queryKey: ["sync-pending"] });
    },
  });

  // ── Load more ────────────────────────────────────────────────────────────────
  async function handleLoadMore() {
    if (!nextPageToken) return;
    setLoadingMore(true);
    try {
      const res = await fetchSyncLog(50, nextPageToken);
      setLogEntries((prev) => [...prev, ...(res.entries ?? [])]);
      setNextPageToken(res.next_page_token);
    } finally {
      setLoadingMore(false);
    }
  }

  const status = statusQuery.data;
  const pending = pendingQuery.data;
  const hasError = statusQuery.isError;

  return (
    <div className="page-stack">
      {/* ── Header ─────────────────────────────────────────────────────── */}
      <div className="section-header">
        <div>
          <div className="section-title">Sync</div>
          <div className="section-sub">Peer synchronisation status and log</div>
        </div>
        <div className="topbar-right">
          <span
            style={{
              fontSize: 10.5,
              color: "var(--text-muted)",
              fontFamily: "var(--font-mono)",
            }}
          >
            auto-refreshing
          </span>
          <button
            className="btn btn-ghost"
            onClick={() => {
              statusQuery.refetch();
              logQuery.refetch();
              pendingQuery.refetch();
            }}
            disabled={statusQuery.isFetching}
          >
            <HugeiconsIcon
              icon={Refresh01Icon}
              size={13}
              strokeWidth={1.5}
              className={statusQuery.isFetching ? "spin-icon" : ""}
            />
            Refresh
          </button>
        </div>
      </div>

      {hasError && (
        <div className="error-banner">
          <HugeiconsIcon icon={AlertCircleIcon} size={14} strokeWidth={1.5} />
          Could not reach the sync status endpoint.
        </div>
      )}

      {/* ── A. Status card ──────────────────────────────────────────────── */}
      <div
        style={{
          background: "var(--surface)",
          border: "1px solid var(--border)",
          borderRadius: 8,
          padding: 20,
          display: "flex",
          flexDirection: "column",
          gap: 0,
        }}
      >
        <div
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            marginBottom: 14,
          }}
        >
          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            {statusQuery.isLoading ? (
              <div className="spinner" />
            ) : (
              <ConnDot connected={status?.connected ?? false} />
            )}
            <span
              style={{
                fontWeight: 600,
                fontSize: 13,
                color: statusQuery.isLoading
                  ? "var(--text-muted)"
                  : status?.connected
                    ? "#22c55e"
                    : "#ef4444",
              }}
            >
              {statusQuery.isLoading
                ? "Checking…"
                : status?.connected
                  ? "Connected"
                  : "Disconnected"}
            </span>
          </div>

          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            {(pending?.count ?? 0) > 0 && (
              <span
                style={{
                  fontSize: 11,
                  fontWeight: 600,
                  padding: "2px 8px",
                  borderRadius: 99,
                  background: "rgba(168,85,247,0.1)",
                  color: "#a855f7",
                  border: "1px solid rgba(168,85,247,0.25)",
                }}
              >
                {pending!.count} pending
              </span>
            )}
            <button
              className="btn btn-primary"
              style={{ fontSize: 12, padding: "4px 12px" }}
              onClick={() => syncMut.mutate()}
              disabled={syncMut.isPending}
            >
              <HugeiconsIcon
                icon={Refresh01Icon}
                size={13}
                strokeWidth={1.5}
                className={syncMut.isPending ? "spin-icon" : ""}
              />
              Sync now
            </button>
          </div>
        </div>

        {/* Info rows */}
        <div>
          <InfoRow label="Peer authority">
            {status ? (
              <span style={{ fontFamily: "var(--font-mono)", fontSize: 12 }}>
                {status.peer_authority || "—"}
              </span>
            ) : (
              "—"
            )}
          </InfoRow>
          <InfoRow label="Connected since">
            {status?.connected_at ? (
              <span title={status.connected_at}>
                {timeSince(status.connected_at)}
              </span>
            ) : (
              "—"
            )}
          </InfoRow>
          <InfoRow label="Last ping">
            {status?.last_ping_at ? (
              <span title={status.last_ping_at}>
                {timeSince(status.last_ping_at)}
              </span>
            ) : (
              "—"
            )}
          </InfoRow>
          <InfoRow label="Cert expiry">
            {status?.cert_expiry ? fmtDate(status.cert_expiry) : "—"}
          </InfoRow>
          <InfoRow label="Pending notes">
            <span
              style={{
                fontWeight: 600,
                color:
                  (status?.pending_count ?? 0) > 0
                    ? "#a855f7"
                    : "var(--text-secondary)",
              }}
            >
              {status?.pending_count ?? "—"}
            </span>
          </InfoRow>
        </div>

        {syncMut.isError && (
          <div
            style={{
              marginTop: 12,
              fontSize: 12,
              color: "#ef4444",
              display: "flex",
              alignItems: "center",
              gap: 6,
            }}
          >
            <HugeiconsIcon icon={AlertCircleIcon} size={13} strokeWidth={1.5} />
            Sync trigger failed. Check server logs.
          </div>
        )}
        {syncMut.isSuccess && (
          <div
            style={{
              marginTop: 12,
              fontSize: 12,
              color: "#22c55e",
              display: "flex",
              alignItems: "center",
              gap: 6,
            }}
          >
            <HugeiconsIcon
              icon={CheckmarkCircle01Icon}
              size={13}
              strokeWidth={1.5}
            />
            Sync triggered successfully.
          </div>
        )}
      </div>

      {/* ── B. Pending notes ────────────────────────────────────────────── */}
      <div
        style={{
          background: "var(--surface)",
          border: "1px solid var(--border)",
          borderRadius: 8,
          overflow: "hidden",
        }}
      >
        <button
          onClick={() => setPendingOpen((v) => !v)}
          style={{
            width: "100%",
            background: "none",
            border: "none",
            padding: "12px 16px",
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            cursor: "pointer",
            color: "var(--text-primary)",
          }}
        >
          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <HugeiconsIcon
              icon={Clock01Icon}
              size={14}
              strokeWidth={1.5}
              style={{ color: "var(--text-muted)" }}
            />
            <span style={{ fontSize: 12, fontWeight: 600 }}>Pending notes</span>
            {pendingQuery.isLoading ? (
              <div className="spinner" style={{ width: 10, height: 10 }} />
            ) : (
              <span
                style={{
                  fontSize: 11,
                  padding: "1px 7px",
                  borderRadius: 99,
                  background:
                    (pending?.count ?? 0) > 0
                      ? "rgba(168,85,247,0.12)"
                      : "var(--bg)",
                  color:
                    (pending?.count ?? 0) > 0 ? "#a855f7" : "var(--text-muted)",
                  border:
                    (pending?.count ?? 0) > 0
                      ? "1px solid rgba(168,85,247,0.25)"
                      : "1px solid var(--border)",
                  fontWeight: 600,
                }}
              >
                {pending?.count ?? 0}
              </span>
            )}
          </div>
          <span style={{ fontSize: 11, color: "var(--text-muted)" }}>
            {pendingOpen ? "▲" : "▼"}
          </span>
        </button>

        {pendingOpen && (
          <div
            style={{
              borderTop: "1px solid var(--border)",
              padding: 12,
              maxHeight: 220,
              overflowY: "auto",
            }}
          >
            {pendingQuery.isLoading ? (
              <div className="loading-center" style={{ padding: "16px 0" }}>
                <div className="spinner" />
                Loading…
              </div>
            ) : !pending?.note_urns?.length ? (
              <div
                style={{
                  fontSize: 12,
                  color: "var(--text-muted)",
                  textAlign: "center",
                  padding: "12px 0",
                }}
              >
                No pending notes
              </div>
            ) : (
              <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
                {pending.note_urns.map((urn) => (
                  <div
                    key={urn}
                    style={{
                      fontSize: 11,
                      fontFamily: "var(--font-mono)",
                      color: "var(--text-secondary)",
                      background: "var(--bg)",
                      border: "1px solid var(--border)",
                      borderRadius: 4,
                      padding: "3px 8px",
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                    }}
                    title={urn}
                  >
                    {urn}
                  </div>
                ))}
              </div>
            )}
          </div>
        )}
      </div>

      {/* ── C. Sync log table ───────────────────────────────────────────── */}
      <div
        style={{
          background: "var(--surface)",
          border: "1px solid var(--border)",
          borderRadius: 8,
          overflow: "hidden",
        }}
      >
        <div
          style={{
            padding: "12px 16px",
            borderBottom: "1px solid var(--border)",
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
          }}
        >
          <span style={{ fontSize: 12, fontWeight: 600 }}>Sync log</span>
          {logQuery.data && (
            <span
              style={{
                fontSize: 11,
                color: "var(--text-muted)",
                fontFamily: "var(--font-mono)",
              }}
            >
              {logQuery.data.total} total
            </span>
          )}
        </div>

        {logQuery.isLoading ? (
          <div className="loading-center" style={{ padding: "32px 0" }}>
            <div className="spinner" />
            Loading…
          </div>
        ) : logQuery.isError ? (
          <div
            style={{
              padding: 20,
              fontSize: 12,
              color: "#ef4444",
              display: "flex",
              alignItems: "center",
              gap: 6,
            }}
          >
            <HugeiconsIcon icon={AlertCircleIcon} size={13} strokeWidth={1.5} />
            Failed to load sync log.
          </div>
        ) : logEntries.length === 0 ? (
          <div
            style={{
              padding: 32,
              fontSize: 12,
              color: "var(--text-muted)",
              textAlign: "center",
            }}
          >
            No sync entries yet.
          </div>
        ) : (
          <>
            <div style={{ overflowX: "auto" }}>
              <table
                style={{
                  width: "100%",
                  borderCollapse: "collapse",
                }}
              >
                <thead>
                  <tr>
                    {[
                      "Time",
                      "Note",
                      "Direction",
                      "Events",
                      "Status",
                      "Error",
                    ].map((col) => (
                      <th
                        key={col}
                        style={{
                          padding: "8px 14px",
                          textAlign: "left",
                          fontSize: 11,
                          fontWeight: 600,
                          textTransform: "uppercase",
                          letterSpacing: "0.04em",
                          color: "var(--text-muted)",
                          whiteSpace: "nowrap",
                          borderBottom: "1px solid var(--border)",
                        }}
                      >
                        {col}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {logEntries.map((entry) => (
                    <LogRow key={entry.id} entry={entry} />
                  ))}
                </tbody>
              </table>
            </div>

            {nextPageToken && (
              <div
                style={{
                  padding: "12px 16px",
                  borderTop: "1px solid var(--border)",
                  display: "flex",
                  justifyContent: "center",
                }}
              >
                <button
                  className="btn btn-ghost"
                  onClick={handleLoadMore}
                  disabled={loadingMore}
                >
                  {loadingMore ? (
                    <>
                      <div className="spinner" />
                      Loading…
                    </>
                  ) : (
                    "Load more"
                  )}
                </button>
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );
}

// ─── Log row ─────────────────────────────────────────────────────────────────

function LogRow({ entry }: { entry: SyncLogEntry }) {
  return (
    <tr
      style={{
        borderBottom: "1px solid var(--border)",
        transition: "background 0.1s",
      }}
      onMouseEnter={(e) =>
        (e.currentTarget.style.background = "rgba(255,255,255,0.03)")
      }
      onMouseLeave={(e) => (e.currentTarget.style.background = "transparent")}
    >
      {/* Time */}
      <td
        style={{
          padding: "8px 14px",
          fontSize: 12,
          color: "var(--text-secondary)",
          fontFamily: "var(--font-mono)",
          whiteSpace: "nowrap",
        }}
      >
        {fmtLogTime(entry.synced_at)}
      </td>

      {/* Note URN */}
      <td
        style={{
          padding: "8px 14px",
          fontSize: 12,
          fontFamily: "var(--font-mono)",
          color: "var(--text-muted)",
          whiteSpace: "nowrap",
        }}
        title={entry.note_urn}
      >
        {truncateUrnShort(entry.note_urn)}
      </td>

      {/* Direction */}
      <td style={{ padding: "8px 14px", whiteSpace: "nowrap" }}>
        {entry.direction === "push" ? (
          <span
            style={{
              fontSize: 12,
              fontWeight: 600,
              color: "#3b82f6",
            }}
          >
            ↑ push
          </span>
        ) : (
          <span
            style={{
              fontSize: 12,
              fontWeight: 600,
              color: "#a855f7",
            }}
          >
            ↓ pull
          </span>
        )}
      </td>

      {/* Events */}
      <td
        style={{
          padding: "8px 14px",
          fontSize: 12,
          color: "var(--text-secondary)",
          textAlign: "right",
        }}
      >
        {entry.event_count}
      </td>

      {/* Status */}
      <td style={{ padding: "8px 14px", whiteSpace: "nowrap" }}>
        {entry.status === "ok" ? (
          <span
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: 4,
              fontSize: 11,
              fontWeight: 600,
              padding: "2px 8px",
              borderRadius: 99,
              background: "rgba(34,197,94,0.1)",
              color: "#22c55e",
              border: "1px solid rgba(34,197,94,0.25)",
            }}
          >
            <HugeiconsIcon
              icon={CheckmarkCircle01Icon}
              size={11}
              strokeWidth={1.5}
            />
            ok
          </span>
        ) : (
          <span
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: 4,
              fontSize: 11,
              fontWeight: 600,
              padding: "2px 8px",
              borderRadius: 99,
              background: "rgba(239,68,68,0.1)",
              color: "#ef4444",
              border: "1px solid rgba(239,68,68,0.25)",
            }}
          >
            <HugeiconsIcon icon={Cancel01Icon} size={11} strokeWidth={1.5} />
            error
          </span>
        )}
      </td>

      {/* Error */}
      <td
        style={{
          padding: "8px 14px",
          fontSize: 11,
          color: "#ef4444",
          fontFamily: "var(--font-mono)",
          maxWidth: 300,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
        title={entry.error}
      >
        {entry.error ? entry.error.slice(0, 60) : ""}
      </td>
    </tr>
  );
}
