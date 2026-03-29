// ─── Wire types mirroring the Go HTTP JSON layer ──────────────────────────

export type NoteType = "normal" | "secure";

export interface NoteHeader {
  urn: string;
  name: string;
  note_type: NoteType;
  project_urn: string;
  folder_urn: string;
  deleted: boolean;
  created_at: string; // ISO-8601
  updated_at: string; // ISO-8601
}

export interface LineEntry {
  op: "set" | "delete";
  line_number: number;
  content?: string;
}

export interface Event {
  urn: string;
  note_urn: string;
  sequence: number;
  author_urn: string;
  created_at: string;
  entries: LineEntry[];
}

export interface NoteDetail {
  header: NoteHeader;
  content: string;
}

export interface NoteEvents {
  note_urn: string;
  events: Event[];
  count: number;
}

export interface ListNotesResponse {
  notes: NoteHeader[];
  next_page_token: string;
}

export interface SearchResult {
  note: NoteHeader;
  excerpt: string;
}

export interface SearchNotesResponse {
  results: SearchResult[];
  next_page_token: string;
}

// ─── Admin-only synthetic types (assembled client-side) ────────────────────

export interface ServerConfig {
  http_port: number;
  grpc_port: number;
  host: string;
  data_dir: string;
  enable_http: boolean;
  enable_grpc: boolean;
  tls_enabled: boolean;
  mtls_enabled: boolean;
  shutdown_timeout_s: number;
  max_page_size: number;
  default_page_size: number;
  log_level: string;
}

export interface HealthStatus {
  http_ok: boolean;
  ready_ok: boolean;
  checked_at: string;
}

export interface ServerMetrics {
  total_notes: number;
  deleted_notes: number;
  active_notes: number;
  secure_notes: number;
  normal_notes: number;
  total_events: number;
}
