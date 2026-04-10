import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { HugeiconsIcon } from "@hugeicons/react";
import {
  ServerStack01Icon,
  Refresh01Icon,
  AlertCircleIcon,
  Link01Icon,
  ShieldUserIcon,
  Delete01Icon,
  Copy01Icon,
  CheckmarkCircle01Icon,
  LockIcon,
  Clock01Icon,
} from "@hugeicons/core-free-icons";
import {
  fetchServers,
  revokeServer,
  fetchCACertificate,
  createPairingSecret,
  pairWithServer,
} from "../api/client";
import type { ServerInfo, PairingSecret } from "../api/types";
import axios from "axios";

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

function isPairingDisabled(err: unknown): boolean {
  if (axios.isAxiosError(err) && err.response?.status === 503) return true;
  return false;
}

// ─── Status badge ─────────────────────────────────────────────────────────────

function StatusBadge({ revoked }: { revoked: boolean }) {
  if (revoked) {
    return (
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
          color: "var(--red, #ef4444)",
          border: "1px solid rgba(239,68,68,0.25)",
          whiteSpace: "nowrap",
        }}
      >
        Revoked
      </span>
    );
  }
  return (
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
        color: "var(--green, #22c55e)",
        border: "1px solid rgba(34,197,94,0.25)",
        whiteSpace: "nowrap",
      }}
    >
      Active
    </span>
  );
}

// ─── Confirm revoke modal ─────────────────────────────────────────────────────

function ConfirmRevokeModal({
  server,
  onConfirm,
  onCancel,
  isPending,
}: {
  server: ServerInfo;
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
          <HugeiconsIcon
            icon={Delete01Icon}
            size={16}
            strokeWidth={1.5}
            style={{ color: "var(--red)", flexShrink: 0 }}
          />
          <span
            style={{
              fontWeight: 700,
              fontSize: 15,
              color: "var(--text-primary)",
            }}
          >
            Revoke peer server
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
            {server.name}
          </span>
          . The server will immediately lose all pairing access and{" "}
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
          {server.urn}
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
              <HugeiconsIcon icon={Delete01Icon} size={14} strokeWidth={1.5} />
            )}
            Revoke
          </button>
        </div>
      </div>
    </div>
  );
}

// ─── Generate pairing secret modal ───────────────────────────────────────────

