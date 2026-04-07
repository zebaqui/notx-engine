import axios from "axios";
import type {
  ListNotesResponse,
  NoteDetail,
  NoteEvents,
  SearchNotesResponse,
  HealthStatus,
  ServerMetrics,
  Project,
  Folder,
  ListProjectsResponse,
  ListFoldersResponse,
  ListDevicesResponse,
  Device,
  ListUsersResponse,
  User,
  ListServersResponse,
  PairingSecret,
  CACertificateResponse,
  ContextStats,
  ListCandidatesResponse,
  CandidateRecord,
  PromoteResponse,
  ListAnchorsResponse,
  ListBacklinksResponse,
  ListOutboundLinksResponse,
  ListExternalLinksResponse,
} from "./types";

// LOCAL_ADMIN_DEVICE_URN is the well-known built-in sentinel device that the
// server bootstraps automatically in local-mode (no --admin-passphrase set).
// See: internal/server/config/config.go — DefaultAdminDeviceURN
const LOCAL_ADMIN_DEVICE_URN =
  "notx:device:00000000-0000-0000-0000-000000000000";

// activeDeviceURN holds the URN used for X-Device-ID on every request.
// It starts as the local sentinel and is overwritten by initAdminDeviceURN()
// when the /admin-config endpoint reveals a different URN (remote mode).
let activeDeviceURN = LOCAL_ADMIN_DEVICE_URN;

const http = axios.create({ baseURL: "/" });

// Inject X-Device-ID on every outgoing request so the value is always
// current even if initAdminDeviceURN() resolves after the instance is created.
http.interceptors.request.use((config) => {
  config.headers["X-Device-ID"] = activeDeviceURN;
  return config;
});

/**
 * Fetches /admin-config from the admin server (served by `notx admin`) and
 * updates the active device URN. Call this once at app startup before any
 * data fetches are made.
 *
 * In local mode the endpoint returns `{"device_urn": ""}` and the sentinel
 * value is kept. In remote mode it returns the URN that was registered during
 * `notx admin --remote` so the correct admin device is used automatically.
 */
export async function initAdminDeviceURN(): Promise<void> {
  try {
    const resp = await axios.get<{ device_urn: string }>("/admin-config");
    const urn = resp.data?.device_urn;
    if (urn && urn.trim() !== "") {
      activeDeviceURN = urn.trim();
    }
    // else: keep LOCAL_ADMIN_DEVICE_URN (local mode)
  } catch {
    // /admin-config is unreachable — keep the sentinel and carry on.
    // This can happen during development when Vite proxies to the API server
    // directly without the `notx admin` process in front.
  }
}

// ─── Health ───────────────────────────────────────────────────────────────

export async function fetchHealth(): Promise<HealthStatus> {
  const [healthz, readyz] = await Promise.allSettled([
    http.get<{ status: string }>("/healthz"),
    http.get<{ status: string }>("/readyz"),
  ]);

  return {
    http_ok:
      healthz.status === "fulfilled" && healthz.value.data.status === "ok",
    ready_ok:
      readyz.status === "fulfilled" && readyz.value.data.status === "ready",
    checked_at: new Date().toISOString(),
  };
}

// ─── Notes ────────────────────────────────────────────────────────────────

export interface ListNotesParams {
  page_size?: number;
  page_token?: string;
  project_urn?: string;
  folder_urn?: string;
  include_deleted?: boolean;
}

export async function fetchNotes(
  params: ListNotesParams = {},
): Promise<ListNotesResponse> {
  const query = new URLSearchParams();
  if (params.page_size) query.set("page_size", String(params.page_size));
  if (params.page_token) query.set("page_token", params.page_token);
  if (params.project_urn) query.set("project_urn", params.project_urn);
  if (params.folder_urn) query.set("folder_urn", params.folder_urn);
  if (params.include_deleted) query.set("include_deleted", "true");

  const { data } = await http.get<ListNotesResponse>(`/v1/notes?${query}`);
  return data;
}

export async function fetchNote(urn: string): Promise<NoteDetail> {
  const { data } = await http.get<NoteDetail>(
    `/v1/notes/${encodeURIComponent(urn)}`,
  );
  return data;
}

export async function fetchNoteEvents(
  urn: string,
  fromSeq?: number,
): Promise<NoteEvents> {
  const query = new URLSearchParams();
  if (fromSeq !== undefined) query.set("from", String(fromSeq));
  const qs = query.toString() ? `?${query}` : "";
  const { data } = await http.get<NoteEvents>(
    `/v1/notes/${encodeURIComponent(urn)}/events${qs}`,
  );
  return data;
}

export async function deleteNote(urn: string): Promise<void> {
  await http.delete(`/v1/notes/${encodeURIComponent(urn)}`);
}

