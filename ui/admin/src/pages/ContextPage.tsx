import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { HugeiconsIcon } from "@hugeicons/react";
import {
  Refresh01Icon,
  AlertCircleIcon,
  GitBranchIcon,
  ArrowLeft01Icon,
  ArrowRight01Icon,
  CheckmarkCircle01Icon,
} from "@hugeicons/core-free-icons";
import {
  fetchContextStats,
  fetchCandidates,
  promoteCandidate,
  dismissCandidate,
} from "../api/client";
import type { CandidateRecord, PromoteResponse } from "../api/types";

// ─── Helpers ──────────────────────────────────────────────────────────────────

function shortUrn(urn: string) {
  const parts = urn.split(":");
  return parts.length >= 3 ? "…" + parts[parts.length - 1].slice(-8) : urn;
}

function fmtAge(iso?: string): string {
  if (!iso) return "—";
  const diff = Math.round((Date.now() - new Date(iso).getTime()) / 1000);
  if (diff < 60) return `${diff}s`;
  if (diff < 3600) return `${Math.round(diff / 60)}m`;
  if (diff < 86400) return `${Math.round(diff / 3600)}h`;
  return `${Math.round(diff / 86400)}d`;
}

const STATUS_BADGE: Record<string, string> = {
  pending: "badge badge-yellow",
  promoted: "badge badge-green",
  dismissed: "badge badge-red",
  expired: "badge",
};

// ─── Page ─────────────────────────────────────────────────────────────────────

