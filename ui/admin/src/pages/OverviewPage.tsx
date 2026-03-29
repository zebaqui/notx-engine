import { useQuery } from "@tanstack/react-query";
import { RefreshCw, AlertCircle } from "lucide-react";
import { fetchHealth, fetchMetrics } from "../api/client";

function fmt(n: number) {
  return n.toLocaleString();
}

function timeSince(iso: string) {
  const diff = Math.round((Date.now() - new Date(iso).getTime()) / 1000);
  if (diff < 5) return "just now";
  if (diff < 60) return `${diff}s ago`;
  return `${Math.round(diff / 60)}m ago`;
}

export default function OverviewPage() {
  const health = useQuery({
    queryKey: ["health"],
    queryFn: fetchHealth,
    refetchInterval: 15_000,
  });

  const metrics = useQuery({
    queryKey: ["metrics"],
    queryFn: fetchMetrics,
    refetchInterval: 30_000,
  });

  const isLoading = health.isLoading || metrics.isLoading;
  const hasError = health.isError || metrics.isError;

  return (
    <div className="page-stack">
      {/* ── Header ─────────────────────────────────────────────────────── */}
      <div className="section-header">
        <div>
          <div className="section-title">Overview</div>
          <div className="section-sub">Server health and note statistics</div>
        </div>
        <div className="topbar-right">
          {health.data && (
            <span className="last-updated">
              Checked {timeSince(health.data.checked_at)}
            </span>
          )}
          <button
            className="btn btn-ghost"
            onClick={() => {
              health.refetch();
              metrics.refetch();
            }}
            disabled={isLoading}
          >
            <RefreshCw size={14} className={isLoading ? "spin-icon" : ""} />
            Refresh
          </button>
        </div>
      </div>

      {hasError && (
        <div className="error-banner">
          <AlertCircle size={15} />
          Could not reach the notx server. Make sure it is running on{" "}
          <span className="mono">:4060</span>.
        </div>
      )}

      {/* ── Health row ─────────────────────────────────────────────────── */}
      <div>
        <div className="card-title" style={{ marginBottom: 10 }}>
          Server status
        </div>
        <div className="grid-2">
          <HealthCard
            label="HTTP API"
            ok={health.data?.http_ok}
            loading={health.isLoading}
          />
          <HealthCard
            label="Ready probe"
            ok={health.data?.ready_ok}
            loading={health.isLoading}
          />
        </div>
      </div>

      {/* ── Metrics grid ───────────────────────────────────────────────── */}
      <div>
        <div className="card-title" style={{ marginBottom: 10 }}>
          Notes
        </div>
        {metrics.isLoading ? (
          <div className="loading-center">
            <div className="spinner" />
            Loading metrics…
          </div>
        ) : (
          <div className="grid-4">
            <StatTile
              label="Total"
              value={fmt(metrics.data?.total_notes ?? 0)}
              sub="all time, inc. deleted"
            />
            <StatTile
              label="Active"
              value={fmt(metrics.data?.active_notes ?? 0)}
              sub="not soft-deleted"
              accent
            />
            <StatTile
              label="Normal"
              value={fmt(metrics.data?.normal_notes ?? 0)}
              sub="plain text notes"
            />
            <StatTile
              label="Secure"
              value={fmt(metrics.data?.secure_notes ?? 0)}
              sub="encrypted notes"
            />
          </div>
        )}
      </div>

      {/* ── Deleted row ────────────────────────────────────────────────── */}
      {!metrics.isLoading && (
        <div className="grid-2">
          <StatTile
            label="Deleted"
            value={fmt(metrics.data?.deleted_notes ?? 0)}
            sub="soft-deleted, recoverable"
            warn={Boolean(metrics.data && metrics.data.deleted_notes > 0)}
          />
          <div className="stat-tile" style={{ justifyContent: "center" }}>
            <div className="stat-label">Deletion rate</div>
            <div className="stat-value">
              {metrics.data && metrics.data.total_notes > 0
                ? (
                    (metrics.data.deleted_notes / metrics.data.total_notes) *
                    100
                  ).toFixed(1)
                : "0.0"}
              <span style={{ fontSize: 16, fontWeight: 400, color: "var(--text-secondary)" }}>
                %
              </span>
            </div>
            <div className="stat-sub">deleted / total</div>
          </div>
        </div>
      )}
    </div>
  );
}

// ─── Sub-components ──────────────────────────────────────────────────────────

function HealthCard({
  label,
  ok,
  loading,
}: {
  label: string;
  ok?: boolean;
  loading: boolean;
}) {
  return (
    <div className="stat-tile" style={{ flexDirection: "row", alignItems: "center", gap: 14 }}>
      {loading ? (
        <div className="spinner" />
      ) : (
        <div
          style={{
            width: 10,
            height: 10,
            borderRadius: "50%",
            background: ok ? "var(--green)" : "var(--red)",
            boxShadow: ok ? "0 0 8px var(--green)" : "0 0 8px var(--red)",
            flexShrink: 0,
          }}
        />
      )}
      <div>
        <div className="stat-label">{label}</div>
        <div style={{ fontSize: 15, fontWeight: 600, color: "var(--text-primary)", marginTop: 2 }}>
          {loading ? "—" : ok ? "Healthy" : "Unreachable"}
        </div>
      </div>
      {!loading && (
        <div style={{ marginLeft: "auto" }}>
          <span className={`badge ${ok ? "badge-green" : "badge-red"}`}>
            <span className="badge-dot" />
            {ok ? "up" : "down"}
          </span>
        </div>
      )}
    </div>
  );
}

function StatTile({
  label,
  value,
  sub,
  accent,
  warn,
}: {
  label: string;
  value: string;
  sub?: string;
  accent?: boolean;
  warn?: boolean;
}) {
  const color = accent
    ? "var(--accent)"
    : warn
    ? "var(--yellow)"
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