// ─── Search ───────────────────────────────────────────────────────────────

export interface SearchParams {
  q: string;
  page_size?: number;
  page_token?: string;
}

export async function searchNotes(
  params: SearchParams,
): Promise<SearchNotesResponse> {
  const query = new URLSearchParams({ q: params.q });
  if (params.page_size) query.set("page_size", String(params.page_size));
  if (params.page_token) query.set("page_token", params.page_token);

  const { data } = await http.get<SearchNotesResponse>(`/v1/search?${query}`);
  return data;
}

// ─── Projects ─────────────────────────────────────────────────────────────

export interface ListProjectsParams {
  include_deleted?: boolean;
  page_size?: number;
  page_token?: string;
}

export async function fetchProjects(
  params: ListProjectsParams = {},
): Promise<ListProjectsResponse> {
  const query = new URLSearchParams();
  if (params.page_size) query.set("page_size", String(params.page_size));
  if (params.page_token) query.set("page_token", params.page_token);
  if (params.include_deleted) query.set("include_deleted", "true");

  const { data } = await http.get<ListProjectsResponse>(
    `/v1/projects?${query}`,
  );
  return data;
}

export async function fetchProject(urn: string): Promise<Project> {
  const { data } = await http.get<Project>(
    `/v1/projects/${encodeURIComponent(urn)}`,
  );
  return data;
}

export async function createProject(payload: {
  urn: string;
  name: string;
  description?: string;
}): Promise<Project> {
  const { data } = await http.post<Project>("/v1/projects", payload);
  return data;
}

export async function updateProject(
  urn: string,
  patch: { name?: string; description?: string; deleted?: boolean },
): Promise<Project> {
  const { data } = await http.patch<Project>(
    `/v1/projects/${encodeURIComponent(urn)}`,
    patch,
  );
  return data;
}

export async function deleteProject(urn: string): Promise<void> {
  await http.delete(`/v1/projects/${encodeURIComponent(urn)}`);
}

// ─── Folders ──────────────────────────────────────────────────────────────

export interface ListFoldersParams {
  project_urn?: string;
  include_deleted?: boolean;
  page_size?: number;
  page_token?: string;
}

export async function fetchFolders(
  params: ListFoldersParams = {},
): Promise<ListFoldersResponse> {
  const query = new URLSearchParams();
  if (params.project_urn) query.set("project_urn", params.project_urn);
  if (params.page_size) query.set("page_size", String(params.page_size));
  if (params.page_token) query.set("page_token", params.page_token);
  if (params.include_deleted) query.set("include_deleted", "true");

  const { data } = await http.get<ListFoldersResponse>(`/v1/folders?${query}`);
  return data;
}

export async function fetchFolder(urn: string): Promise<Folder> {
  const { data } = await http.get<Folder>(
    `/v1/folders/${encodeURIComponent(urn)}`,
  );
  return data;
}

export async function createFolder(payload: {
  urn: string;
  project_urn: string;
  name: string;
  description?: string;
}): Promise<Folder> {
  const { data } = await http.post<Folder>("/v1/folders", payload);
  return data;
}

export async function updateFolder(
  urn: string,
  patch: { name?: string; description?: string; deleted?: boolean },
): Promise<Folder> {
  const { data } = await http.patch<Folder>(
    `/v1/folders/${encodeURIComponent(urn)}`,
    patch,
  );
  return data;
}

export async function deleteFolder(urn: string): Promise<void> {
  await http.delete(`/v1/folders/${encodeURIComponent(urn)}`);
}

// ─── Devices ──────────────────────────────────────────────────────────────

export interface ListDevicesParams {
  owner_urn?: string;
  include_revoked?: boolean;
}

export async function fetchDevices(
  params: ListDevicesParams = {},
): Promise<ListDevicesResponse> {
  const query = new URLSearchParams();
  if (params.owner_urn) query.set("owner_urn", params.owner_urn);
  if (params.include_revoked) query.set("include_revoked", "true");
  const qs = query.toString() ? `?${query}` : "";
  const { data } = await http.get<ListDevicesResponse>(`/v1/devices${qs}`);
  return data;
}

export async function registerDevice(payload: {
  urn: string;
  name: string;
  owner_urn: string;
  public_key_b64?: string;
}): Promise<Device> {
  const { data } = await http.post<Device>("/v1/devices", payload);
  return data;
}

export async function updateDevice(
  urn: string,
  patch: { name?: string; last_seen_at?: string },
): Promise<Device> {
  const { data } = await http.patch<Device>(
    `/v1/devices/${encodeURIComponent(urn)}`,
    patch,
  );
  return data;
}

export async function revokeDevice(urn: string): Promise<void> {
  await http.delete(`/v1/devices/${encodeURIComponent(urn)}`);
}

