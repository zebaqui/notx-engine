import axios from "axios";
import type {
  ListNotesResponse,
  NoteDetail,
  NoteEvents,
  SearchNotesResponse,
  HealthStatus,
  ServerMetrics,
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

// ─── Metrics (assembled from list endpoint) ───────────────────────────────

export async function fetchMetrics(): Promise<ServerMetrics> {
  // Pull up to 200 notes (max page size) to compute stats.
  // For a small/medium deployment this is fine; a dedicated /metrics endpoint
  // would be better for large deployments.
  const [all, deleted] = await Promise.all([
    fetchNotes({ page_size: 200, include_deleted: true }),
    fetchNotes({ page_size: 200, include_deleted: true }),
  ]);

  const notes = all.notes ?? [];
  const total = notes.length;
  const deletedCount = notes.filter((n) => n.deleted).length;
  const activeCount = total - deletedCount;
  const secureCount = notes.filter((n) => n.note_type === "secure").length;
  const normalCount = notes.filter((n) => n.note_type === "normal").length;

  // head_sequence is not on NoteHeader in the list response, but we can still
  // show note counts. Total events would need individual GETs — skip for now.
  void deleted;

  return {
    total_notes: total,
    deleted_notes: deletedCount,
    active_notes: activeCount,
    secure_notes: secureCount,
    normal_notes: normalCount,
    total_events: 0, // requires per-note fetch; omitted for performance
  };
}
