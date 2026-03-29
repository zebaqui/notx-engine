import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Monitor,
  RefreshCw,
  AlertCircle,
  ShieldOff,
  ShieldCheck,
  ShieldX,
  Key,
  Clock,
  Search,
  ChevronRight,
  X,
  Plus,
  Hourglass,
} from "lucide-react";
import {
  fetchDevices,
  revokeDevice,
  registerDevice,
  approveDevice,
  rejectDevice,
} from "../api/client";
import type { Device, DeviceApprovalStatus } from "../api/types";

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
  const parts = urn.split(":");
  if (parts.length === 3) {
    const uuid = parts[2];
    return `${parts[0]}:${parts[1]}:…${uuid.slice(-12)}`;
  }
  return urn;
}

// ─── Status badge ─────────────────────────────────────────────────────────────

function ApprovalBadge({
  device,
  size = "normal",
}: {
  device: Device;
  size?: "normal" | "small";
}) {
  const dotSize = size === "small" ? 6 : 7;
  const fontSize = size === "small" ? 10 : undefined;

  if (device.revoked) {
    return (
      <span className="badge badge-red" style={{ fontSize }}>
        <span
          className="badge-dot"
          style={{ width: dotSize, height: dotSize }}
        />
        revoked
      </span>
    );
  }
  switch (device.approval_status) {
    case "approved":
      return (
        <span className="badge badge-green" style={{ fontSize }}>
          <span
            className="badge-dot"
            style={{ width: dotSize, height: dotSize }}
          />
          approved
        </span>
      );
    case "pending":
      return (
        <span
          className="badge"
          style={{
            fontSize,
            background: "var(--yellow-dim, rgba(234,179,8,0.12))",
            color: "var(--yellow, #ca8a04)",
            border: "1px solid var(--yellow, #ca8a04)",
          }}
        >
          <Hourglass size={dotSize} style={{ flexShrink: 0 }} />
          pending
        </span>
      );
    case "rejected":
      return (
        <span
          className="badge"
          style={{
            fontSize,
            background: "var(--orange-dim, rgba(234,88,12,0.12))",
            color: "var(--orange, #ea580c)",
            border: "1px solid var(--orange, #ea580c)",
          }}
        >
          <span
            className="badge-dot"
            style={{ width: dotSize, height: dotSize }}
          />
          rejected
        </span>
      );
    default:
      return (
        <span className="badge" style={{ fontSize }}>
          <span
            className="badge-dot"
            style={{ width: dotSize, height: dotSize }}
          />
          {device.approval_status ?? "unknown"}
        </span>
      );
  }
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
    width: 440,
    display: "flex",
    flexDirection: "column",
    gap: 16,
  };

  return (
    <div
      style={overlayStyle}
      onClick={(e) => e.target === e.currentTarget && onCancel()}
    >
      <div style={boxStyle}>
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <ShieldOff size={16} style={{ color: "var(--red)", flexShrink: 0 }} />
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
            lineHeight: 1.6,
            margin: 0,
          }}
        >
          This will permanently revoke{" "}
          <span style={{ color: "var(--text-primary)", fontWeight: 600 }}>
            {device.name}
          </span>
          . The device will immediately lose all data access and{" "}
          <span style={{ color: "var(--red)", fontWeight: 600 }}>
            this cannot be undone
          </span>
          .
        </p>

        <div
          style={{
            background: "var(--bg-surface)",
            border: "1px solid var(--border)",
            borderRadius: "var(--radius-md)",
            padding: "8px 12px",
            fontFamily: "var(--font-mono)",
            fontSize: 11,
            color: "var(--text-secondary)",
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
              background: "transparent",
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
            Revoke
          </button>
        </div>
      </div>
    </div>
  );
}

// ─── Confirm reject modal ─────────────────────────────────────────────────────