export async function fetchDevice(urn: string): Promise<Device> {
  const { data } = await http.get<Device>(
    `/v1/devices/${encodeURIComponent(urn)}`,
  );
  return data;
}

export async function approveDevice(urn: string): Promise<Device> {
  const { data } = await http.patch<Device>(
    `/v1/devices/${encodeURIComponent(urn)}/approve`,
  );
  return data;
}

export async function rejectDevice(urn: string): Promise<Device> {
  const { data } = await http.patch<Device>(
    `/v1/devices/${encodeURIComponent(urn)}/reject`,
  );
  return data;
}

// ─── Users ────────────────────────────────────────────────────────────────

export interface ListUsersParams {
  include_deleted?: boolean;
  page_size?: number;
  page_token?: string;
}

export async function fetchUsers(
  params: ListUsersParams = {},
): Promise<ListUsersResponse> {
  const query = new URLSearchParams();
  if (params.page_size) query.set("page_size", String(params.page_size));
  if (params.page_token) query.set("page_token", params.page_token);
  if (params.include_deleted) query.set("include_deleted", "true");
  const qs = query.toString() ? `?${query}` : "";
  const { data } = await http.get<ListUsersResponse>(`/v1/users${qs}`);
  return data;
}

export async function fetchUser(urn: string): Promise<User> {
  const { data } = await http.get<User>(`/v1/users/${encodeURIComponent(urn)}`);
  return data;
}

export async function createUser(payload: {
  urn: string;
  display_name: string;
  email?: string;
}): Promise<User> {
  const { data } = await http.post<User>("/v1/users", payload);
  return data;
}

export async function updateUser(
  urn: string,
  patch: { display_name?: string; email?: string; deleted?: boolean },
): Promise<User> {
  const { data } = await http.patch<User>(
    `/v1/users/${encodeURIComponent(urn)}`,
    patch,
  );
  return data;
}

export async function deleteUser(urn: string): Promise<void> {
  await http.delete(`/v1/users/${encodeURIComponent(urn)}`);
}

// ─── Servers (Pairing) ───────────────────────────────────────────────────────

export interface ListServersParams {
  include_revoked?: boolean;
}

export async function fetchServers(
  params: ListServersParams = {},
): Promise<ListServersResponse> {
  const query = new URLSearchParams();
  if (params.include_revoked) query.set("include_revoked", "true");
  const qs = query.toString() ? `?${query}` : "";
  const { data } = await http.get<ListServersResponse>(`/v1/servers${qs}`);
  return data;
}

export async function revokeServer(urn: string): Promise<void> {
  await http.delete(`/v1/servers/${encodeURIComponent(urn)}`);
}

export async function fetchCACertificate(): Promise<CACertificateResponse> {
  const { data } = await http.get<CACertificateResponse>("/v1/servers/ca");
  return data;
}

export async function createPairingSecret(payload: {
  label?: string;
}): Promise<PairingSecret> {
  const { data } = await http.post<PairingSecret>(
    "/v1/pairing-secrets",
    payload,
  );
  return data;
}

export interface OutboundPairResponse {
  server_urn: string;
  expires_at: string; // ISO-8601
}

export async function pairWithServer(payload: {
  url: string;
  secret: string;
}): Promise<OutboundPairResponse> {
  const { data } = await http.post<OutboundPairResponse>(
    "/v1/servers/outbound-pair",
    payload,
  );
  return data;
}

// ─── Context ──────────────────────────────────────────────────────────────────

export async function fetchContextStats(
  projectUrn?: string,
): Promise<ContextStats> {
  const query = new URLSearchParams();
  if (projectUrn) query.set("project_urn", projectUrn);
  const qs = query.toString() ? `?${query}` : "";
  const { data } = await http.get<ContextStats>(`/v1/context/stats${qs}`);
  return data;
}

export interface ListCandidatesParams {
  project_urn?: string;
  note_urn?: string;
  status?: string;
  min_score?: number;
  include_bursts?: boolean;
  page_size?: number;
  page_token?: string;
}

export async function fetchCandidates(
  params: ListCandidatesParams = {},
): Promise<ListCandidatesResponse> {
  const query = new URLSearchParams();
  if (params.project_urn) query.set("project_urn", params.project_urn);
  if (params.note_urn) query.set("note_urn", params.note_urn);
  if (params.status) query.set("status", params.status);
  if (params.min_score !== undefined)
    query.set("min_score", String(params.min_score));
  if (params.include_bursts) query.set("include_bursts", "true");
  if (params.page_size) query.set("page_size", String(params.page_size));
  if (params.page_token) query.set("page_token", params.page_token);
  const { data } = await http.get<ListCandidatesResponse>(
    `/v1/context/candidates?${query}`,
  );
  return data;
}

