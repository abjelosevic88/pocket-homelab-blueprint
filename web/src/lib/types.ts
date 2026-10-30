export interface User { id: number; email: string; name: string; role: 'admin' | 'member'; created_at: string }
export interface APIToken { id: number; user_id: number; name: string; prefix: string; last_used_at: string | null; created_at: string }

export type ConnMode = 'core' | 'direct' | 'off'
export type ConnVia = '' | 'core' | 'direct'

export interface Settings {
  core_configured: boolean
  core_registered?: boolean
  title: string; date_format: string; time_format: string
  keep_scans: number; vendored: boolean; git_commits: number; hide_tests: boolean; graph_layout: 'layers' | 'organic'; show_external: boolean
  notify_done: boolean; notify_failed: boolean
  notify_url: string; notify_format: string; notify_configured: boolean
  ai_provider: 'auto' | 'pocket' | 'openai'; ai_pocket_model: string; openai_api_key: string; openai_model: string; openai_base_url: string
  setup_complete: boolean; base_url: string
  // per sibling app: <slug>_mode, _via, _detail, _configured, _url, _token, _public_url, _open_url
  [k: string]: unknown
}

export type Source = 'path' | 'git' | 'upload' | 'files'

export interface Stats {
  files: number; code_files: number; dirs: number; lines: number; code: number; bytes: number; symbols: number; edges: number
  endpoints: number; tables: number; env_vars: number; externals: number; todos: number; tests: number; skipped: number; cycles: number; orphans: number
}

export interface Scan {
  id: number; codebase_id: number; status: 'queued' | 'running' | 'done' | 'failed' | 'cancelled'; stage: string; progress: number; error: string
  trigger: string; commit: string; branch: string; stats: Partial<Stats>; result_bytes: number
  started_at: string; finished_at: string; duration_ms: number; created_at: string
}

export interface Codebase {
  id: number; name: string; description: string; source: Source; location: string; branch: string; subdir: string
  git_user: string; has_token: boolean; vendored: boolean; rescan_hours: number; pinned: boolean; last_scan_id: number | null
  created_at: string; updated_at: string
  latest?: Scan; done?: Scan
}

export interface FileInfo {
  path: string; dir: string; lang: string; lines: number; code: number; bytes: number; package?: string
  imports: number; imported_by: number; symbols: number; todos: number; test?: boolean; generated?: boolean; parsed: boolean
  churn: number; added: number; deleted: number; authors: number; last_change?: string
}
export interface DirInfo { path: string; files: number; lines: number; lang: string; depends_on: number; used_by: number }
export interface Symbol {
  file: string; name: string; kind: string; line: number; end_line?: number; exported: boolean; parent?: string; signature?: string; doc?: string; refs: number
}
export interface Call { from: string; to: string; count: number }
export interface External { name: string; ecosystem: string; version?: string; dev?: boolean; files: number; declared: string }
export interface Endpoint {
  method: string; path: string; handler?: string; file: string; line: number; framework: string; source: 'code' | 'spec'; params?: string[]; summary?: string; tag?: string
}
export interface SpecFile { path: string; title: string; version: string; kind: string; endpoints: number }
export interface Column { name: string; type: string; pk?: boolean; not_null?: boolean; unique?: boolean; ref?: string }
export interface Table { name: string; file: string; line: number; source: string; columns: Column[] }
export interface EnvVar { name: string; uses: string[]; default?: string; documented: boolean }
export interface Service { name: string; image?: string; build?: string; ports?: string[]; depends_on?: string[]; file: string }
export interface EntryPoint { path: string; kind: string; detail?: string }
export interface LangStat { lang: string; files: number; lines: number; code: number }
export interface Author { name: string; commits: number }
export interface GitInfo { branch: string; head: string; head_date: string; head_message: string; remote?: string; commits: number; first_date: string; authors: Author[] }

export interface Hotspot { path: string; lang: string; lines: number; churn: number; authors: number; imported_by: number; todos: number; last_change: string; score: number }
export interface DirSum { path: string; files: number; lines: number; lang: string }

export interface Overview {
  scan: Scan; name: string; stats: Stats; languages: LangStat[]; entry_points: EntryPoint[]; externals: External[]; externals_total: number
  warnings: string[]; git: GitInfo | null; cycles: string[][]; top_dirs: DirSum[]; hotspots: Hotspot[]; largest: FileInfo[]; central: FileInfo[]
  methods: Record<string, number>; frameworks: Record<string, number>; spec_files: SpecFile[]; services: Service[]
}

export interface Note { id: number; codebase_id: number; path: string; kind: 'note' | 'ai'; text: string; scan_id: number | null; model: string; created_at: string; updated_at: string }

export interface Link { path: string; names?: string[]; count?: number }
export interface FileDetail {
  kind: 'file'; file: FileInfo; symbols: Symbol[]; imports: Link[]; imported_by: Link[]; endpoints: Endpoint[]
  env: { name: string; use: string; documented: boolean }[]; calls: Call[]; tables: Table[]; notes: Note[]
}
export interface DirDetail {
  kind: 'dir'; dir: DirInfo; files: FileInfo[]; subdirs: DirInfo[]; depends_on: Link[]; used_by: Link[]; endpoints: Endpoint[]; endpoints_total: number; symbols: number; notes: Note[]
}
export type PathDetail = FileDetail | DirDetail

export interface GNode { id: string; label: string; kind: 'dir' | 'file'; parent?: string; lang?: string; files: number; lines: number; in: number; out: number; cycle?: boolean; test?: boolean }
export interface GEdge { from: string; to: string; weight: number; cycle?: boolean }
export interface Graph { nodes: GNode[]; edges: GEdge[]; truncated: boolean; level: string; root: string; depth: number }

export interface SearchHit { codebase_id: number; codebase: string; scan_id: number; kind: 'file' | 'symbol' | 'endpoint' | 'table' | 'env'; title: string; detail: string; path: string; line?: number }

export interface Sources {
  path: { available: boolean; admin: boolean; roots: string[] }
  git: { available: boolean }
  files: { available: boolean; detail: string }
  upload: { available: boolean; max_mb: number }
}
