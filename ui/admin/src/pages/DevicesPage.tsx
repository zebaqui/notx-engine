import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Monitor,
  RefreshCw,
  AlertCircle,
  ShieldOff,
  Key,
  Clock,
  Search,
  ChevronRight,
  X,
  AlertTriangle,
  Info,
  Plus,
} from "lucide-react";
import {
  fetchDevices,
  revokeDevice,
  registerDevice,
  DeviceAPINotImplementedError,
} from "../api/client";
import type { Device } from "../api/types";

// ─── Helpers ──────────────────────────────────────────────────────────────────

function fmtDate(iso: string) {
  const d = new Date(iso);
  const year = d.getFullYear();
  const month = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  const h = String(d.getHours()).padStart(2, "0");
  const m = String(d.getMinutes()).padStart(2, "0");
  return `${year}-${month}-${day} ${h}:${m}`;
}

function timeSince(iso: string) {
  const diff = Math.round((Date.now() - new Date(iso).getTime()) / 1000);
  if (diff < 5) return "just now";
  if (diff < 60) return `${diff}s ago`;
  if (diff < 3600) return `${Math.round(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.round(diff / 3600)}h ago`;
  return `${Math.round(diff / 86400)}d ago`;
}

function truncateUrn(urn: string) {
  // notx:device:<uuid> → show last 12 chars of uuid
  const parts = urn.split(":");
  if (parts.length === 3) {
    const uuid = parts[2];
    return `${parts[0]}:${parts[1]}:…${uuid.slice(-12)}`;
  }
  return urn;
}

// ─── Register device modal ────────────────────────────────────────────────────