function ConfirmRejectModal({
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
    width: 440,
    display: "flex",
    flexDirection: "column",
    gap: 16,
  };

  return (
    <div
      style={overlayStyle}
      onClick={(e) => e.target === e.currentTarget && onCancel()}
    >
      <div style={boxStyle}>
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <ShieldX
            size={16}
            style={{ color: "var(--orange, #ea580c)", flexShrink: 0 }}
          />
          <span
            style={{
              fontWeight: 700,
              fontSize: 15,
              color: "var(--text-primary)",
            }}
          >
            Reject device
          </span>
        </div>

        <p
          style={{
            fontSize: 13,
            color: "var(--text-secondary)",
            lineHeight: 1.6,
            margin: 0,
          }}
        >
          Rejecting{" "}
          <span style={{ color: "var(--text-primary)", fontWeight: 600 }}>
            {device.name}
          </span>{" "}
          will permanently bar it from data access. The device must re-register
          with a new URN to retry onboarding.
        </p>

        <div
          style={{
            background: "var(--bg-surface)",
            border: "1px solid var(--border)",
            borderRadius: "var(--radius-md)",
            padding: "8px 12px",
            fontFamily: "var(--font-mono)",
            fontSize: 11,
            color: "var(--text-secondary)",
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
              background: "transparent",
              color: "var(--orange, #ea580c)",
              borderColor: "var(--orange, #ea580c)",
            }}
            onClick={onConfirm}
            disabled={isPending}
          >
            {isPending ? (
              <div className="spinner" style={{ width: 14, height: 14 }} />
            ) : (
              <ShieldX size={14} />
            )}
            Reject
          </button>
        </div>
      </div>
    </div>
  );
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

// ─── Device detail panel ──────────────────────────────────────────────────────

function DeviceDetailPanel({
  device,
  onClose,
  onRevoke,
  onApprove,
  onReject,
}: {
  device: Device;
  onClose: () => void;
  onRevoke: (device: Device) => void;
  onApprove: (device: Device) => void;
  onReject: (device: Device) => void;
}) {
  const isPending = device.approval_status === "pending" && !device.revoked;
  const isApproved = device.approval_status === "approved" && !device.revoked;
  const isRejected = device.approval_status === "rejected" && !device.revoked;

  return (
    <div className="drawer-overlay" onClick={onClose}>
      <div className="drawer" onClick={(e) => e.stopPropagation()}>
        <div className="drawer-header">
          <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
            <Monitor size={15} style={{ color: "var(--accent)" }} />
            <span className="drawer-title">{device.name}</span>
            <ApprovalBadge device={device} />
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
                    <td>Approval status</td>
                    <td>
                      <ApprovalBadge device={device} size="small" />
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>

          {/* Activity */}
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

          {/* Onboarding actions — shown for pending devices */}
          {isPending && (
            <div>
              <div className="card-title" style={{ marginBottom: 10 }}>
                Onboarding approval
              </div>
              <div
                className="card"
                style={{
                  border: "1px solid var(--yellow, #ca8a04)",
                  background: "var(--yellow-dim, rgba(234,179,8,0.06))",
                }}
              >
                <div
                  style={{
                    fontSize: 13,
                    color: "var(--text-secondary)",
                    marginBottom: 14,
                    lineHeight: 1.6,
                  }}
                >
                  This device is awaiting administrator approval before it can
                  access any data. Approve it to grant access or reject it to
                  permanently bar this device URN.
                </div>
                <div style={{ display: "flex", gap: 10 }}>
                  <button
                    className="btn btn-primary"
                    style={{ flex: 1 }}
                    onClick={() => onApprove(device)}
                  >
                    <ShieldCheck size={14} />
                    Approve access
                  </button>
                  <button
                    className="btn"
                    style={{
                      flex: 1,
                      background: "transparent",
                      color: "var(--orange, #ea580c)",
                      borderColor: "var(--orange, #ea580c)",
                    }}
                    onClick={() => onReject(device)}
                  >
                    <ShieldX size={14} />
                    Reject
                  </button>
                </div>
              </div>
            </div>
          )}

          {/* Re-approve action — shown for rejected devices */}
          {isRejected && (
            <div>
              <div className="card-title" style={{ marginBottom: 10 }}>
                Onboarding status
              </div>
              <div
                className="card"
                style={{
                  border: "1px solid var(--orange, #ea580c)",
                  background: "var(--orange-dim, rgba(234,88,12,0.06))",
                }}
              >
                <div
                  style={{
                    fontSize: 13,
                    color: "var(--text-secondary)",
                    lineHeight: 1.6,
                  }}
                >
                  This device has been rejected. It cannot access any data. The
                  device must re-register with a new URN to retry onboarding.
                </div>
              </div>
            </div>
          )}

          {/* Danger zone — shown for non-revoked devices */}
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
                {isApproved && (
                  <div
                    style={{
                      display: "flex",
                      alignItems: "center",
                      justifyContent: "space-between",
                      gap: 16,
                      marginBottom: 12,
                      paddingBottom: 12,
                      borderBottom: "1px solid var(--border)",
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
                        Reject this device
                      </div>
                      <div
                        style={{ fontSize: 12, color: "var(--text-secondary)" }}
                      >
                        Removes data access. Device must re-register to retry.
                      </div>
                    </div>
                    <button
                      className="btn"
                      style={{
                        background: "transparent",
                        color: "var(--orange, #ea580c)",
                        borderColor: "var(--orange, #ea580c)",
                        whiteSpace: "nowrap",
                        flexShrink: 0,
                      }}
                      onClick={() => onReject(device)}
                    >
                      <ShieldX size={14} />
                      Reject
                    </button>
                  </div>
                )}

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

// ─── Status filter type ───────────────────────────────────────────────────────

type StatusFilter = "all" | DeviceApprovalStatus | "revoked";

// ─── Main page ────────────────────────────────────────────────────────────────

export default function DevicesPage() {
  const qc = useQueryClient();
  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [selected, setSelected] = useState<Device | null>(null);
  const [revoking, setRevoking] = useState<Device | null>(null);
  const [rejecting, setRejecting] = useState<Device | null>(null);
  const [showRegister, setShowRegister] = useState(false);

  const query = useQuery({
    queryKey: ["devices"],
    queryFn: () => fetchDevices({ include_revoked: true }),
    retry: false,
  });

  // ── Mutations ────────────────────────────────────────────────────────────────

  const revokeMut = useMutation({
    mutationFn: (urn: string) => revokeDevice(urn),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["devices"] });
      setRevoking(null);
      setSelected(null);
    },
  });

  const approveMut = useMutation({
    mutationFn: (urn: string) => approveDevice(urn),
    onSuccess: (updated) => {
      qc.invalidateQueries({ queryKey: ["devices"] });
      // Keep the detail panel open but show the refreshed device
      setSelected(updated);
    },
  });

  const rejectMut = useMutation({
    mutationFn: (urn: string) => rejectDevice(urn),
    onSuccess: (updated) => {
      qc.invalidateQueries({ queryKey: ["devices"] });
      setRejecting(null);
      setSelected(updated);
    },
  });

  // ── Derived data ──────────────────────────────────────────────────────────────

  const devices: Device[] = query.data?.devices ?? [];

  const pendingCount = devices.filter(
    (d) => d.approval_status === "pending" && !d.revoked,
  ).length;
  const approvedCount = devices.filter(
    (d) => d.approval_status === "approved" && !d.revoked,
  ).length;
  const rejectedCount = devices.filter(
    (d) => d.approval_status === "rejected" && !d.revoked,
  ).length;
  const revokedCount = devices.filter((d) => d.revoked).length;
  const totalCount = devices.length;

  const filtered = devices.filter((d) => {
    // Status filter
    if (statusFilter === "revoked" && !d.revoked) return false;
    if (
      statusFilter === "pending" &&
      (d.approval_status !== "pending" || d.revoked)
    )
      return false;
    if (
      statusFilter === "approved" &&
      (d.approval_status !== "approved" || d.revoked)
    )
      return false;
    if (
      statusFilter === "rejected" &&
      (d.approval_status !== "rejected" || d.revoked)
    )
      return false;

    // Search filter
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

  // ── Filter pill helper ────────────────────────────────────────────────────────

  function FilterPill({
    value,
    label,
    count,
    warn,
  }: {
    value: StatusFilter;
    label: string;
    count?: number;
    warn?: boolean;
  }) {
    const active = statusFilter === value;
    return (
      <button
        className={`btn ${active ? "btn-primary" : "btn-ghost"}`}
        style={{
          fontSize: 12,
          padding: "4px 12px",
          ...(warn && !active
            ? {
                color: "var(--yellow, #ca8a04)",
                borderColor: "var(--yellow, #ca8a04)",
              }
            : {}),
        }}
        onClick={() => setStatusFilter(value)}
      >
        {label}
        {count !== undefined && (
          <span
            style={{
              marginLeft: 5,
              background: active
                ? "rgba(255,255,255,0.2)"
                : "var(--bg-elevated)",
              border: "1px solid var(--border)",
              borderRadius: 99,
              padding: "0 6px",
              fontSize: 10,
              fontWeight: 700,
              color: warn && !active ? "var(--yellow, #ca8a04)" : undefined,
            }}
          >
            {count}
          </span>
        )}
      </button>
    );
  }

  return (
    <div className="page-stack">
      {/* ── Header ──────────────────────────────────────────────────────── */}
      <div className="section-header">
        <div>
          <div className="section-title">Devices</div>
          <div className="section-sub">
            Registered devices and onboarding approvals
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

      {/* ── Error banner ─────────────────────────────────────────────────── */}
      {query.isError && (
        <div className="error-banner">
          <AlertCircle size={15} />
          Failed to load devices — {(query.error as Error).message}
        </div>
      )}

      {/* ── Pending approval callout ─────────────────────────────────────── */}
      {pendingCount > 0 && (
        <div
          style={{
            background: "var(--yellow-dim, rgba(234,179,8,0.08))",
            border: "1px solid var(--yellow, #ca8a04)",
            borderRadius: "var(--radius-lg)",
            padding: "12px 16px",
            display: "flex",
            alignItems: "center",
            gap: 12,
          }}
        >
          <Hourglass
            size={16}
            style={{ color: "var(--yellow, #ca8a04)", flexShrink: 0 }}
          />
          <div style={{ flex: 1 }}>
            <span
              style={{
                fontWeight: 600,
                color: "var(--text-primary)",
                fontSize: 13,
              }}
            >
              {pendingCount} device{pendingCount !== 1 ? "s" : ""} awaiting
              approval
            </span>
            <span
              style={{
                color: "var(--text-secondary)",
                fontSize: 12,
                marginLeft: 8,
              }}
            >
              These devices have registered but cannot access data until
              approved.
            </span>
          </div>
          <button
            className="btn"
            style={{
              fontSize: 12,
              padding: "4px 12px",
              color: "var(--yellow, #ca8a04)",
              borderColor: "var(--yellow, #ca8a04)",
              background: "transparent",
              flexShrink: 0,
            }}
            onClick={() => setStatusFilter("pending")}
          >
            Review
          </button>
        </div>
      )}

      {/* ── Summary tiles ───────────────────────────────────────────────── */}
      <div className="grid-4">
        <div className="stat-tile">
          <div className="stat-label">Total</div>
          <div className="stat-value">{totalCount}</div>
          <div className="stat-sub">all registered</div>
        </div>
        <div className="stat-tile">
          <div className="stat-label">Approved</div>
          <div
            className="stat-value"
            style={{
              color: approvedCount > 0 ? "var(--green)" : "var(--text-primary)",
            }}
          >
            {approvedCount}
          </div>
          <div className="stat-sub">active access</div>
        </div>
        <div className="stat-tile">
          <div className="stat-label">Pending</div>
          <div
            className="stat-value"
            style={{
              color:
                pendingCount > 0
                  ? "var(--yellow, #ca8a04)"
                  : "var(--text-primary)",
            }}
          >
            {pendingCount}
          </div>
          <div className="stat-sub">awaiting review</div>
        </div>
        <div className="stat-tile">
          <div className="stat-label">Revoked / Rejected</div>
          <div
            className="stat-value"
            style={{
              color:
                revokedCount + rejectedCount > 0
                  ? "var(--red)"
                  : "var(--text-primary)",
            }}
          >
            {revokedCount + rejectedCount}
          </div>
          <div className="stat-sub">access removed</div>
        </div>
      </div>

      {/* ── Toolbar ─────────────────────────────────────────────────────── */}
      <div className="toolbar" style={{ flexWrap: "wrap", gap: 10 }}>
        {/* Search */}
        <div
          style={{
            position: "relative",
            flex: 1,
            minWidth: 200,
            maxWidth: 320,
          }}
        >
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

        {/* Status filter pills */}
        <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
          <FilterPill value="all" label="All" count={totalCount} />
          <FilterPill
            value="pending"
            label="Pending"
            count={pendingCount}
            warn={pendingCount > 0}
          />
          <FilterPill value="approved" label="Approved" count={approvedCount} />
          <FilterPill value="rejected" label="Rejected" count={rejectedCount} />
          <FilterPill value="revoked" label="Revoked" count={revokedCount} />
        </div>
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
                  : totalCount === 0
                    ? "No devices registered yet."
                    : "No devices match the selected filter."}
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
                  <th>Actions</th>
                  <th style={{ width: 36 }} />
                </tr>
              </thead>
              <tbody>
                {filtered.map((d) => {
                  const isPendingRow =
                    d.approval_status === "pending" && !d.revoked;
                  return (
                    <tr
                      key={d.urn}
                      style={{
                        cursor: "pointer",
                        opacity:
                          d.revoked || d.approval_status === "rejected"
                            ? 0.55
                            : 1,
                        background: isPendingRow
                          ? "var(--yellow-dim, rgba(234,179,8,0.04))"
                          : undefined,
                      }}
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
                              color:
                                d.revoked || d.approval_status === "rejected"
                                  ? "var(--text-muted)"
                                  : isPendingRow
                                    ? "var(--yellow, #ca8a04)"
                                    : "var(--accent)",
                              flexShrink: 0,
                            }}
                          />
                          <span
                            className="name-cell"
                            style={{
                              color:
                                d.revoked || d.approval_status === "rejected"
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
                        <ApprovalBadge device={d} size="small" />
                      </td>

                      {/* Inline quick-actions for pending */}
                      <td onClick={(e) => e.stopPropagation()}>
                        {isPendingRow ? (
                          <div style={{ display: "flex", gap: 6 }}>
                            <button
                              className="btn btn-primary"
                              style={{ fontSize: 11, padding: "3px 10px" }}
                              onClick={() => approveMut.mutate(d.urn)}
                              disabled={approveMut.isPending}
                              title="Approve this device"
                            >
                              {approveMut.isPending &&
                              approveMut.variables === d.urn ? (
                                <div
                                  className="spinner"
                                  style={{ width: 11, height: 11 }}
                                />
                              ) : (
                                <ShieldCheck size={12} />
                              )}
                              Approve
                            </button>
                            <button
                              className="btn"
                              style={{
                                fontSize: 11,
                                padding: "3px 10px",
                                background: "transparent",
                                color: "var(--orange, #ea580c)",
                                borderColor: "var(--orange, #ea580c)",
                              }}
                              onClick={() => setRejecting(d)}
                              disabled={rejectMut.isPending}
                              title="Reject this device"
                            >
                              <ShieldX size={12} />
                              Reject
                            </button>
                          </div>
                        ) : (
                          <span
                            style={{ color: "var(--text-muted)", fontSize: 12 }}
                          >
                            —
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
                  );
                })}
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
          onApprove={(d) => approveMut.mutate(d.urn)}
          onReject={(d) => {
            setRejecting(d);
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

      {/* ── Reject confirm modal ──────────────────────────────────────────── */}
      {rejecting && (
        <ConfirmRejectModal
          device={rejecting}
          onConfirm={() => rejectMut.mutate(rejecting.urn)}
          onCancel={() => setRejecting(null)}
          isPending={rejectMut.isPending}
        />
      )}

      {/* ── Register device modal ─────────────────────────────────────────── */}
      {showRegister && (
        <RegisterDeviceModal onClose={() => setShowRegister(false)} />
      )}
    </div>
  );
}
