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
} from "./types";

const http = axios.create({ baseURL: "/" });

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

export class DeviceAPINotImplementedError extends Error {
  constructor() {
    super(
      "The Device HTTP API is not yet available. Device management is currently " +
        "only accessible via the gRPC DeviceService (RegisterDevice, ListDevices, " +
        "RevokeDevice). Wire up /v1/devices on the server to enable this section.",
    );
    this.name = "DeviceAPINotImplementedError";
  }
}

export interface ListDevicesParams {
  owner_urn?: string;
  include_revoked?: boolean;
}

export async function fetchDevices(
  params: ListDevicesParams = {},
): Promise<ListDevicesResponse> {
  try {
    const query = new URLSearchParams();
    if (params.owner_urn) query.set("owner_urn", params.owner_urn);
    if (params.include_revoked) query.set("include_revoked", "true");
    const qs = query.toString() ? `?${query}` : "";
    const { data } = await http.get<ListDevicesResponse>(`/v1/devices${qs}`);
    return data;
  } catch (err: unknown) {
    if (axios.isAxiosError(err) && err.response?.status === 404) {
      throw new DeviceAPINotImplementedError();
    }
    throw err;
  }
}

export async function registerDevice(payload: {
  urn: string;
  name: string;
  owner_urn: string;
  public_key_b64?: string;
}): Promise<Device> {
  try {
    const { data } = await http.post<Device>("/v1/devices", payload);
    return data;
  } catch (err: unknown) {
    if (axios.isAxiosError(err) && err.response?.status === 404) {
      throw new DeviceAPINotImplementedError();
    }
    throw err;
  }
}

export async function updateDevice(
  urn: string,
  patch: { name?: string; last_seen_at?: string },
): Promise<Device> {
  try {
    const { data } = await http.patch<Device>(
      `/v1/devices/${encodeURIComponent(urn)}`,
      patch,
    );
    return data;
  } catch (err: unknown) {
    if (axios.isAxiosError(err) && err.response?.status === 404) {
      throw new DeviceAPINotImplementedError();
    }
    throw err;
  }
}

export async function revokeDevice(urn: string): Promise<void> {
  try {
    await http.delete(`/v1/devices/${encodeURIComponent(urn)}`);
  } catch (err: unknown) {
    if (axios.isAxiosError(err) && err.response?.status === 404) {
      throw new DeviceAPINotImplementedError();
    }
    throw err;
  }
}

export async function fetchDevice(urn: string): Promise<Device> {
  try {
    const { data } = await http.get<Device>(
      `/v1/devices/${encodeURIComponent(urn)}`,
    );
    return data;
  } catch (err: unknown) {
    if (axios.isAxiosError(err) && err.response?.status === 404) {
      throw new DeviceAPINotImplementedError();
    }
    throw err;
  }
}

// ─── Metrics (assembled from list endpoint) ───────────────────────────────

export async function fetchMetrics(): Promise<ServerMetrics> {
  // Pull up to 200 notes/projects/folders to compute stats.
  // For a small/medium deployment this is fine; a dedicated /metrics endpoint
  // would be better for large deployments.
  const [notesResp, projectsResp, foldersResp] = await Promise.all([
    fetchNotes({ page_size: 200, include_deleted: true }),
    fetchProjects({ page_size: 200, include_deleted: true }),
    fetchFolders({ page_size: 200, include_deleted: true }),
  ]);

  const notes = notesResp.notes ?? [];
  const total = notes.length;
  const deletedCount = notes.filter((n) => n.deleted).length;
  const activeCount = total - deletedCount;
  const secureCount = notes.filter((n) => n.note_type === "secure").length;
  const normalCount = notes.filter((n) => n.note_type === "normal").length;

  return {
    total_notes: total,
    deleted_notes: deletedCount,
    active_notes: activeCount,
    secure_notes: secureCount,
    normal_notes: normalCount,
    total_events: 0, // requires per-note fetch; omitted for performance
    total_projects: (projectsResp.projects ?? []).length,
    total_folders: (foldersResp.folders ?? []).length,
  };
}
