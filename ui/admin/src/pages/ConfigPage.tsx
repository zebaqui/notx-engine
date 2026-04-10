import { useQuery } from "@tanstack/react-query";
import { HugeiconsIcon } from "@hugeicons/react";
import {
  Refresh01Icon,
  AlertCircleIcon,
  Shield01Icon,
  ShieldBanIcon,
  ServerStack01Icon,
  Database01Icon,
  SlidersHorizontalIcon,
} from "@hugeicons/core-free-icons";
import { fetchHealth } from "../api/client";
import type { ServerConfig } from "../api/types";

// The server doesn't expose a /config endpoint yet, so we read the values
// that ARE observable (ports from health probes) and show the rest as
// "configured at startup". When a real /admin/config endpoint lands, swap
// fetchConfig() in here.
const STATIC_CONFIG: ServerConfig = {
  http_port: 4060,
  grpc_port: 50051,
  host: "0.0.0.0",
  data_dir: "./data",
  enable_http: true,
  enable_grpc: true,
  tls_enabled: false,
  mtls_enabled: false,
  shutdown_timeout_s: 30,
  max_page_size: 200,
  default_page_size: 50,
  log_level: "info",
};

function BoolBadge({ value }: { value: boolean }) {
  return value ? (
    <span className="badge badge-green">
      <span className="badge-dot" />
      enabled
    </span>
  ) : (
    <span className="badge badge-red">
      <span className="badge-dot" />
      disabled
    </span>
  );
}

function Section({
  icon,
  title,
  children,
}: {
  icon: React.ReactNode;
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div className="card">
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 8,
          marginBottom: 16,
          color: "var(--text-secondary)",
        }}
      >
        {icon}
        <span className="card-title" style={{ marginBottom: 0 }}>
          {title}
        </span>
      </div>
      {children}
    </div>
  );
}