export default function ContextPage() {
  const qc = useQueryClient();

  // ── Filters ────────────────────────────────────────────────────────────────
  const [projectUrn, setProjectUrn] = useState("");
  const [status, setStatus] = useState("pending");
  const [minScore, setMinScore] = useState("0.15");
  const [includeBursts, setIncludeBursts] = useState(true);

  // ── Pagination ─────────────────────────────────────────────────────────────
  const [pageToken, setPageToken] = useState<string | undefined>(undefined);
  const [tokenHistory, setTokenHistory] = useState<string[]>([]);

  // ── Drawer ─────────────────────────────────────────────────────────────────
  const [selected, setSelected] = useState<CandidateRecord | null>(null);
  const [promoteLabel, setPromoteLabel] = useState("");
  const [showPromoteInput, setShowPromoteInput] = useState(false);
  const [promoteResult, setPromoteResult] = useState<PromoteResponse | null>(
    null,
  );

  // ── Queries ────────────────────────────────────────────────────────────────
  const statsQuery = useQuery({
    queryKey: ["context-stats", projectUrn],
    queryFn: () => fetchContextStats(projectUrn || undefined),
    refetchInterval: 30_000,
  });

  const candidatesQuery = useQuery({
    queryKey: [
      "candidates",
      projectUrn,
      status,
      minScore,
      includeBursts,
      pageToken,
    ],
    queryFn: () =>
      fetchCandidates({
        project_urn: projectUrn || undefined,
        status: status === "all" ? undefined : status,
        min_score: parseFloat(minScore),
        include_bursts: includeBursts,
        page_size: 20,
        page_token: pageToken,
      }),
    placeholderData: (prev) => prev,
  });

  // ── Mutations ──────────────────────────────────────────────────────────────
  const promoteMut = useMutation({
    mutationFn: ({ id, label }: { id: string; label: string }) =>
      promoteCandidate(id, { label }),
    onSuccess: (result) => {
      qc.invalidateQueries({ queryKey: ["candidates"] });
      qc.invalidateQueries({ queryKey: ["context-stats"] });
      setPromoteResult(result);
      setShowPromoteInput(false);
      setPromoteLabel("");
      // refresh the selected candidate from the list
      setSelected((prev) => (prev ? { ...prev, status: "promoted" } : prev));
    },
  });

  const dismissMut = useMutation({
    mutationFn: (id: string) => dismissCandidate(id, "urn:notx:usr:anon"),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["candidates"] });
      qc.invalidateQueries({ queryKey: ["context-stats"] });
      setSelected((prev) => (prev ? { ...prev, status: "dismissed" } : prev));
    },
  });

  // ── Pagination helpers ─────────────────────────────────────────────────────
  const candidates = candidatesQuery.data?.candidates ?? [];
  const nextToken = candidatesQuery.data?.next_page_token;

  function goNext() {
    if (!nextToken) return;
    setTokenHistory((h) => [...h, pageToken ?? ""]);
    setPageToken(nextToken);
  }

  function goPrev() {
    const hist = [...tokenHistory];
    const prev = hist.pop();
    setTokenHistory(hist);
    setPageToken(prev === "" ? undefined : prev);
  }

  // ── Drawer close ───────────────────────────────────────────────────────────
  function closeDrawer() {
    setSelected(null);
    setShowPromoteInput(false);
    setPromoteLabel("");
    setPromoteResult(null);
  }

  function openDrawer(c: CandidateRecord) {
    setSelected(c);
    setShowPromoteInput(false);
    setPromoteLabel("");
    setPromoteResult(null);
  }

  // ── Readiness badge ────────────────────────────────────────────────────────
  function ReadinessBadge() {
    const s = statsQuery.data;
    if (!s) return null;
    if (s.candidates_pending === 0) {
      return (
        <span className="badge badge-red">
          <span className="badge-dot" />
          Queue empty
        </span>
      );
    }
    if (s.candidates_pending_unenriched === 0) {
      return (
        <span className="badge badge-green">
          <span className="badge-dot" />
          Queue ready
        </span>
      );
    }
    return (
      <span className="badge badge-yellow">
        <span className="badge-dot" />
        Scorer running ({s.candidates_pending_unenriched} unenriched)
      </span>
    );
  }

  const isActionPending = promoteMut.isPending || dismissMut.isPending;
  const selectedIsReviewable = selected && selected.status === "pending";

  return (
    <>
      <div className="page-stack">
        {/* ── Header ───────────────────────────────────────────────────────── */}
        <div className="section-header">
          <div>
            <div className="section-title">Context Graph</div>
            <div className="section-sub">
              Review and promote candidate connections between notes
            </div>
          </div>
          <div className="topbar-right">
            <ReadinessBadge />
            <button
              className="btn btn-ghost"
              onClick={() => {
                statsQuery.refetch();
                candidatesQuery.refetch();
              }}
              disabled={statsQuery.isLoading || candidatesQuery.isLoading}
            >
              <HugeiconsIcon
                icon={Refresh01Icon}
                size={14}
                strokeWidth={1.5}
                className={
                  statsQuery.isLoading || candidatesQuery.isLoading
                    ? "spin-icon"
                    : ""
                }
              />
              Refresh
            </button>
          </div>
        </div>

        {/* ── Error ────────────────────────────────────────────────────────── */}
        {(statsQuery.isError || candidatesQuery.isError) && (
          <div className="error-banner">
            <HugeiconsIcon icon={AlertCircleIcon} size={15} strokeWidth={1.5} />
            {statsQuery.error instanceof Error
              ? statsQuery.error.message
              : candidatesQuery.error instanceof Error
                ? candidatesQuery.error.message
                : "Failed to load context data"}
          </div>
        )}

        {/* ── Stats bar ────────────────────────────────────────────────────── */}
        {statsQuery.isLoading ? (
          <div className="loading-center" style={{ padding: "20px 0" }}>
            <div className="spinner" /> Loading stats…
          </div>
        ) : statsQuery.data ? (
          <div className="grid-4">
            <StatTile
              label="Bursts today"
              value={String(statsQuery.data.bursts_today)}
              sub={`${statsQuery.data.bursts_total} total`}
            />
            <StatTile
              label="Pending"
              value={String(statsQuery.data.candidates_pending)}
              sub={
                statsQuery.data.candidates_pending_unenriched > 0
                  ? `${statsQuery.data.candidates_pending_unenriched} unenriched`
                  : "all scored"
              }
              accent={statsQuery.data.candidates_pending > 0}
            />
            <StatTile
              label="Promoted"
              value={String(statsQuery.data.candidates_promoted)}
              sub="linked"
              ok
            />
            <StatTile
              label="Dismissed"
              value={String(statsQuery.data.candidates_dismissed)}
              sub={
                statsQuery.data.oldest_pending_age_days > 0
                  ? `oldest ${statsQuery.data.oldest_pending_age_days.toFixed(1)}d`
                  : "none pending"
              }
            />
          </div>
        ) : null}

        {/* ── Filter toolbar ───────────────────────────────────────────────── */}
        <div className="toolbar" style={{ flexWrap: "wrap", gap: 8 }}>
          <input
            className="search-input"
            style={{ width: 260 }}
            placeholder="Filter by project URN…"
            value={projectUrn}
            onChange={(e) => {
              setProjectUrn(e.target.value);
              setPageToken(undefined);
              setTokenHistory([]);
            }}
          />

          <select
            className="search-input"
            style={{ width: 160 }}
            value={status}
            onChange={(e) => {
              setStatus(e.target.value);
              setPageToken(undefined);
              setTokenHistory([]);
            }}
          >
            <option value="pending">Pending</option>
            <option value="promoted">Promoted</option>
            <option value="dismissed">Dismissed</option>
            <option value="all">All statuses</option>
          </select>

          <select
            className="search-input"
            style={{ width: 200 }}
            value={minScore}
            onChange={(e) => {
              setMinScore(e.target.value);
              setPageToken(undefined);
              setTokenHistory([]);
            }}
          >
            <option value="0.15">≥ 0.15 (recommended)</option>
            <option value="0.0">≥ 0.0 (all)</option>
            <option value="0.25">≥ 0.25 (strict)</option>
          </select>

          <label
            style={{
              display: "flex",
              alignItems: "center",
              gap: 6,
              fontSize: 13,
              color: "var(--text-secondary)",
              cursor: "pointer",
              userSelect: "none",
            }}
          >
            <input
              type="checkbox"
              checked={includeBursts}
              onChange={(e) => setIncludeBursts(e.target.checked)}
              style={{ accentColor: "var(--accent)", cursor: "pointer" }}
            />
            Include burst text
          </label>
        </div>

        {/* ── Candidates table ─────────────────────────────────────────────── */}
        {candidatesQuery.isLoading ? (
          <div className="loading-center">
            <div className="spinner" /> Loading candidates…
          </div>
        ) : candidates.length === 0 ? (
          <div className="empty-state">
            <HugeiconsIcon
              icon={GitBranchIcon}
              size={28}
              strokeWidth={1.5}
              style={{ opacity: 0.3, marginBottom: 8 }}
            />
            No candidates found for the current filters
          </div>
        ) : (
          <div style={{ overflowX: "auto" }}>
            <table className="data-table">
              <thead>
                <tr>
                  <th>Score</th>
                  <th>Note A</th>
                  <th>Note B</th>
                  <th>Status</th>
                  <th>Age</th>
                  <th style={{ width: 120 }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {candidates.map((c) => (
                  <tr
                    key={c.id}
                    style={{
                      cursor: "pointer",
                      background:
                        selected?.id === c.id
                          ? "var(--bg-secondary)"
                          : undefined,
                    }}
                    onClick={() => openDrawer(c)}
                  >
                    <td>
                      <div
                        style={{ fontSize: 12, fontFamily: "var(--font-mono)" }}
                      >
                        {c.overlap_score.toFixed(3)}
                      </div>
                      <div style={{ fontSize: 10, color: "var(--text-muted)" }}>
                        BM25: {c.bm25_score.toFixed(2)}
                      </div>
                    </td>
                    <td className="urn-cell" title={c.note_urn_a}>
                      {shortUrn(c.note_urn_a)}
                    </td>
                    <td className="urn-cell" title={c.note_urn_b}>
                      {shortUrn(c.note_urn_b)}
                    </td>
                    <td>
                      <span className={STATUS_BADGE[c.status] ?? "badge"}>
                        {c.status}
                      </span>
                    </td>
                    <td style={{ fontSize: 12, color: "var(--text-muted)" }}>
                      {fmtAge(c.created_at)}
                    </td>
                    <td onClick={(e) => e.stopPropagation()}>
                      <div style={{ display: "flex", gap: 4 }}>
                        {c.status === "pending" && (
                          <>
                            <button
                              className="btn btn-ghost"
                              style={{ padding: "3px 8px", fontSize: 12 }}
                              disabled={isActionPending}
                              onClick={() => {
                                openDrawer(c);
                                setShowPromoteInput(true);
                              }}
                            >
                              <span style={{ fontSize: 12 }}>✓</span>
                              Promote
                            </button>
                            <button
                              className="btn btn-ghost"
                              style={{
                                padding: "3px 8px",
                                fontSize: 12,
                                color: "var(--red)",
                              }}
                              disabled={isActionPending}
                              onClick={() => dismissMut.mutate(c.id)}
                            >
                              <span style={{ fontSize: 14 }}>×</span>
                              Dismiss
                            </button>
                          </>
                        )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        {/* ── Pagination ───────────────────────────────────────────────────── */}
        {(tokenHistory.length > 0 || nextToken) && (
          <div className="pagination">
            <button
              className="btn btn-ghost"
              onClick={goPrev}
              disabled={tokenHistory.length === 0}
            >
              <HugeiconsIcon
                icon={ArrowLeft01Icon}
                size={14}
                strokeWidth={1.5}
              />{" "}
              Prev
            </button>
            <span style={{ fontSize: 12, color: "var(--text-muted)" }}>
              Page {tokenHistory.length + 1}
            </span>
            <button
              className="btn btn-ghost"
              onClick={goNext}
              disabled={!nextToken}
            >
              Next{" "}
              <HugeiconsIcon
                icon={ArrowRight01Icon}
                size={14}
                strokeWidth={1.5}
              />
            </button>
          </div>
        )}
      </div>

      {/* ── Drawer ───────────────────────────────────────────────────────── */}
      {selected && (
        <div
          className="drawer-overlay"
          onClick={(e) => e.target === e.currentTarget && closeDrawer()}
        >
          <div className="drawer">
            <div className="drawer-header">
              <span className="drawer-title">Candidate Detail</span>
              <button className="close-btn" onClick={closeDrawer}>
                <span style={{ fontSize: 14 }}>×</span>
              </button>
            </div>

            <div className="drawer-body">
              {/* ── Metadata ─────────────────────────────────────────────── */}
              <table className="kv-table" style={{ marginBottom: 16 }}>
                <tbody>
                  <tr>
                    <td>ID</td>
                    <td className="mono" style={{ fontSize: 11 }}>
                      {selected.id}
                    </td>
                  </tr>
                  <tr>
                    <td>Status</td>
                    <td>
                      <span
                        className={STATUS_BADGE[selected.status] ?? "badge"}
                      >
                        {selected.status}
                      </span>
                    </td>
                  </tr>
                  <tr>
                    <td>Overlap</td>
                    <td className="mono">
                      {selected.overlap_score.toFixed(4)}
                    </td>
                  </tr>
                  <tr>
                    <td>BM25</td>
                    <td className="mono">{selected.bm25_score.toFixed(4)}</td>
                  </tr>
                  <tr>
                    <td>Created</td>
                    <td style={{ fontSize: 12, color: "var(--text-muted)" }}>
                      {selected.created_at
                        ? new Date(selected.created_at).toLocaleString()
                        : "—"}
                    </td>
                  </tr>
                  {selected.reviewed_at && (
                    <tr>
                      <td>Reviewed</td>
                      <td style={{ fontSize: 12, color: "var(--text-muted)" }}>
                        {new Date(selected.reviewed_at).toLocaleString()}
                      </td>
                    </tr>
                  )}
                  {selected.promoted_link && (
                    <tr>
                      <td>Link</td>
                      <td className="mono" style={{ fontSize: 11 }}>
                        {selected.promoted_link}
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>

              <div className="divider" />

              {/* ── Burst A ──────────────────────────────────────────────── */}
              <BurstPanel
                label="Burst A"
                noteUrn={selected.note_urn_a}
                burst={selected.burst_a}
              />

              <div className="divider" />

              {/* ── Burst B ──────────────────────────────────────────────── */}
              <BurstPanel
                label="Burst B"
                noteUrn={selected.note_urn_b}
                burst={selected.burst_b}
              />

              <div className="divider" />

              {/* ── Promote result ───────────────────────────────────────── */}
              {promoteResult && (
                <div
                  style={{
                    padding: "10px 12px",
                    borderRadius: "var(--radius-sm, 4px)",
                    background: "rgba(34,197,94,0.08)",
                    border: "1px solid var(--green)",
                    marginBottom: 12,
                  }}
                >
                  <div
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: 6,
                      color: "var(--green)",
                      fontWeight: 600,
                      fontSize: 13,
                      marginBottom: 6,
                    }}
                  >
                    <HugeiconsIcon
                      icon={CheckmarkCircle01Icon}
                      size={14}
                      strokeWidth={1.5}
                    />
                    Promoted successfully
                  </div>
                  <table className="kv-table">
                    <tbody>
                      <tr>
                        <td>Anchor A</td>
                        <td className="mono" style={{ fontSize: 11 }}>
                          {promoteResult.anchor_a_id}
                        </td>
                      </tr>
                      <tr>
                        <td>Anchor B</td>
                        <td className="mono" style={{ fontSize: 11 }}>
                          {promoteResult.anchor_b_id}
                        </td>
                      </tr>
                      {promoteResult.link_a_to_b && (
                        <tr>
                          <td>Link A→B</td>
                          <td className="mono" style={{ fontSize: 11 }}>
                            {promoteResult.link_a_to_b}
                          </td>
                        </tr>
                      )}
                      {promoteResult.link_b_to_a && (
                        <tr>
                          <td>Link B→A</td>
                          <td className="mono" style={{ fontSize: 11 }}>
                            {promoteResult.link_b_to_a}
                          </td>
                        </tr>
                      )}
                    </tbody>
                  </table>
                </div>
              )}

              {/* ── Mutation errors ──────────────────────────────────────── */}
              {promoteMut.isError && (
                <div className="error-banner" style={{ marginBottom: 8 }}>
                  <HugeiconsIcon
                    icon={AlertCircleIcon}
                    size={13}
                    strokeWidth={1.5}
                  />
                  {promoteMut.error instanceof Error
                    ? promoteMut.error.message
                    : "Failed to promote candidate"}
                </div>
              )}
              {dismissMut.isError && (
                <div className="error-banner" style={{ marginBottom: 8 }}>
                  <HugeiconsIcon
                    icon={AlertCircleIcon}
                    size={13}
                    strokeWidth={1.5}
                  />
                  {dismissMut.error instanceof Error
                    ? dismissMut.error.message
                    : "Failed to dismiss candidate"}
                </div>
              )}

              {/* ── Actions ──────────────────────────────────────────────── */}
              {selectedIsReviewable && (
                <div>
                  {showPromoteInput ? (
                    <div
                      style={{
                        display: "flex",
                        flexDirection: "column",
                        gap: 8,
                      }}
                    >
                      <div
                        style={{
                          fontSize: 11,
                          color: "var(--text-muted)",
                          textTransform: "uppercase",
                          letterSpacing: "0.06em",
                          fontWeight: 600,
                        }}
                      >
                        Link label
                      </div>
                      <input
                        className="search-input"
                        style={{
                          width: "100%",
                          fontFamily: "var(--font-mono)",
                        }}
                        placeholder="e.g. related-concept"
                        value={promoteLabel}
                        onChange={(e) =>
                          setPromoteLabel(
                            e.target.value
                              .toLowerCase()
                              .replace(/\s+/g, "-")
                              .replace(/[^a-z0-9-]/g, ""),
                          )
                        }
                        onKeyDown={(e) => {
                          if (e.key === "Enter" && promoteLabel.trim()) {
                            promoteMut.mutate({
                              id: selected.id,
                              label: promoteLabel.trim(),
                            });
                          }
                          if (e.key === "Escape") {
                            setShowPromoteInput(false);
                            setPromoteLabel("");
                          }
                        }}
                        autoFocus
                      />
                      <div style={{ display: "flex", gap: 8 }}>
                        <button
                          className="btn btn-primary"
                          style={{ flex: 1 }}
                          disabled={
                            !promoteLabel.trim() || promoteMut.isPending
                          }
                          onClick={() =>
                            promoteMut.mutate({
                              id: selected.id,
                              label: promoteLabel.trim(),
                            })
                          }
                        >
                          {promoteMut.isPending ? (
                            <>
                              <div
                                className="spinner"
                                style={{ width: 12, height: 12 }}
                              />
                              Promoting…
                            </>
                          ) : (
                            <>
                              <span style={{ fontSize: 12 }}>✓</span>
                              Confirm promote
                            </>
                          )}
                        </button>
                        <button
                          className="btn btn-ghost"
                          onClick={() => {
                            setShowPromoteInput(false);
                            setPromoteLabel("");
                          }}
                          disabled={promoteMut.isPending}
                        >
                          Cancel
                        </button>
                      </div>
                    </div>
                  ) : (
                    <div style={{ display: "flex", gap: 8 }}>
                      <button
                        className="btn btn-primary"
                        style={{ flex: 1 }}
                        disabled={isActionPending}
                        onClick={() => setShowPromoteInput(true)}
                      >
                        <span style={{ fontSize: 12 }}>✓</span>
                        Promote…
                      </button>
                      <button
                        className="btn btn-ghost"
                        style={{ flex: 1, color: "var(--red)" }}
                        disabled={isActionPending}
                        onClick={() => dismissMut.mutate(selected.id)}
                      >
                        {dismissMut.isPending ? (
                          <>
                            <div
                              className="spinner"
                              style={{ width: 12, height: 12 }}
                            />
                            Dismissing…
                          </>
                        ) : (
                          <>
                            <span style={{ fontSize: 14 }}>×</span>
                            Dismiss
                          </>
                        )}
                      </button>
                    </div>
                  )}
                </div>
              )}
            </div>
          </div>
        </div>
      )}
    </>
  );
}

// ─── Sub-components ───────────────────────────────────────────────────────────

function StatTile({
  label,
  value,
  sub,
  accent,
  ok,
}: {
  label: string;
  value: string;
  sub?: string;
  accent?: boolean;
  ok?: boolean;
}) {
  const color = ok
    ? "var(--green)"
    : accent
      ? "var(--accent)"
      : "var(--text-primary)";
  return (
    <div className="stat-tile">
      <div className="stat-label">{label}</div>
      <div className="stat-value" style={{ color }}>
        {value}
      </div>
      {sub && <div className="stat-sub">{sub}</div>}
    </div>
  );
}

function BurstPanel({
  label,
  noteUrn,
  burst,
}: {
  label: string;
  noteUrn: string;
  burst?: import("../api/types").BurstRecord;
}) {
  return (
    <div style={{ marginBottom: 12 }}>
      <div
        style={{
          fontSize: 11,
          fontWeight: 600,
          textTransform: "uppercase",
          letterSpacing: "0.06em",
          color: "var(--text-muted)",
          marginBottom: 6,
        }}
      >
        {label}
      </div>
      <table className="kv-table" style={{ marginBottom: 8 }}>
        <tbody>
          <tr>
            <td>Note</td>
            <td
              className="urn-cell mono"
              title={noteUrn}
              style={{ fontSize: 11 }}
            >
              {noteUrn}
            </td>
          </tr>
          {burst && (
            <>
              <tr>
                <td>Lines</td>
                <td className="mono" style={{ fontSize: 11 }}>
                  {burst.line_start}–{burst.line_end}
                </td>
              </tr>
              <tr>
                <td>Sequence</td>
                <td className="mono" style={{ fontSize: 11 }}>
                  {burst.sequence}
                </td>
              </tr>
              {burst.truncated && (
                <tr>
                  <td>Truncated</td>
                  <td>
                    <span className="badge badge-yellow">yes</span>
                  </td>
                </tr>
              )}
            </>
          )}
        </tbody>
      </table>
      {burst?.text ? (
        <pre className="content-preview">{burst.text}</pre>
      ) : (
        <div
          style={{
            fontSize: 12,
            color: "var(--text-muted)",
            fontStyle: "italic",
          }}
        >
          {burst
            ? "No text available"
            : "Load candidates with burst text enabled to preview"}
        </div>
      )}
    </div>
  );
}
