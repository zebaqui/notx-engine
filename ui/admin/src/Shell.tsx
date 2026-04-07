import { useNavigate, useRouterState } from "@tanstack/react-router";
import {
  LayoutDashboard,
  Settings,
  FileText,
  Activity,
  FolderOpen,
  Monitor,
  Users,
  Server,
  GitBranch,
  Link2,
} from "lucide-react";

// ─── Nav definition ───────────────────────────────────────────────────────────

const NAV = [
  { path: "/overview", label: "Overview", icon: <LayoutDashboard size={16} /> },
  { path: "/notes", label: "Notes", icon: <FileText size={16} /> },
  { path: "/projects", label: "Projects", icon: <FolderOpen size={16} /> },
  { path: "/context", label: "Context", icon: <GitBranch size={16} /> },
  { path: "/links", label: "Links", icon: <Link2 size={16} /> },
  { path: "/devices", label: "Devices", icon: <Monitor size={16} /> },
  { path: "/servers", label: "Servers", icon: <Server size={16} /> },
  { path: "/users", label: "Users", icon: <Users size={16} /> },
  { path: "/config", label: "Configuration", icon: <Settings size={16} /> },
] as const;

const PAGE_TITLES: Record<string, string> = {
  "/overview": "Overview",
  "/notes": "Notes",
  "/projects": "Projects & Folders",
  "/context": "Context Graph",
  "/links": "Link Inspector",
  "/devices": "Devices",
  "/servers": "Peer Servers",
  "/users": "Users",
  "/config": "Configuration",
};

// ─── Shell ────────────────────────────────────────────────────────────────────

export default function Shell({ children }: { children: React.ReactNode }) {
  const navigate = useNavigate();
  const routerState = useRouterState();
  const pathname = routerState.location.pathname;
  const title = PAGE_TITLES[pathname] ?? "notx admin";

  return (
    <div className="shell">
      {/* ── Sidebar ─────────────────────────────────────────────────────── */}
      <aside className="sidebar">
        <div className="sidebar-logo">
          <button className="search-hint" onClick={() => {}}>
            <svg
              width="13"
              height="13"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2.2"
              strokeLinecap="round"
              strokeLinejoin="round"
            >
              <circle cx="11" cy="11" r="8" />
              <line x1="21" y1="21" x2="16.65" y2="16.65" />
            </svg>
            <kbd>⌘P</kbd>
          </button>
          <div className="logo-wordmark">
            <span className="logo-dot" />
            notx
          </div>
        </div>

        <nav className="sidebar-nav">
          {NAV.map(({ path, label, icon }) => (
            <button
              key={path}
              className={`nav-item${pathname === path ? " active" : ""}`}
              onClick={() => navigate({ to: path })}
            >
              {icon}
              {label}
            </button>
          ))}

          <div className="divider" style={{ margin: "8px 0" }} />

          <a
            href="http://localhost:4060/healthz"
            target="_blank"
            rel="noopener noreferrer"
            className="nav-item"
            style={{ textDecoration: "none" }}
          >
            <Activity size={16} />
            /healthz
          </a>
        </nav>

        <div
          style={{
            padding: "12px 16px",
            borderTop: "1px solid var(--border)",
            fontSize: 11,
            color: "var(--text-muted)",
            lineHeight: 1.6,
          }}
        >
          <div style={{ fontWeight: 600, marginBottom: 2 }}>notx-engine</div>
          <div>HTTP :4060 · gRPC :50051</div>
        </div>
      </aside>

      {/* ── Main ────────────────────────────────────────────────────────── */}
      <div className="main">
        <header className="topbar">
          <span className="topbar-title">{title}</span>
          <div className="topbar-right">
            <span
              style={{
                fontSize: 11,
                color: "var(--text-muted)",
                fontFamily: "var(--font-mono)",
              }}
            >
              localhost:4060
            </span>
          </div>
        </header>

        <main className="page-content">{children}</main>
      </div>
    </div>
  );
}