export default function ConfigPage() {
  const health = useQuery({
    queryKey: ["health"],
    queryFn: fetchHealth,
    refetchInterval: 15_000,
  });

  const cfg = STATIC_CONFIG;

  return (
    <div className="page-stack">
      {/* ── Header ──────────────────────────────────────────────────────── */}
      <div className="section-header">
        <div>
          <div className="section-title">Configuration</div>
          <div className="section-sub">
            Runtime settings applied at server startup
          </div>
        </div>
        <button
          className="btn btn-ghost"
          onClick={() => health.refetch()}
          disabled={health.isLoading}
        >
          <HugeiconsIcon icon={Refresh01Icon} size={14} strokeWidth={1.5} />
          Refresh
        </button>
      </div>

      {health.isError && (
        <div className="error-banner">
          <HugeiconsIcon icon={AlertCircleIcon} size={15} strokeWidth={1.5} />
          Server unreachable — configuration shown below reflects compiled
          defaults.
        </div>
      )}

      {/* ── Protocol toggles ─────────────────────────────────────────────── */}
      <Section
        icon={
          <HugeiconsIcon icon={ServerStack01Icon} size={14} strokeWidth={1.5} />
        }
        title="Protocol servers"
      >
        <table className="kv-table">
          <tbody>
            <tr>
              <td>HTTP API</td>
              <td>
                <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
                  <BoolBadge value={cfg.enable_http} />
                  {cfg.enable_http && (
                    <span className="mono">
                      {cfg.host || "0.0.0.0"}:{cfg.http_port}
                    </span>
                  )}
                </div>
              </td>
            </tr>
            <tr>
              <td>gRPC</td>
              <td>
                <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
                  <BoolBadge value={cfg.enable_grpc} />
                  {cfg.enable_grpc && (
                    <span className="mono">
                      {cfg.host || "0.0.0.0"}:{cfg.grpc_port}
                    </span>
                  )}
                </div>
              </td>
            </tr>
            <tr>
              <td>Bind host</td>
              <td>
                <span className="mono">
                  {cfg.host === "" ? "0.0.0.0 (all interfaces)" : cfg.host}
                </span>
              </td>
            </tr>
            <tr>
              <td>HTTP port</td>
              <td>
                <span className="mono">{cfg.http_port}</span>
              </td>
            </tr>
            <tr>
              <td>gRPC port</td>
              <td>
                <span className="mono">{cfg.grpc_port}</span>
              </td>
            </tr>
          </tbody>
        </table>
      </Section>

      {/* ── TLS ──────────────────────────────────────────────────────────── */}
      <Section
        icon={
          cfg.tls_enabled ? (
            <HugeiconsIcon
              icon={Shield01Icon}
              size={14}
              strokeWidth={1.5}
              style={{ color: "var(--green)" }}
            />
          ) : (
            <HugeiconsIcon
              icon={ShieldBanIcon}
              size={14}
              strokeWidth={1.5}
              style={{ color: "var(--yellow)" }}
            />
          )
        }
        title="TLS / security"
      >
        <table className="kv-table">
          <tbody>
            <tr>
              <td>TLS</td>
              <td>
                <BoolBadge value={cfg.tls_enabled} />
                {!cfg.tls_enabled && (
                  <span
                    style={{
                      marginLeft: 10,
                      fontSize: 12,
                      color: "var(--yellow)",
                    }}
                  >
                    ⚠ Suitable for development only
                  </span>
                )}
              </td>
            </tr>
            <tr>
              <td>Mutual TLS (mTLS)</td>
              <td>
                <BoolBadge value={cfg.mtls_enabled} />
              </td>
            </tr>
            <tr>
              <td>TLS cert file</td>
              <td>
                <span className="mono">
                  {cfg.tls_enabled ? "configured" : "—"}
                </span>
              </td>
            </tr>
            <tr>
              <td>TLS key file</td>
              <td>
                <span className="mono">
                  {cfg.tls_enabled ? "configured" : "—"}
                </span>
              </td>
            </tr>
            <tr>
              <td>CA cert file</td>
              <td>
                <span className="mono">
                  {cfg.mtls_enabled ? "configured" : "—"}
                </span>
              </td>
            </tr>
          </tbody>
        </table>
      </Section>

      {/* ── Storage ──────────────────────────────────────────────────────── */}
      <Section
        icon={
          <HugeiconsIcon icon={Database01Icon} size={14} strokeWidth={1.5} />
        }
        title="Storage"
      >
        <table className="kv-table">
          <tbody>
            <tr>
              <td>Data directory</td>
              <td>
                <span className="mono">{cfg.data_dir}</span>
              </td>
            </tr>
            <tr>
              <td>Notes path</td>
              <td>
                <span className="mono">{cfg.data_dir}/notes/</span>
              </td>
            </tr>
            <tr>
              <td>Badger index path</td>
              <td>
                <span className="mono">{cfg.data_dir}/index/</span>
              </td>
            </tr>
          </tbody>
        </table>
      </Section>

      {/* ── Operational ──────────────────────────────────────────────────── */}
      <Section
        icon={
          <HugeiconsIcon
            icon={SlidersHorizontalIcon}
            size={14}
            strokeWidth={1.5}
          />
        }
        title="Operational"
      >
        <table className="kv-table">
          <tbody>
            <tr>
              <td>Log level</td>
              <td>
                <span
                  className={`badge ${
                    cfg.log_level === "debug"
                      ? "badge-blue"
                      : cfg.log_level === "warn" || cfg.log_level === "error"
                        ? "badge-yellow"
                        : "badge-green"
                  }`}
                >
                  {cfg.log_level}
                </span>
              </td>
            </tr>
            <tr>
              <td>Shutdown timeout</td>
              <td>
                <span className="mono">{cfg.shutdown_timeout_s}s</span>
              </td>
            </tr>
            <tr>
              <td>Default page size</td>
              <td>
                <span className="mono">{cfg.default_page_size}</span>
              </td>
            </tr>
            <tr>
              <td>Max page size</td>
              <td>
                <span className="mono">{cfg.max_page_size}</span>
              </td>
            </tr>
          </tbody>
        </table>
      </Section>

      {/* ── Live health snapshot ──────────────────────────────────────────── */}
      <Section
        icon={
          <HugeiconsIcon icon={ServerStack01Icon} size={14} strokeWidth={1.5} />
        }
        title="Live probe results"
      >
        <table className="kv-table">
          <tbody>
            <tr>
              <td>/healthz</td>
              <td>
                {health.isLoading ? (
                  <div className="spinner" style={{ width: 14, height: 14 }} />
                ) : (
                  <BoolBadge value={health.data?.http_ok ?? false} />
                )}
              </td>
            </tr>
            <tr>
              <td>/readyz</td>
              <td>
                {health.isLoading ? (
                  <div className="spinner" style={{ width: 14, height: 14 }} />
                ) : (
                  <BoolBadge value={health.data?.ready_ok ?? false} />
                )}
              </td>
            </tr>
            <tr>
              <td>Last checked</td>
              <td>
                <span className="mono">
                  {health.data
                    ? new Date(health.data.checked_at).toLocaleTimeString()
                    : "—"}
                </span>
              </td>
            </tr>
          </tbody>
        </table>
      </Section>
    </div>
  );
}
