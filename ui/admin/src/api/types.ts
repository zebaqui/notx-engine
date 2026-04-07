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

export interface Project {
  urn: string;
  name: string;
  description?: string;
  deleted: boolean;
  created_at: string; // ISO-8601
  updated_at: string; // ISO-8601
}

export interface Folder {
  urn: string;
  project_urn: string;
  name: string;
  description?: string;
  deleted: boolean;
  created_at: string; // ISO-8601
  updated_at: string; // ISO-8601
}

export interface ListProjectsResponse {
  projects: Project[];
  next_page_token: string;
}

export interface ListFoldersResponse {
  folders: Folder[];
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

export type DeviceApprovalStatus = "pending" | "approved" | "rejected";

export interface Device {
  urn: string;
  name: string;
  owner_urn: string;
  public_key_b64: string; // base64-encoded Ed25519 public key
  role: "client" | "admin";
  approval_status: "pending" | "approved" | "rejected";
  registered_at: string; // ISO-8601
  last_seen_at?: string; // ISO-8601, optional
  revoked: boolean;
}

export interface ListDevicesResponse {
  devices: Device[];
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
  total_projects: number;
  total_folders: number;
  total_users: number;
  active_users: number;
  total_devices: number;
  active_devices: number;
  pending_devices: number;
}

export interface User {
  urn: string;
  display_name: string;
  email?: string;
  deleted: boolean;
  created_at: string; // ISO-8601
  updated_at: string; // ISO-8601
}

export interface ListUsersResponse {
  users: User[];
  next_page_token: string;
}

// ─── Server Pairing ───────────────────────────────────────────────────────────

export interface ServerInfo {
  urn: string;
  name: string;
  endpoint: string;
  revoked: boolean;
  registered_at: string; // ISO-8601
  expires_at: string; // ISO-8601
  last_seen_at?: string; // ISO-8601, optional
}

export interface ListServersResponse {
  servers: ServerInfo[];
}

export interface PairingSecret {
  id: string;
  label: string;
  plaintext: string; // shown only once on creation
  expires_at: string; // ISO-8601
}

export interface CACertificateResponse {
  ca_certificate: string; // PEM-encoded CA cert
}

// ─── Context Graph ────────────────────────────────────────────────────────────

export interface ContextStats {
  bursts_total: number;
  bursts_today: number;
  candidates_pending: number;
  candidates_pending_unenriched: number;
  candidates_promoted: number;
  candidates_dismissed: number;
  oldest_pending_age_days: number;
}

export interface BurstRecord {
  id: string;
  note_urn: string;
  project_urn?: string;
  folder_urn?: string;
  author_urn?: string;
  sequence: number;
  line_start: number;
  line_end: number;
  text: string;
  tokens?: string;
  truncated?: boolean;
  created_at?: string;
}

export interface CandidateRecord {
  id: string;
  burst_a_id: string;
  burst_b_id: string;
  note_urn_a: string;
  note_urn_b: string;
  project_urn?: string;
  overlap_score: number;
  bm25_score: number;
  status: "pending" | "promoted" | "dismissed" | "expired";
  created_at?: string;
  reviewed_at?: string;
  reviewed_by?: string;
  promoted_link?: string;
  burst_a?: BurstRecord;
  burst_b?: BurstRecord;
}

export interface ListCandidatesResponse {
  candidates: CandidateRecord[];
  next_page_token?: string;
}

export interface PromoteResponse {
  anchor_a_id: string;
  anchor_b_id: string;
  link_a_to_b?: string;
  link_b_to_a?: string;
  candidate?: CandidateRecord;
}

export interface ProjectContextConfig {
  project_urn: string;
  burst_max_per_note_per_day: number;
  burst_max_per_project_per_day: number;
  updated_at?: string;
}

// ─── Links ────────────────────────────────────────────────────────────────────

export interface AnchorRecord {
  note_urn: string;
  anchor_id: string;
  line: number;
  char_start: number;
  char_end?: number;
  preview?: string;
  status: "ok" | "broken" | "deprecated";
  updated_at?: string;
}

export interface BacklinkRecord {
  source_urn: string;
  target_urn: string;
  target_anchor: string;
  label?: string;
  created_at?: string;
}

export interface ExternalLinkRecord {
  source_urn: string;
  uri: string;
  label?: string;
  created_at?: string;
}

export interface ListAnchorsResponse {
  anchors: AnchorRecord[];
}

export interface ListBacklinksResponse {
  backlinks: BacklinkRecord[];
}

export interface ListOutboundLinksResponse {
  links: BacklinkRecord[];
}

export interface ListExternalLinksResponse {
  links: ExternalLinkRecord[];
}
