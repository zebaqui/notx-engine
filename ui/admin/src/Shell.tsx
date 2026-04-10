import { useNavigate, useRouterState } from "@tanstack/react-router";
import { HugeiconsIcon } from "@hugeicons/react";
import {
  DashboardSquare01Icon,
  Settings01Icon,
  Note01Icon,
  FolderOpenIcon,
  GitBranchIcon,
  Link01Icon,
  MonitorDotIcon,
  ServerStack01Icon,
  UserGroupIcon,
  Activity01Icon,
  Search01Icon,
} from "@hugeicons/core-free-icons";

// ─── Nav definition ───────────────────────────────────────────────────────────

const NAV = [
  { path: "/overview", label: "Overview", icon: DashboardSquare01Icon },
  { path: "/notes", label: "Notes", icon: Note01Icon },
  { path: "/projects", label: "Projects", icon: FolderOpenIcon },
  { path: "/context", label: "Context", icon: GitBranchIcon },
  { path: "/links", label: "Links", icon: Link01Icon },
  { path: "/devices", label: "Devices", icon: MonitorDotIcon },
  { path: "/servers", label: "Servers", icon: ServerStack01Icon },
  { path: "/users", label: "Users", icon: UserGroupIcon },
  { path: "/config", label: "Config", icon: Settings01Icon },
] as const;

const PAGE_TITLES: Record<string, string> = {
  "/overview": "Overview",
  "/notes": "Notes",
  "/projects": "Projects",
  "/context": "Context graph",
  "/links": "Link inspector",
  "/devices": "Devices",
  "/servers": "Peer servers",
  "/users": "Users",
  "/config": "Configuration",
};

// ─── Shell ────────────────────────────────────────────────────────────────────

export default function Shell({ children }: { children: React.ReactNode }) {
  const navigate = useNavigate();
  const routerState = useRouterState();
  const pathname = routerState.location.pathname;
  const title = PAGE_TITLES[pathname] ?? "notx";

  return (
    <div className="shell">
      {/* ── Sidebar ─────────────────────────────────────────────────────── */}
      <aside className="sidebar">
        <div className="sidebar-logo">
          <div className="logo-wordmark">
            <span className="logo-dot" />
            notx
          </div>
          <button className="search-hint" onClick={() => {}}>
            <HugeiconsIcon icon={Search01Icon} size={11} />
            <kbd>⌘P</kbd>
          </button>
        </div>

        <nav className="sidebar-nav">
          {NAV.map(({ path, label, icon }) => (
            <button
              key={path}
              className={`nav-item${pathname === path ? " active" : ""}`}
              onClick={() => navigate({ to: path })}
            >
              <HugeiconsIcon icon={icon} size={14} strokeWidth={1.5} />
              {label}
            </button>
          ))}

          <div className="divider" style={{ margin: "6px 0" }} />

          <a
            href="http://localhost:4060/healthz"
            target="_blank"
            rel="noopener noreferrer"
            className="nav-item"
            style={{ textDecoration: "none" }}
          >
            <HugeiconsIcon icon={Activity01Icon} size={14} strokeWidth={1.5} />
            healthz
          </a>
        </nav>

        <div
          style={{
            padding: "10px 14px",
            borderTop: "1px solid var(--border)",
            fontSize: 10.5,
            color: "var(--text-muted)",
            lineHeight: 1.7,
          }}
        >
          <div style={{ color: "var(--text-secondary)", marginBottom: 1 }}>
            notx-engine
          </div>
          <div>:4060 · :50051</div>
        </div>
      </aside>

      {/* ── Main ────────────────────────────────────────────────────────── */}
      <div className="main">
        <header className="topbar">
          <span className="topbar-title">{title}</span>
          <div className="topbar-right">
            <span
              style={{
                fontSize: 10.5,
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