function GenerateSecretModal({ onClose }: { onClose: () => void }) {
  const [label, setLabel] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [created, setCreated] = useState<PairingSecret | null>(null);
  const [copied, setCopied] = useState(false);

  const createMut = useMutation({
    mutationFn: () => createPairingSecret({ label: label.trim() || undefined }),
    onSuccess: (secret) => {
      setCreated(secret);
      setError(null);
    },
    onError: (err: unknown) => {
      if (axios.isAxiosError(err)) {
        setError(err.response?.data?.message ?? err.message);
      } else {
        setError("Failed to generate pairing secret.");
      }
    },
  });

  function handleCopy() {
    if (!created) return;
    navigator.clipboard.writeText(created.plaintext).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
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
    width: 480,
    display: "flex",
    flexDirection: "column",
    gap: 18,
  };
  const fieldStyle: React.CSSProperties = {
    display: "flex",
    flexDirection: "column",
    gap: 6,
  };
  const labelStyle: React.CSSProperties = {
    fontSize: 11,
    fontWeight: 600,
    textTransform: "uppercase",
    letterSpacing: "0.06em",
    color: "var(--text-muted)",
  };

  return (
    <div
      style={overlayStyle}
      onClick={(e) => e.target === e.currentTarget && onClose()}
    >
      <div style={boxStyle}>
        {/* Header */}
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <HugeiconsIcon
            icon={LockIcon}
            size={16}
            strokeWidth={1.5}
            style={{ color: "var(--accent)", flexShrink: 0 }}
          />
          <span
            style={{
              fontWeight: 700,
              fontSize: 15,
              color: "var(--text-primary)",
              flex: 1,
            }}
          >
            Generate pairing secret
          </span>
          <button
            className="btn btn-ghost"
            style={{ padding: "2px 8px", fontSize: 12 }}
            onClick={onClose}
          >
            Close
          </button>
        </div>

        {/* Error */}
        {error && (
          <div
            className="error-banner"
            style={{ display: "flex", alignItems: "center", gap: 8 }}
          >
            <HugeiconsIcon
              icon={AlertCircleIcon}
              size={14}
              strokeWidth={1.5}
              style={{ flexShrink: 0 }}
            />
            {error}
          </div>
        )}

        {/* Label input — only shown before creation */}
        {!created && (
          <>
            <div style={fieldStyle}>
              <label style={labelStyle}>Label (optional)</label>
              <input
                className="search-input"
                style={{ width: "100%" }}
                placeholder="e.g. prod-replica-1"
                value={label}
                onChange={(e) => setLabel(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && !createMut.isPending)
                    createMut.mutate();
                }}
                autoFocus
              />
              <span
                style={{
                  fontSize: 11,
                  color: "var(--text-muted)",
                  marginTop: 2,
                }}
              >
                A short human-readable name to identify where this secret will
                be used.
              </span>
            </div>

            <div
              style={{ display: "flex", gap: 10, justifyContent: "flex-end" }}
            >
              <button
                className="btn btn-ghost"
                onClick={onClose}
                disabled={createMut.isPending}
              >
                Cancel
              </button>
              <button
                className="btn btn-primary"
                onClick={() => createMut.mutate()}
                disabled={createMut.isPending}
              >
                {createMut.isPending ? (
                  <div className="spinner" style={{ width: 14, height: 14 }} />
                ) : (
                  <HugeiconsIcon icon={LockIcon} size={14} strokeWidth={1.5} />
                )}
                Generate
              </button>
            </div>
          </>
        )}

        {/* One-time secret display */}
        {created && (
          <>
            {/* Warning banner */}
            <div
              style={{
                background: "rgba(234,179,8,0.08)",
                border: "1px solid var(--yellow, #ca8a04)",
                borderRadius: "var(--radius-md)",
                padding: "10px 14px",
                display: "flex",
                alignItems: "flex-start",
                gap: 10,
              }}
            >
              <HugeiconsIcon
                icon={AlertCircleIcon}
                size={15}
                strokeWidth={1.5}
                style={{
                  color: "var(--yellow, #ca8a04)",
                  flexShrink: 0,
                  marginTop: 1,
                }}
              />
              <span
                style={{
                  fontSize: 13,
                  color: "var(--yellow, #ca8a04)",
                  lineHeight: 1.5,
                  fontWeight: 600,
                }}
              >
                This secret is shown only once. Copy it now — you will not be
                able to retrieve it again.
              </span>
            </div>

            {/* Secret metadata */}
            {created.label && (
              <div style={fieldStyle}>
                <span style={labelStyle}>Label</span>
                <span style={{ fontSize: 13, color: "var(--text-primary)" }}>
                  {created.label}
                </span>
              </div>
            )}

            <div style={fieldStyle}>
              <span style={labelStyle}>Expires</span>
              <span style={{ fontSize: 13, color: "var(--text-secondary)" }}>
                {fmtDate(created.expires_at)}
              </span>
            </div>

            {/* Plaintext secret box */}
            <div style={fieldStyle}>
              <span style={labelStyle}>Secret</span>
              <div
                style={{
                  position: "relative",
                  display: "flex",
                  alignItems: "stretch",
                  gap: 0,
                  background: "var(--bg-surface)",
                  border: "1px solid var(--border-strong)",
                  borderRadius: "var(--radius-md)",
                  overflow: "hidden",
                }}
              >
                <code
                  style={{
                    flex: 1,
                    fontFamily: "var(--font-mono)",
                    fontSize: 12,
                    padding: "10px 14px",
                    color: "var(--green, #22c55e)",
                    wordBreak: "break-all",
                    lineHeight: 1.6,
                    userSelect: "all",
                  }}
                >
                  {created.plaintext}
                </code>
                <button
                  onClick={handleCopy}
                  style={{
                    flexShrink: 0,
                    alignSelf: "stretch",
                    padding: "0 14px",
                    background: copied
                      ? "rgba(34,197,94,0.12)"
                      : "var(--bg-elevated)",
                    border: "none",
                    borderLeft: "1px solid var(--border)",
                    cursor: "pointer",
                    color: copied
                      ? "var(--green, #22c55e)"
                      : "var(--text-muted)",
                    display: "flex",
                    alignItems: "center",
                    gap: 6,
                    fontSize: 12,
                    transition: "background 0.15s, color 0.15s",
                  }}
                  title="Copy secret to clipboard"
                >
                  {copied ? (
                    <HugeiconsIcon
                      icon={CheckmarkCircle01Icon}
                      size={14}
                      strokeWidth={1.5}
                    />
                  ) : (
                    <HugeiconsIcon
                      icon={Copy01Icon}
                      size={14}
                      strokeWidth={1.5}
                    />
                  )}
                  {copied ? "Copied!" : "Copy"}
                </button>
              </div>
            </div>

            <div style={{ display: "flex", justifyContent: "flex-end" }}>
              <button className="btn btn-primary" onClick={onClose}>
                Done
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}

// ─── CA Certificate panel ─────────────────────────────────────────────────────

// ─── PairWithServerModal ──────────────────────────────────────────────────────

function PairWithServerModal({ onClose }: { onClose: () => void }) {
  const [url, setUrl] = useState("");
  const [secret, setSecret] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<{
    server_urn: string;
    expires_at: string;
  } | null>(null);
  const qc = useQueryClient();

  const pairMut = useMutation({
    mutationFn: () =>
      pairWithServer({ url: url.trim(), secret: secret.trim() }),
    onSuccess: (data) => {
      setSuccess({ server_urn: data.server_urn, expires_at: data.expires_at });
      setError(null);
      qc.invalidateQueries({ queryKey: ["servers"] });
    },
    onError: (err: unknown) => {
      if (axios.isAxiosError(err)) {
        setError(err.response?.data?.error ?? err.message);
      } else {
        setError(String(err));
      }
    },
  });

  const overlayStyle: React.CSSProperties = {
    position: "fixed",
    inset: 0,
    background: "rgba(0,0,0,0.55)",
    zIndex: 50,
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
  };

  const boxStyle: React.CSSProperties = {
    background: "var(--bg-surface)",
    border: "1px solid var(--border)",
    borderRadius: "var(--radius-lg)",
    padding: "28px 28px 24px",
    width: 440,
    display: "flex",
    flexDirection: "column",
    gap: 18,
  };

  const fieldStyle: React.CSSProperties = {
    display: "flex",
    flexDirection: "column",
    gap: 6,
  };

  const labelStyle: React.CSSProperties = {
    fontSize: 11,
    fontWeight: 600,
    textTransform: "uppercase",
    letterSpacing: "0.06em",
    color: "var(--text-muted)",
  };

  return (
    <div style={overlayStyle} onClick={onClose}>
      <div style={boxStyle} onClick={(e) => e.stopPropagation()}>
        {/* Header */}
        <div>
          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <HugeiconsIcon
              icon={Link01Icon}
              size={16}
              strokeWidth={1.5}
              style={{ color: "var(--accent)", flexShrink: 0 }}
            />
            <span
              style={{
                fontWeight: 600,
                fontSize: 15,
                color: "var(--text-primary)",
              }}
            >
              Pair with a Server
            </span>
          </div>
          <div
            style={{
              fontSize: 13,
              color: "var(--text-muted)",
              marginTop: 6,
              lineHeight: 1.5,
            }}
          >
            Enter the remote authority's bootstrap address and the pairing
            secret it generated. This server will register itself with the
            remote authority.
          </div>
        </div>

        {/* Success state */}
        {success ? (
          <div>
            <div
              style={{
                background: "rgba(34,197,94,0.08)",
                border: "1px solid rgba(34,197,94,0.3)",
                borderRadius: "var(--radius)",
                padding: "14px 16px",
                display: "flex",
                alignItems: "flex-start",
                gap: 10,
              }}
            >
              <HugeiconsIcon
                icon={CheckmarkCircle01Icon}
                size={16}
                strokeWidth={1.5}
                style={{ color: "#22c55e", flexShrink: 0, marginTop: 1 }}
              />
              <div>
                <div
                  style={{
                    fontWeight: 600,
                    fontSize: 13,
                    color: "var(--text-primary)",
                    marginBottom: 4,
                  }}
                >
                  Successfully paired!
                </div>
                <div
                  style={{
                    fontSize: 12,
                    color: "var(--text-secondary)",
                    lineHeight: 1.5,
                  }}
                >
                  <span style={{ color: "var(--text-muted)" }}>URN: </span>
                  <span style={{ fontFamily: "var(--font-mono)" }}>
                    {success.server_urn}
                  </span>
                </div>
                <div
                  style={{
                    fontSize: 12,
                    color: "var(--text-secondary)",
                    marginTop: 2,
                  }}
                >
                  <span style={{ color: "var(--text-muted)" }}>
                    Cert expires:{" "}
                  </span>
                  {new Date(success.expires_at).toLocaleDateString()}
                </div>
              </div>
            </div>
            <div
              style={{
                display: "flex",
                justifyContent: "flex-end",
                marginTop: 16,
              }}
            >
              <button className="btn btn-primary" onClick={onClose}>
                Done
              </button>
            </div>
          </div>
        ) : (
          <>
            {/* URL field */}
            <div style={fieldStyle}>
              <label style={labelStyle}>Authority URL</label>
              <input
                className="input"
                type="text"
                placeholder="remote-host:50052"
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                disabled={pairMut.isPending}
                style={{ fontFamily: "var(--font-mono)", fontSize: 13 }}
              />
              <div style={{ fontSize: 11, color: "var(--text-muted)" }}>
                The bootstrap gRPC address of the remote authority (default port
                50052).
              </div>
            </div>

            {/* Secret field */}
            <div style={fieldStyle}>
              <label style={labelStyle}>Pairing Secret</label>
              <input
                className="input"
                type="password"
                placeholder="NTXP-..."
                value={secret}
                onChange={(e) => setSecret(e.target.value)}
                disabled={pairMut.isPending}
                style={{ fontFamily: "var(--font-mono)", fontSize: 13 }}
              />
              <div style={{ fontSize: 11, color: "var(--text-muted)" }}>
                The one-time pairing secret generated on the remote authority's
                admin panel.
              </div>
            </div>

            {/* Error banner */}
            {error && (
              <div
                style={{
                  display: "flex",
                  alignItems: "flex-start",
                  gap: 8,
                  background: "rgba(239,68,68,0.08)",
                  border: "1px solid rgba(239,68,68,0.3)",
                  borderRadius: "var(--radius)",
                  padding: "10px 14px",
                  fontSize: 13,
                  color: "var(--red)",
                }}
              >
                <HugeiconsIcon
                  icon={AlertCircleIcon}
                  size={14}
                  strokeWidth={1.5}
                  style={{ flexShrink: 0, marginTop: 1 }}
                />
                {error}
              </div>
            )}

            {/* Actions */}
            <div
              style={{ display: "flex", gap: 10, justifyContent: "flex-end" }}
            >
              <button
                className="btn btn-ghost"
                onClick={onClose}
                disabled={pairMut.isPending}
              >
                Cancel
              </button>
              <button
                className="btn btn-primary"
                onClick={() => pairMut.mutate()}
                disabled={pairMut.isPending || !url.trim() || !secret.trim()}
              >
                {pairMut.isPending ? (
                  <>
                    <div
                      className="spinner"
                      style={{ width: 13, height: 13 }}
                    />
                    Pairing…
                  </>
                ) : (
                  <>
                    <HugeiconsIcon
                      icon={Link01Icon}
                      size={13}
                      strokeWidth={1.5}
                    />
                    Pair
                  </>
                )}
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}

function CACertPanel() {
  const [copied, setCopied] = useState(false);

  const caQuery = useQuery({
    queryKey: ["ca-certificate"],
    queryFn: fetchCACertificate,
    retry: false,
  });

  function handleCopy() {
    const pem = caQuery.data?.ca_certificate;
    if (!pem) return;
    navigator.clipboard.writeText(pem).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
  }

  return (
    <div className="card" style={{ overflow: "hidden" }}>
      {/* Card header */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: 8,
          marginBottom: 14,
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <HugeiconsIcon
            icon={ShieldUserIcon}
            size={15}
            strokeWidth={1.5}
            style={{ color: "var(--accent)", flexShrink: 0 }}
          />
          <span className="card-title" style={{ marginBottom: 0 }}>
            CA Certificate
          </span>
        </div>
        {caQuery.data?.ca_certificate && (
          <button
            className="btn btn-ghost"
            style={{
              fontSize: 12,
              padding: "4px 10px",
              color: copied ? "var(--green, #22c55e)" : undefined,
              borderColor: copied ? "var(--green, #22c55e)" : undefined,
            }}
            onClick={handleCopy}
          >
            {copied ? (
              <HugeiconsIcon
                icon={CheckmarkCircle01Icon}
                size={13}
                strokeWidth={1.5}
              />
            ) : (
              <HugeiconsIcon icon={Copy01Icon} size={13} strokeWidth={1.5} />
            )}
            {copied ? "Copied!" : "Copy PEM"}
          </button>
        )}
      </div>

      {/* Loading */}
      {caQuery.isLoading && (
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 10,
            color: "var(--text-muted)",
            fontSize: 13,
          }}
        >
          <div className="spinner" style={{ width: 14, height: 14 }} />
          Loading CA certificate…
        </div>
      )}

      {/* Pairing disabled */}
      {caQuery.isError && isPairingDisabled(caQuery.error) && (
        <div
          style={{
            fontSize: 13,
            color: "var(--text-muted)",
            fontStyle: "italic",
          }}
        >
          Server pairing is not enabled on this authority.
        </div>
      )}

      {/* Generic error */}
      {caQuery.isError && !isPairingDisabled(caQuery.error) && (
        <div
          className="error-banner"
          style={{ display: "flex", alignItems: "center", gap: 8 }}
        >
          <HugeiconsIcon
            icon={AlertCircleIcon}
            size={14}
            strokeWidth={1.5}
            style={{ flexShrink: 0 }}
          />
          Failed to load CA certificate — {(caQuery.error as Error).message}
        </div>
      )}

      {/* PEM content */}
      {caQuery.data?.ca_certificate && (
        <pre
          style={{
            background: "var(--bg-surface)",
            border: "1px solid var(--border)",
            borderRadius: "var(--radius-md)",
            padding: "12px 14px",
            fontFamily: "var(--font-mono)",
            fontSize: 11,
            color: "var(--text-secondary)",
            overflowX: "auto",
            whiteSpace: "pre-wrap",
            wordBreak: "break-all",
            lineHeight: 1.6,
            margin: 0,
          }}
        >
          {caQuery.data.ca_certificate}
        </pre>
      )}
    </div>
  );
}

// ─── Main page ────────────────────────────────────────────────────────────────

export default function ServersPage() {
  const qc = useQueryClient();
  const [includeRevoked, setIncludeRevoked] = useState(false);
  const [revoking, setRevoking] = useState<ServerInfo | null>(null);
  const [showGenerateSecret, setShowGenerateSecret] = useState(false);
  const [showPairWithServer, setShowPairWithServer] = useState(false);
  const [hoveredRow, setHoveredRow] = useState<string | null>(null);

  // ── Queries ───────────────────────────────────────────────────────────────

  const serversQuery = useQuery({
    queryKey: ["servers", { include_revoked: includeRevoked }],
    queryFn: () => fetchServers({ include_revoked: includeRevoked }),
    retry: false,
  });

  // ── Mutations ─────────────────────────────────────────────────────────────

  const revokeMut = useMutation({
    mutationFn: (urn: string) => revokeServer(urn),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["servers"] });
      setRevoking(null);
    },
  });

  // ── Derived data ──────────────────────────────────────────────────────────

  const servers: ServerInfo[] = serversQuery.data?.servers ?? [];
  const totalCount = servers.length;
  const activeCount = servers.filter((s) => !s.revoked).length;
  const revokedCount = servers.filter((s) => s.revoked).length;

  const isPairingOff =
    serversQuery.isError && isPairingDisabled(serversQuery.error);

  return (
    <div className="page-stack">
      {/* ── Header ────────────────────────────────────────────────────────── */}
      <div className="section-header">
        <div>
          <div
            className="section-title"
            style={{ display: "flex", alignItems: "center", gap: 8 }}
          >
            <HugeiconsIcon
              icon={ServerStack01Icon}
              size={18}
              strokeWidth={1.5}
              style={{ color: "var(--accent)" }}
            />
            Peer Servers
          </div>
          <div className="section-sub">
            Paired server nodes and pairing secret management
          </div>
        </div>
        <div style={{ display: "flex", gap: 8 }}>
          <button
            className="btn btn-ghost"
            onClick={() => setShowPairWithServer(true)}
            disabled={isPairingOff}
          >
            <HugeiconsIcon icon={Link01Icon} size={14} strokeWidth={1.5} />
            Pair with Server
          </button>
          <button
            className="btn btn-primary"
            onClick={() => setShowGenerateSecret(true)}
            disabled={isPairingOff}
          >
            <HugeiconsIcon icon={LockIcon} size={14} strokeWidth={1.5} />
            Generate Secret
          </button>
          <button
            className="btn btn-ghost"
            onClick={() => qc.invalidateQueries({ queryKey: ["servers"] })}
            disabled={serversQuery.isFetching}
          >
            <HugeiconsIcon
              icon={Refresh01Icon}
              size={14}
              strokeWidth={1.5}
              className={serversQuery.isFetching ? "spin-icon" : ""}
            />
            Refresh
          </button>
        </div>
      </div>

      {/* ── Pairing disabled notice ───────────────────────────────────────── */}
      {isPairingOff && (
        <div
          style={{
            background: "var(--bg-surface)",
            border: "1px solid var(--border)",
            borderRadius: "var(--radius-lg)",
            padding: "20px 24px",
            display: "flex",
            alignItems: "center",
            gap: 14,
          }}
        >
          <HugeiconsIcon
            icon={ShieldUserIcon}
            size={20}
            strokeWidth={1.5}
            style={{ color: "var(--text-muted)", flexShrink: 0 }}
          />
          <div>
            <div
              style={{
                fontWeight: 600,
                fontSize: 14,
                color: "var(--text-primary)",
                marginBottom: 4,
              }}
            >
              Server pairing is not enabled on this authority
            </div>
            <div style={{ fontSize: 13, color: "var(--text-muted)" }}>
              To enable server pairing, restart notx-engine with the pairing
              feature flag configured.
            </div>
          </div>
        </div>
      )}

      {/* ── Generic error banner ──────────────────────────────────────────── */}
      {serversQuery.isError && !isPairingOff && (
        <div className="error-banner">
          <HugeiconsIcon icon={AlertCircleIcon} size={15} strokeWidth={1.5} />
          Failed to load servers — {(serversQuery.error as Error).message}
        </div>
      )}

      {/* ── Stats row ─────────────────────────────────────────────────────── */}
      {!isPairingOff && (
        <div className="grid-4">
          <div className="stat-tile">
            <div className="stat-label">Total</div>
            <div className="stat-value">{totalCount}</div>
            <div className="stat-sub">all registered</div>
          </div>
          <div className="stat-tile">
            <div className="stat-label">Active</div>
            <div
              className="stat-value"
              style={{
                color:
                  activeCount > 0
                    ? "var(--green, #22c55e)"
                    : "var(--text-primary)",
              }}
            >
              {activeCount}
            </div>
            <div className="stat-sub">live connections</div>
          </div>
          <div className="stat-tile">
            <div className="stat-label">Revoked</div>
            <div
              className="stat-value"
              style={{
                color: revokedCount > 0 ? "var(--red)" : "var(--text-primary)",
              }}
            >
              {revokedCount}
            </div>
            <div className="stat-sub">access removed</div>
          </div>
          <div className="stat-tile">
            <div className="stat-label">Shown</div>
            <div className="stat-value">{servers.length}</div>
            <div className="stat-sub">
              {includeRevoked ? "incl. revoked" : "active only"}
            </div>
          </div>
        </div>
      )}

      {/* ── Toolbar ───────────────────────────────────────────────────────── */}
      {!isPairingOff && (
        <div className="toolbar">
          <button
            className={`btn ${includeRevoked ? "btn-primary" : "btn-ghost"}`}
            style={{ fontSize: 12, padding: "4px 12px" }}
            onClick={() => setIncludeRevoked((v) => !v)}
          >
            {includeRevoked ? (
              <HugeiconsIcon
                icon={CheckmarkCircle01Icon}
                size={13}
                strokeWidth={1.5}
              />
            ) : (
              <span
                style={{
                  width: 13,
                  height: 13,
                  borderRadius: 3,
                  border: "1.5px solid currentColor",
                  display: "inline-block",
                  flexShrink: 0,
                }}
              />
            )}
            Include revoked
          </button>
        </div>
      )}

      {/* ── Table ─────────────────────────────────────────────────────────── */}
      {!isPairingOff && (
        <>
          {serversQuery.isLoading ? (
            <div className="loading-center">
              <div className="spinner" />
              Loading servers…
            </div>
          ) : (
            <div className="card" style={{ overflow: "hidden", padding: 0 }}>
              {servers.length === 0 ? (
                <div className="empty-state" style={{ padding: "56px 0" }}>
                  <HugeiconsIcon
                    icon={ServerStack01Icon}
                    size={28}
                    strokeWidth={1.5}
                    style={{ opacity: 0.3 }}
                  />
                  <div style={{ marginTop: 8 }}>
                    No peer servers registered yet.
                  </div>
                  <div
                    style={{
                      fontSize: 12,
                      color: "var(--text-muted)",
                      marginTop: 4,
                    }}
                  >
                    Generate a pairing secret and use it on a peer node to
                    register.
                  </div>
                </div>
              ) : (
                <table style={{ width: "100%", borderCollapse: "collapse" }}>
                  <thead>
                    <tr
                      style={{
                        borderBottom: "1px solid var(--border)",
                      }}
                    >
                      {[
                        "Name",
                        "Endpoint",
                        "URN",
                        "Status",
                        "Registered",
                        "Expires",
                        "Last Seen",
                        "Actions",
                      ].map((col) => (
                        <th
                          key={col}
                          style={{
                            padding: "10px 14px",
                            textAlign: "left",
                            fontSize: 11,
                            fontWeight: 600,
                            textTransform: "uppercase",
                            letterSpacing: "0.06em",
                            color: "var(--text-muted)",
                            whiteSpace: "nowrap",
                          }}
                        >
                          {col}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {servers.map((s) => (
                      <tr
                        key={s.urn}
                        style={{
                          borderBottom: "1px solid var(--border)",
                          opacity: s.revoked ? 0.55 : 1,
                          background:
                            hoveredRow === s.urn
                              ? "var(--bg-elevated)"
                              : undefined,
                          transition: "background 0.1s",
                        }}
                        onMouseEnter={() => setHoveredRow(s.urn)}
                        onMouseLeave={() => setHoveredRow(null)}
                      >
                        {/* Name */}
                        <td
                          style={{
                            padding: "10px 14px",
                            whiteSpace: "nowrap",
                          }}
                        >
                          <div
                            style={{
                              display: "flex",
                              alignItems: "center",
                              gap: 8,
                            }}
                          >
                            <HugeiconsIcon
                              icon={ServerStack01Icon}
                              size={14}
                              strokeWidth={1.5}
                              style={{
                                color: s.revoked
                                  ? "var(--text-muted)"
                                  : "var(--accent)",
                                flexShrink: 0,
                              }}
                            />
                            <span
                              style={{
                                fontSize: 13,
                                fontWeight: 500,
                                color: s.revoked
                                  ? "var(--text-secondary)"
                                  : "var(--text-primary)",
                              }}
                            >
                              {s.name}
                            </span>
                          </div>
                        </td>

                        {/* Endpoint */}
                        <td
                          style={{
                            padding: "10px 14px",
                            fontSize: 12,
                            color: "var(--text-secondary)",
                            fontFamily: "var(--font-mono)",
                            whiteSpace: "nowrap",
                            maxWidth: 200,
                            overflow: "hidden",
                            textOverflow: "ellipsis",
                          }}
                          title={s.endpoint}
                        >
                          {s.endpoint}
                        </td>

                        {/* URN (truncated) */}
                        <td
                          style={{
                            padding: "10px 14px",
                            fontSize: 11,
                            color: "var(--text-muted)",
                            fontFamily: "var(--font-mono)",
                            whiteSpace: "nowrap",
                          }}
                          title={s.urn}
                        >
                          {truncateUrn(s.urn)}
                        </td>

                        {/* Status */}
                        <td style={{ padding: "10px 14px" }}>
                          <StatusBadge revoked={s.revoked} />
                        </td>

                        {/* Registered */}
                        <td
                          style={{
                            padding: "10px 14px",
                            whiteSpace: "nowrap",
                          }}
                        >
                          <div
                            style={{
                              display: "flex",
                              alignItems: "center",
                              gap: 5,
                              fontSize: 12,
                              color: "var(--text-secondary)",
                            }}
                          >
                            <HugeiconsIcon
                              icon={Clock01Icon}
                              size={11}
                              strokeWidth={1.5}
                              style={{ opacity: 0.5 }}
                            />
                            {fmtDate(s.registered_at)}
                          </div>
                        </td>

                        {/* Expires */}
                        <td
                          style={{
                            padding: "10px 14px",
                            fontSize: 12,
                            color: "var(--text-secondary)",
                            whiteSpace: "nowrap",
                          }}
                        >
                          {fmtDate(s.expires_at)}
                        </td>

                        {/* Last seen */}
                        <td
                          style={{
                            padding: "10px 14px",
                            fontSize: 12,
                            color: "var(--text-muted)",
                            whiteSpace: "nowrap",
                          }}
                        >
                          {s.last_seen_at ? timeSince(s.last_seen_at) : "—"}
                        </td>

                        {/* Actions */}
                        <td
                          style={{ padding: "10px 14px" }}
                          onClick={(e) => e.stopPropagation()}
                        >
                          {!s.revoked ? (
                            <button
                              className="btn"
                              style={{
                                fontSize: 11,
                                padding: "3px 10px",
                                background: "transparent",
                                color: "var(--red)",
                                borderColor: "var(--red)",
                                whiteSpace: "nowrap",
                              }}
                              onClick={() => setRevoking(s)}
                              disabled={
                                revokeMut.isPending &&
                                revokeMut.variables === s.urn
                              }
                              title="Revoke this server"
                            >
                              {revokeMut.isPending &&
                              revokeMut.variables === s.urn ? (
                                <div
                                  className="spinner"
                                  style={{ width: 11, height: 11 }}
                                />
                              ) : (
                                <HugeiconsIcon
                                  icon={Delete01Icon}
                                  size={12}
                                  strokeWidth={1.5}
                                />
                              )}
                              Revoke
                            </button>
                          ) : (
                            <span
                              style={{
                                fontSize: 12,
                                color: "var(--text-muted)",
                              }}
                            >
                              —
                            </span>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          )}
        </>
      )}

      {/* ── CA Certificate section ────────────────────────────────────────── */}
      <CACertPanel />

      {/* ── Revoke confirm modal ──────────────────────────────────────────── */}
      {revoking && (
        <ConfirmRevokeModal
          server={revoking}
          onConfirm={() => revokeMut.mutate(revoking.urn)}
          onCancel={() => setRevoking(null)}
          isPending={revokeMut.isPending}
        />
      )}

      {/* ── Generate secret modal ─────────────────────────────────────────── */}
      {showGenerateSecret && (
        <GenerateSecretModal onClose={() => setShowGenerateSecret(false)} />
      )}

      {/* ── Pair with server modal ────────────────────────────────────────── */}
      {showPairWithServer && (
        <PairWithServerModal onClose={() => setShowPairWithServer(false)} />
      )}
    </div>
  );
}
