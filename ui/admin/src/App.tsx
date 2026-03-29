import { useState } from "react";
import { LayoutDashboard, Settings, FileText, Activity } from "lucide-react";
import OverviewPage from "./pages/OverviewPage";
import ConfigPage from "./pages/ConfigPage";
import NotesPage from "./pages/NotesPage";

type Page = "overview" | "config" | "notes";

const NAV: { id: Page; label: string; icon: React.ReactNode }[] = [
  { id: "overview", label: "Overview", icon: <LayoutDashboard size={16} /> },
  { id: "notes", label: "Notes", icon: <FileText size={16} /> },
  { id: "config", label: "Configuration", icon: <Settings size={16} /> },
];

const PAGE_TITLES: Record<Page, string> = {
  overview: "Overview",
  notes: "Notes",
  config: "Configuration",
};

export default function App() {
  const [page, setPage] = useState<Page>("overview");

  return (
    <div className="shell">
      {/* ── Sidebar ─────────────────────────────────────────────────────── */}
      <aside className="sidebar">
        <div className="sidebar-logo">
          <span className="logo-dot" />
          notx admin
        </div>

        <nav className="sidebar-nav">
          {NAV.map(({ id, label, icon }) => (
            <button
              key={id}
              className={`nav-item${page === id ? " active" : ""}`}
              onClick={() => setPage(id)}
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
        {/* Topbar */}
        <header className="topbar">
          <span className="topbar-title">{PAGE_TITLES[page]}</span>
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

        {/* Page content */}
        <main className="page-content">
          {page === "overview" && <OverviewPage />}
          {page === "notes" && <NotesPage />}
          {page === "config" && <ConfigPage />}
        </main>
      </div>
    </div>
  );
}
