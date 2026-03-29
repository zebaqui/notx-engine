import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { initAdminDeviceURN } from "./api/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createRouter,
  createRootRoute,
  createRoute,
  RouterProvider,
  Outlet,
  redirect,
} from "@tanstack/react-router";
import "./index.css";
import Shell from "./Shell";
import OverviewPage from "./pages/OverviewPage";
import NotesPage from "./pages/NotesPage";
import ProjectsPage from "./pages/ProjectsPage";
import DevicesPage from "./pages/DevicesPage";
import UsersPage from "./pages/UsersPage";
import ConfigPage from "./pages/ConfigPage";

// ─── Query client ─────────────────────────────────────────────────────────────

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      staleTime: 10_000,
      refetchOnWindowFocus: false,
    },
  },
});

// ─── Route tree ───────────────────────────────────────────────────────────────

const rootRoute = createRootRoute({
  component: () => (
    <QueryClientProvider client={queryClient}>
      <Shell>
        <Outlet />
      </Shell>
    </QueryClientProvider>
  ),
});

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  beforeLoad: () => {
    throw redirect({ to: "/overview", replace: true });
  },
});

const overviewRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/overview",
  component: OverviewPage,
});

const notesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/notes",
  component: NotesPage,
});

const projectsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/projects",
  component: ProjectsPage,
});

const devicesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/devices",
  component: DevicesPage,
});

const usersRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/users",
  component: UsersPage,
});

const configRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/config",
  component: ConfigPage,
});

const routeTree = rootRoute.addChildren([
  indexRoute,
  overviewRoute,
  notesRoute,
  projectsRoute,
  devicesRoute,
  usersRoute,
  configRoute,
]);

// ─── Router ───────────────────────────────────────────────────────────────────

const router = createRouter({
  routeTree,
  defaultPreload: "intent",
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

// ─── Mount ────────────────────────────────────────────────────────────────────

// Resolve the admin device URN before mounting so every API call from the
// very first render already carries the correct X-Device-ID header.
// initAdminDeviceURN() is safe to call even if /admin-config is unreachable —
// it falls back to the local-mode sentinel silently.
initAdminDeviceURN().then(() => {
  createRoot(document.getElementById("root")!).render(
    <StrictMode>
      <RouterProvider router={router} />
    </StrictMode>,
  );
});