export async function promoteCandidate(
  id: string,
  payload: { label: string; direction?: string; reviewer_urn?: string },
): Promise<PromoteResponse> {
  const { data } = await http.post<PromoteResponse>(
    `/v1/context/candidates/${encodeURIComponent(id)}/promote`,
    { direction: "both", reviewer_urn: "urn:notx:usr:anon", ...payload },
  );
  return data;
}

export async function dismissCandidate(
  id: string,
  reviewerUrn?: string,
): Promise<CandidateRecord> {
  const { data } = await http.post<{ candidate: CandidateRecord }>(
    `/v1/context/candidates/${encodeURIComponent(id)}/dismiss`,
    { reviewer_urn: reviewerUrn ?? "urn:notx:usr:anon" },
  );
  return data.candidate;
}

// ─── Links ────────────────────────────────────────────────────────────────────

export async function fetchAnchors(
  noteUrn: string,
): Promise<ListAnchorsResponse> {
  const { data } = await http.get<ListAnchorsResponse>(
    `/v1/links/anchors?note_urn=${encodeURIComponent(noteUrn)}`,
  );
  return data;
}

export async function fetchBacklinks(
  targetUrn: string,
  anchorId?: string,
): Promise<ListBacklinksResponse> {
  const query = new URLSearchParams({ target_urn: targetUrn });
  if (anchorId) query.set("anchor_id", anchorId);
  const { data } = await http.get<ListBacklinksResponse>(
    `/v1/links/backlinks?${query}`,
  );
  return data;
}

export async function fetchOutboundLinks(
  sourceUrn: string,
): Promise<ListOutboundLinksResponse> {
  const { data } = await http.get<ListOutboundLinksResponse>(
    `/v1/links/outbound?source_urn=${encodeURIComponent(sourceUrn)}`,
  );
  return data;
}

export interface RecentBacklinksParams {
  note_urn?: string;
  label?: string;
  limit?: number;
}

export async function fetchRecentBacklinks(
  params: RecentBacklinksParams = {},
): Promise<ListBacklinksResponse> {
  const query = new URLSearchParams();
  if (params.note_urn) query.set("note_urn", params.note_urn);
  if (params.label) query.set("label", params.label);
  if (params.limit) query.set("limit", String(params.limit));
  const qs = query.toString() ? `?${query}` : "";
  const { data } = await http.get<ListBacklinksResponse>(
    `/v1/links/backlinks/recent${qs}`,
  );
  return data;
}

export async function fetchExternalLinks(
  sourceUrn: string,
): Promise<ListExternalLinksResponse> {
  const { data } = await http.get<ListExternalLinksResponse>(
    `/v1/links/external?source_urn=${encodeURIComponent(sourceUrn)}`,
  );
  return data;
}

// ─── Metrics (assembled from list endpoint) ───────────────────────────────

export async function fetchMetrics(): Promise<ServerMetrics> {
  // Pull up to 200 records per resource to compute stats.
  // For a small/medium deployment this is fine; a dedicated /metrics endpoint
  // would be better for large deployments.
  const [notesResp, projectsResp, foldersResp, usersResp, devicesResp] =
    await Promise.all([
      fetchNotes({ page_size: 200, include_deleted: true }),
      fetchProjects({ page_size: 200, include_deleted: true }),
      fetchFolders({ page_size: 200, include_deleted: true }),
      fetchUsers({ page_size: 200, include_deleted: true }),
      fetchDevices({ include_revoked: true }),
    ]);

  const notes = notesResp.notes ?? [];
  const total = notes.length;
  const deletedCount = notes.filter((n) => n.deleted).length;
  const activeCount = total - deletedCount;
  const secureCount = notes.filter((n) => n.note_type === "secure").length;
  const normalCount = notes.filter((n) => n.note_type === "normal").length;

  const users = usersResp.users ?? [];
  const totalUsers = users.length;
  const activeUsers = users.filter((u) => !u.deleted).length;

  const devices = devicesResp.devices ?? [];
  const totalDevices = devices.length;
  const activeDevices = devices.filter(
    (d) => d.approval_status === "approved" && !d.revoked,
  ).length;
  const pendingDevices = devices.filter(
    (d) => d.approval_status === "pending" && !d.revoked,
  ).length;

  return {
    total_notes: total,
    deleted_notes: deletedCount,
    active_notes: activeCount,
    secure_notes: secureCount,
    normal_notes: normalCount,
    total_events: 0, // requires per-note fetch; omitted for performance
    total_projects: (projectsResp.projects ?? []).length,
    total_folders: (foldersResp.folders ?? []).length,
    total_users: totalUsers,
    active_users: activeUsers,
    total_devices: totalDevices,
    active_devices: activeDevices,
    pending_devices: pendingDevices,
  };
}