function RegisterDeviceModal({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const [urn, setUrn] = useState("");
  const [name, setName] = useState("");
  const [ownerUrn, setOwnerUrn] = useState("");
  const [publicKey, setPublicKey] = useState("");
  const [error, setError] = useState<string | null>(null);

  const registerMut = useMutation({
    mutationFn: () =>
      registerDevice({
        urn,
        name,
        owner_urn: ownerUrn,
        public_key_b64: publicKey || undefined,
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["devices"] });
      onClose();
    },
    onError: (err: unknown) => {
      setError(err instanceof Error ? err.message : "Registration failed");
    },
  });

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (!urn.trim() || !name.trim() || !ownerUrn.trim()) {
      setError("Device name, URN, and owner URN are required.");
      return;
    }
    registerMut.mutate();
  }

  function generateUrn() {
    setUrn(`notx:device:${crypto.randomUUID()}`);
  }

  const overlayStyle: React.CSSProperties = {
    position: "fixed",
    inset: 0,
    background: "rgba(0,0,0,0.6)",
    zIndex: 200,
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
  };

  const boxStyle: React.CSSProperties = {
    background: "var(--bg-elevated)",
    border: "1px solid var(--border-strong)",
    borderRadius: "var(--radius-lg)",
    padding: 28,
    width: 460,
    display: "flex",
    flexDirection: "column",
    gap: 18,
  };

  const fieldStyle: React.CSSProperties = {
    display: "flex",
    flexDirection: "column",
    gap: 4,
  };

  const labelStyle: React.CSSProperties = {
    fontSize: 11,
    fontWeight: 600,
    textTransform: "uppercase",
    letterSpacing: "0.6px",
    color: "var(--text-muted)",
  };

  return (
    <div
      style={overlayStyle}
      onClick={(e) => e.target === e.currentTarget && onClose()}
    >
      <div style={boxStyle}>
        {/* Header */}
        <div
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
          }}
        >
          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <Plus size={16} style={{ color: "var(--accent)", flexShrink: 0 }} />
            <span
              style={{
                fontWeight: 700,
                fontSize: 15,
                color: "var(--text-primary)",
              }}
            >
              Register device
            </span>
          </div>
          <button
            className="btn btn-ghost"
            onClick={onClose}
            style={{ padding: "4px 8px" }}
          >
            <X size={14} />
          </button>
        </div>

        <form
          onSubmit={handleSubmit}
          style={{ display: "flex", flexDirection: "column", gap: 14 }}
        >
          {/* Device name */}
          <div style={fieldStyle}>
            <label style={labelStyle}>
              Device name <span style={{ color: "var(--red)" }}>*</span>
            </label>
            <input
              className="search-input"
              style={{ width: "100%" }}
              placeholder="e.g. MacBook Pro (work)"
              value={name}
              onChange={(e) => setName(e.target.value)}
              autoFocus
            />
          </div>

          {/* Device URN */}
          <div style={fieldStyle}>
            <label style={labelStyle}>
              Device URN <span style={{ color: "var(--red)" }}>*</span>
            </label>
            <div style={{ display: "flex", gap: 6 }}>
              <input
                className="search-input"
                style={{
                  width: "100%",
                  fontFamily: "var(--font-mono)",
                  fontSize: 12,
                }}
                placeholder="notx:device:<uuid>"
                value={urn}
                onChange={(e) => setUrn(e.target.value)}
              />
              <button
                type="button"
                className="btn btn-ghost"
                onClick={generateUrn}
                style={{ whiteSpace: "nowrap", flexShrink: 0 }}
              >
                Generate
              </button>
            </div>
          </div>

          {/* Owner URN */}
          <div style={fieldStyle}>
            <label style={labelStyle}>
              Owner URN <span style={{ color: "var(--red)" }}>*</span>
            </label>
            <input
              className="search-input"
              style={{
                width: "100%",
                fontFamily: "var(--font-mono)",
                fontSize: 12,
              }}
              placeholder="notx:usr:<uuid>"
              value={ownerUrn}
              onChange={(e) => setOwnerUrn(e.target.value)}
            />
          </div>

          {/* Public key */}
          <div style={fieldStyle}>
            <label style={labelStyle}>Public key (base64) — optional</label>
            <input
              className="search-input"
              style={{
                width: "100%",
                fontFamily: "var(--font-mono)",
                fontSize: 12,
              }}
              placeholder="MCowBQYDK2VwAyEA…"
              value={publicKey}
              onChange={(e) => setPublicKey(e.target.value)}
            />
          </div>

          {/* Error */}
          {error && (
            <div
              style={{
                display: "flex",
                alignItems: "center",
                gap: 6,
                fontSize: 12,
                color: "var(--red)",
              }}
            >
              <AlertCircle size={13} style={{ flexShrink: 0 }} />
              {error}
            </div>
          )}

          {/* Actions */}
          <div
            style={{
              display: "flex",
              gap: 10,
              justifyContent: "flex-end",
              marginTop: 4,
            }}
          >
            <button
              type="button"
              className="btn btn-ghost"
              onClick={onClose}
              disabled={registerMut.isPending}
            >
              Cancel
            </button>
            <button
              type="submit"
              className="btn btn-primary"
              disabled={registerMut.isPending}
            >
              {registerMut.isPending ? (
                <div className="spinner" style={{ width: 14, height: 14 }} />
              ) : (
                <Plus size={14} />
              )}
              Register
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

// ─── Confirm revoke modal ─────────────────────────────────────────────────────

function ConfirmRevokeModal({
  device,
  onConfirm,
  onCancel,
  isPending,
}: {
  device: Device;
  onConfirm: () => void;
  onCancel: () => void;
  isPending: boolean;
}) {
  return (
    <div
      style={{
        position: "fixed",
        inset: 0,
        background: "rgba(0,0,0,0.6)",
        zIndex: 200,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
      }}
    >
      <div
        style={{
          background: "var(--bg-elevated)",
          border: "1px solid var(--border-strong)",
          borderRadius: "var(--radius-lg)",
          padding: 28,
          width: 420,
          display: "flex",
          flexDirection: "column",
          gap: 16,
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <AlertTriangle
            size={18}
            style={{ color: "var(--red)", flexShrink: 0 }}
          />
          <span
            style={{
              fontWeight: 700,
              fontSize: 15,
              color: "var(--text-primary)",
            }}
          >
            Revoke device
          </span>
        </div>

        <p
          style={{
            fontSize: 13,
            color: "var(--text-secondary)",
            lineHeight: 1.7,
          }}
        >
          Are you sure you want to revoke{" "}
          <span style={{ color: "var(--text-primary)", fontWeight: 600 }}>
            {device.name}
          </span>
          ? This device will immediately lose access and any future requests
          using its URN will be rejected. This action{" "}
          <span style={{ color: "var(--red)", fontWeight: 600 }}>
            cannot be undone
          </span>
          .
        </p>

        <div
          style={{
            background: "var(--bg-surface)",
            border: "1px solid var(--border)",
            borderRadius: "var(--radius-md)",
            padding: "10px 14px",
            fontFamily: "var(--font-mono)",
            fontSize: 11.5,
            color: "var(--text-muted)",
            wordBreak: "break-all",
          }}
        >
          {device.urn}
        </div>

        <div
          style={{
            display: "flex",
            gap: 10,
            justifyContent: "flex-end",
            marginTop: 4,
          }}
        >
          <button
            className="btn btn-ghost"
            onClick={onCancel}
            disabled={isPending}
          >
            Cancel
          </button>
          <button
            className="btn"
            style={{
              background: "var(--red-dim)",
              color: "var(--red)",
              borderColor: "var(--red)",
            }}
            onClick={onConfirm}
            disabled={isPending}
          >
            {isPending ? (
              <div className="spinner" style={{ width: 14, height: 14 }} />
            ) : (
              <ShieldOff size={14} />
            )}
            Revoke device
          </button>
        </div>
      </div>
    </div>
  );
}

// ─── Device detail panel ──────────────────────────────────────────────────────

function DeviceDetailPanel({
  device,
  onClose,
  onRevoke,
}: {
  device: Device;
  onClose: () => void;
  onRevoke: (device: Device) => void;
}) {
  return (
    <div className="drawer-overlay" onClick={onClose}>
      <div className="drawer" onClick={(e) => e.stopPropagation()}>
        <div className="drawer-header">
          <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
            <Monitor size={15} style={{ color: "var(--accent)" }} />
            <span className="drawer-title">{device.name}</span>
            {device.revoked ? (
              <span className="badge badge-red">
                <span className="badge-dot" />
                revoked
              </span>
            ) : (
              <span className="badge badge-green">
                <span className="badge-dot" />
                active
              </span>
            )}
          </div>
          <button className="close-btn" onClick={onClose}>
            <X size={16} />
          </button>
        </div>

        <div className="drawer-body">
          {/* Identity */}
          <div>
            <div className="card-title" style={{ marginBottom: 10 }}>
              Identity
            </div>
            <div className="card">
              <table className="kv-table">
                <tbody>
                  <tr>
                    <td>Device URN</td>
                    <td>
                      <span
                        className="mono"
                        style={{ fontSize: 11, wordBreak: "break-all" }}
                      >
                        {device.urn}
                      </span>
                    </td>
                  </tr>
                  <tr>
                    <td>Device name</td>
                    <td
                      style={{ color: "var(--text-primary)", fontWeight: 500 }}
                    >
                      {device.name}
                    </td>
                  </tr>
                  <tr>
                    <td>Owner URN</td>
                    <td>
                      <span
                        className="mono"
                        style={{ fontSize: 11, wordBreak: "break-all" }}
                      >
                        {device.owner_urn}
                      </span>
                    </td>
                  </tr>
                  <tr>
                    <td>Status</td>
                    <td>
                      {device.revoked ? (
                        <span className="badge badge-red">
                          <span className="badge-dot" />
                          revoked
                        </span>
                      ) : (
                        <span className="badge badge-green">
                          <span className="badge-dot" />
                          active
                        </span>
                      )}
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>

          {/* Timestamps */}
          <div>
            <div className="card-title" style={{ marginBottom: 10 }}>
              Activity
            </div>
            <div className="card">
              <table className="kv-table">
                <tbody>
                  <tr>
                    <td>Registered</td>
                    <td>
                      <span className="mono">
                        {fmtDate(device.registered_at)}
                      </span>
                    </td>
                  </tr>
                  <tr>
                    <td>Last seen</td>
                    <td>
                      {device.last_seen_at ? (
                        <span className="mono">
                          {fmtDate(device.last_seen_at)}{" "}
                          <span
                            style={{ color: "var(--text-muted)", fontSize: 11 }}
                          >
                            ({timeSince(device.last_seen_at)})
                          </span>
                        </span>
                      ) : (
                        <span style={{ color: "var(--text-muted)" }}>—</span>
                      )}
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>

          {/* Public key */}
          <div>
            <div className="card-title" style={{ marginBottom: 10 }}>
              Cryptographic key
            </div>
            <div className="card">
              <div
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: 8,
                  marginBottom: 10,
                  color: "var(--text-secondary)",
                  fontSize: 12,
                }}
              >
                <Key size={13} />
                Ed25519 public key (base64)
              </div>
              <div
                style={{
                  background: "var(--bg-elevated)",
                  border: "1px solid var(--border)",
                  borderRadius: "var(--radius-md)",
                  padding: "10px 14px",
                  fontFamily: "var(--font-mono)",
                  fontSize: 11,
                  color: "var(--text-secondary)",
                  wordBreak: "break-all",
                  lineHeight: 1.7,
                }}
              >
                {device.public_key_b64 || "—"}
              </div>
            </div>
          </div>

          {/* Danger zone */}
          {!device.revoked && (
            <div>
              <div
                className="card-title"
                style={{ marginBottom: 10, color: "var(--red)" }}
              >
                Danger zone
              </div>
              <div
                className="card"
                style={{
                  border: "1px solid var(--red)",
                  background: "var(--red-dim)",
                }}
              >
                <div
                  style={{
                    display: "flex",
                    alignItems: "center",
                    justifyContent: "space-between",
                    gap: 16,
                  }}
                >
                  <div>
                    <div
                      style={{
                        fontWeight: 600,
                        fontSize: 13,
                        color: "var(--text-primary)",
                        marginBottom: 4,
                      }}
                    >
                      Revoke this device
                    </div>
                    <div
                      style={{ fontSize: 12, color: "var(--text-secondary)" }}
                    >
                      Permanently removes device access. Cannot be undone.
                    </div>
                  </div>
                  <button
                    className="btn"
                    style={{
                      background: "transparent",
                      color: "var(--red)",
                      borderColor: "var(--red)",
                      whiteSpace: "nowrap",
                      flexShrink: 0,
                    }}
                    onClick={() => onRevoke(device)}
                  >
                    <ShieldOff size={14} />
                    Revoke
                  </button>
                </div>
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

// ─── Not-implemented banner ────────────────────────────────────────────────────

function GrpcOnlyBanner() {
  return (
    <div
      style={{
        background: "rgba(108, 143, 255, 0.08)",
        border: "1px solid var(--accent-dim)",
        borderRadius: "var(--radius-md)",
        padding: "14px 18px",
        display: "flex",
        gap: 12,
        alignItems: "flex-start",
      }}
    >
      <Info
        size={15}
        style={{ color: "var(--accent)", flexShrink: 0, marginTop: 1 }}
      />
      <div
        style={{
          fontSize: 12.5,
          color: "var(--text-secondary)",
          lineHeight: 1.7,
        }}
      >
        <span style={{ color: "var(--accent)", fontWeight: 600 }}>
          Device HTTP API not yet available.
        </span>{" "}
        Device management is currently only accessible via the gRPC{" "}
        <span className="mono">DeviceService</span> (
        <span className="mono">RegisterDevice</span>,{" "}
        <span className="mono">ListDevices</span>,{" "}
        <span className="mono">RevokeDevice</span>
        ). Add a <span className="mono">/v1/devices</span> HTTP route on the
        server to enable live data in this section.
      </div>
    </div>
  );
}

// ─── Main page ────────────────────────────────────────────────────────────────

export default function DevicesPage() {
  const qc = useQueryClient();
  const [search, setSearch] = useState("");
  const [showRevoked, setShowRevoked] = useState(false);
  const [selected, setSelected] = useState<Device | null>(null);
  const [revoking, setRevoking] = useState<Device | null>(null);
  const [showRegister, setShowRegister] = useState(false);

  const query = useQuery({
    queryKey: ["devices"],
    queryFn: () => fetchDevices({ include_revoked: true }),
    retry: false,
  });

  const isNotImplemented =
    query.isError && query.error instanceof DeviceAPINotImplementedError;

  const revokeMut = useMutation({
    mutationFn: (urn: string) => revokeDevice(urn),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["devices"] });
      setRevoking(null);
      setSelected(null);
    },
  });

  const devices: Device[] = query.data?.devices ?? [];

  // ── Demo stub — shown only when the API is not yet implemented ────────────
  const stubDevices: Device[] = isNotImplemented
    ? [
        {
          urn: "notx:device:4a5b6c7d-8e9f-0a1b-2c3d-4e5f6a7b8c9d",
          name: "MacBook Pro (work)",
          owner_urn: "notx:usr:1a2b3c4d-5e6f-7a8b-9c0d-1e2f3a4b5c6d",
          public_key_b64: "MCowBQYDK2VwAyEAyHvGkDPLFXUhH6ZpW8N+dVFkKQ==",
          registered_at: new Date(
            Date.now() - 1000 * 60 * 60 * 24 * 14,
          ).toISOString(),
          last_seen_at: new Date(Date.now() - 1000 * 60 * 8).toISOString(),
          revoked: false,
        },
        {
          urn: "notx:device:b1c2d3e4-f5a6-b7c8-d9e0-f1a2b3c4d5e6",
          name: "iPhone 15 Pro",
          owner_urn: "notx:usr:1a2b3c4d-5e6f-7a8b-9c0d-1e2f3a4b5c6d",
          public_key_b64: "MCowBQYDK2VwAyEA3KlMj/xPZ2sVm1dQeR8yLNaT7w==",
          registered_at: new Date(
            Date.now() - 1000 * 60 * 60 * 24 * 3,
          ).toISOString(),
          last_seen_at: new Date(Date.now() - 1000 * 60 * 60 * 2).toISOString(),
          revoked: false,
        },
        {
          urn: "notx:device:e7f8a9b0-c1d2-e3f4-a5b6-c7d8e9f0a1b2",
          name: "Linux desktop (home)",
          owner_urn: "notx:usr:1a2b3c4d-5e6f-7a8b-9c0d-1e2f3a4b5c6d",
          public_key_b64: "MCowBQYDK2VwAyEA9QwXmLkP4cRBzfGn2HsVaUeDj1==",
          registered_at: new Date(
            Date.now() - 1000 * 60 * 60 * 24 * 60,
          ).toISOString(),
          last_seen_at: new Date(
            Date.now() - 1000 * 60 * 60 * 24 * 5,
          ).toISOString(),
          revoked: true,
        },
      ]
    : [];

  const displayDevices = isNotImplemented ? stubDevices : devices;

  const filtered = displayDevices.filter((d) => {
    if (!showRevoked && d.revoked) return false;
    if (search.trim()) {
      const q = search.toLowerCase();
      return (
        d.name.toLowerCase().includes(q) ||
        d.urn.toLowerCase().includes(q) ||
        d.owner_urn.toLowerCase().includes(q)
      );
    }
    return true;
  });

  const activeCount = displayDevices.filter((d) => !d.revoked).length;
  const revokedCount = displayDevices.filter((d) => d.revoked).length;
  const totalCount = displayDevices.length;

  return (
    <div className="page-stack">
      {/* ── Header ──────────────────────────────────────────────────────── */}
      <div className="section-header">
        <div>
          <div className="section-title">Devices</div>
          <div className="section-sub">
            Registered devices and cryptographic identities
          </div>
        </div>
        <div style={{ display: "flex", gap: 8 }}>
          <button
            className="btn btn-primary"
            onClick={() => setShowRegister(true)}
          >
            <Plus size={14} />
            Register device
          </button>
          <button
            className="btn btn-ghost"
            onClick={() => qc.invalidateQueries({ queryKey: ["devices"] })}
            disabled={query.isFetching}
          >
            <RefreshCw
              size={14}
              className={query.isFetching ? "spin-icon" : ""}
            />
            Refresh
          </button>
        </div>
      </div>

      {/* ── API not implemented banner ───────────────────────────────────── */}
      {isNotImplemented && <GrpcOnlyBanner />}

      {/* ── Generic error banner ─────────────────────────────────────────── */}
      {query.isError && !isNotImplemented && (
        <div className="error-banner">
          <AlertCircle size={15} />
          Failed to load devices — {(query.error as Error).message}
        </div>
      )}

      {/* ── Summary tiles ───────────────────────────────────────────────── */}
      <div className="grid-3">
        <div className="stat-tile">
          <div className="stat-label">Total devices</div>
          <div className="stat-value">{totalCount}</div>
          <div className="stat-sub">all registered</div>
        </div>
        <div className="stat-tile">
          <div className="stat-label">Active</div>
          <div
            className="stat-value"
            style={{
              color: activeCount > 0 ? "var(--green)" : "var(--text-primary)",
            }}
          >
            {activeCount}
          </div>
          <div className="stat-sub">not revoked</div>
        </div>
        <div className="stat-tile">
          <div className="stat-label">Revoked</div>
          <div
            className="stat-value"
            style={{
              color: revokedCount > 0 ? "var(--yellow)" : "var(--text-primary)",
            }}
          >
            {revokedCount}
          </div>
          <div className="stat-sub">access removed</div>
        </div>
      </div>

      {/* ── Toolbar ─────────────────────────────────────────────────────── */}
      <div className="toolbar">
        <div style={{ position: "relative", flex: 1, maxWidth: 320 }}>
          <Search
            size={13}
            style={{
              position: "absolute",
              left: 10,
              top: "50%",
              transform: "translateY(-50%)",
              color: "var(--text-muted)",
              pointerEvents: "none",
            }}
          />
          <input
            className="search-input"
            style={{ paddingLeft: 32, width: "100%" }}
            placeholder="Search by name, URN or owner…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </div>

        <label
          style={{
            display: "flex",
            alignItems: "center",
            gap: 6,
            cursor: "pointer",
            fontSize: 13,
            color: "var(--text-secondary)",
            userSelect: "none",
          }}
        >
          <input
            type="checkbox"
            checked={showRevoked}
            onChange={(e) => setShowRevoked(e.target.checked)}
            style={{ accentColor: "var(--accent)", cursor: "pointer" }}
          />
          Show revoked
        </label>
      </div>

      {/* ── Table ───────────────────────────────────────────────────────── */}
      {query.isLoading ? (
        <div className="loading-center">
          <div className="spinner" />
          Loading devices…
        </div>
      ) : (
        <div
          style={{
            background: "var(--bg-surface)",
            border: "1px solid var(--border)",
            borderRadius: "var(--radius-lg)",
            overflow: "hidden",
          }}
        >
          {filtered.length === 0 ? (
            <div className="empty-state" style={{ padding: "48px 0" }}>
              <Monitor size={28} style={{ opacity: 0.3 }} />
              <div style={{ marginTop: 4 }}>
                {search.trim()
                  ? "No devices match your search."
                  : displayDevices.length === 0
                    ? "No devices registered yet."
                    : 'No active devices. Enable "Show revoked" to see all.'}
              </div>
            </div>
          ) : (
            <table className="data-table">
              <thead>
                <tr>
                  <th>Device</th>
                  <th>URN</th>
                  <th>Owner</th>
                  <th>Registered</th>
                  <th>Last seen</th>
                  <th>Status</th>
                  <th style={{ width: 36 }} />
                </tr>
              </thead>
              <tbody>
                {filtered.map((d) => (
                  <tr
                    key={d.urn}
                    style={{ cursor: "pointer", opacity: d.revoked ? 0.55 : 1 }}
                    onClick={() => setSelected(d)}
                  >
                    {/* Device name */}
                    <td>
                      <div
                        style={{
                          display: "flex",
                          alignItems: "center",
                          gap: 8,
                        }}
                      >
                        <Monitor
                          size={14}
                          style={{
                            color: d.revoked
                              ? "var(--text-muted)"
                              : "var(--accent)",
                            flexShrink: 0,
                          }}
                        />
                        <span
                          className="name-cell"
                          style={{
                            color: d.revoked
                              ? "var(--text-secondary)"
                              : "var(--text-primary)",
                          }}
                        >
                          {d.name}
                        </span>
                      </div>
                    </td>

                    {/* URN */}
                    <td className="urn-cell" title={d.urn}>
                      {truncateUrn(d.urn)}
                    </td>

                    {/* Owner URN */}
                    <td className="urn-cell" title={d.owner_urn}>
                      {truncateUrn(d.owner_urn)}
                    </td>

                    {/* Registered */}
                    <td>
                      <div
                        style={{
                          display: "flex",
                          alignItems: "center",
                          gap: 5,
                          whiteSpace: "nowrap",
                          fontSize: 12,
                        }}
                      >
                        <Clock size={11} style={{ opacity: 0.5 }} />
                        {fmtDate(d.registered_at)}
                      </div>
                    </td>

                    {/* Last seen */}
                    <td style={{ whiteSpace: "nowrap", fontSize: 12 }}>
                      {d.last_seen_at ? timeSince(d.last_seen_at) : "—"}
                    </td>

                    {/* Status badge */}
                    <td>
                      {d.revoked ? (
                        <span className="badge badge-red">
                          <span className="badge-dot" />
                          revoked
                        </span>
                      ) : (
                        <span className="badge badge-green">
                          <span className="badge-dot" />
                          active
                        </span>
                      )}
                    </td>

                    {/* Arrow */}
                    <td style={{ padding: "0 12px 0 0" }}>
                      <ChevronRight
                        size={14}
                        style={{ color: "var(--text-muted)" }}
                      />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}

      {/* ── gRPC reference card ──────────────────────────────────────────── */}
      <div className="card" style={{ borderColor: "var(--border)" }}>
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 8,
            marginBottom: 14,
            color: "var(--text-secondary)",
          }}
        >
          <Key size={14} />
          <span className="card-title" style={{ marginBottom: 0 }}>
            gRPC DeviceService reference
          </span>
        </div>
        <table className="kv-table">
          <tbody>
            <tr>
              <td>RegisterDevice</td>
              <td>
                <span className="mono">
                  Registers a device + Ed25519 public key
                </span>
              </td>
            </tr>
            <tr>
              <td>ListDevices</td>
              <td>
                <span className="mono">Lists all devices for an owner URN</span>
              </td>
            </tr>
            <tr>
              <td>GetDevicePublicKey</td>
              <td>
                <span className="mono">
                  Retrieves a device's public key for CEK wrapping
                </span>
              </td>
            </tr>
            <tr>
              <td>RevokeDevice</td>
              <td>
                <span className="mono">
                  Permanently removes a device registration
                </span>
              </td>
            </tr>
            <tr>
              <td>InitiatePairing</td>
              <td>
                <span className="mono">Starts a device pairing handshake</span>
              </td>
            </tr>
            <tr>
              <td>CompletePairing</td>
              <td>
                <span className="mono">Finalises the pairing handshake</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      {/* ── Detail drawer ────────────────────────────────────────────────── */}
      {selected && (
        <DeviceDetailPanel
          device={selected}
          onClose={() => setSelected(null)}
          onRevoke={(d) => {
            setRevoking(d);
            setSelected(null);
          }}
        />
      )}

      {/* ── Revoke confirm modal ──────────────────────────────────────────── */}
      {revoking && (
        <ConfirmRevokeModal
          device={revoking}
          onConfirm={() => revokeMut.mutate(revoking.urn)}
          onCancel={() => setRevoking(null)}
          isPending={revokeMut.isPending}
        />
      )}

      {/* ── Register device modal ─────────────────────────────────────────── */}
      {showRegister && (
        <RegisterDeviceModal onClose={() => setShowRegister(false)} />
      )}
    </div>
  );
}
