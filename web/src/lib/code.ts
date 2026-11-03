// Small helpers for the inspector pages.
import type { Codebase, Scan } from './types'

export const SOURCE_LABEL: Record<string, string> = { path: 'Server folder', git: 'Git repository', upload: 'Uploaded archive', files: 'Pocket Files' }
export const SOURCE_ICON: Record<string, string> = { path: 'server', git: 'git-branch', upload: 'archive', files: 'folder' }

/** Whether the person may scan a codebase again: its owner, or shared with them to scan. */
export const mayScan = (c: Codebase) => c.role !== 'view'

export function where(c: Codebase): string {
  let w = c.location
  if (c.source === 'files') w = w.replace(/^home:/, 'My files: ').replace(/^mount:\d+:/, '').replace(/^share:\d+:/, 'Shared: ')
  if (c.source === 'git') w = w.replace(/^https?:\/\//, '').replace(/\.git$/, '')
  if (c.branch) w += ` @ ${c.branch}`
  if (c.subdir) w += ` / ${c.subdir}`
  return w
}

export function num(n: number | undefined): string { return (n || 0).toLocaleString() }

export function compact(n: number | undefined): string {
  const v = n || 0
  if (v >= 1_000_000) return (v / 1_000_000).toFixed(v >= 10_000_000 ? 0 : 1) + 'M'
  if (v >= 10_000) return Math.round(v / 1000) + 'k'
  if (v >= 1000) return (v / 1000).toFixed(1) + 'k'
  return String(v)
}

export function dur(ms: number): string {
  if (ms < 1000) return `${ms} ms`
  const s = ms / 1000
  if (s < 60) return `${s.toFixed(s < 10 ? 1 : 0)} s`
  return `${Math.floor(s / 60)} min ${Math.round(s % 60)} s`
}

export function scanLabel(s?: Scan): string {
  if (!s) return 'Not scanned yet'
  switch (s.status) {
    case 'queued': return 'Waiting to scan'
    case 'running': return s.stage || 'Scanning'
    case 'failed': return 'Scan failed'
    case 'cancelled': return 'Scan stopped'
  }
  return 'Scanned'
}

export function base(p: string): string { return p.slice(p.lastIndexOf('/') + 1) || p }
export function dirOf(p: string): string { const i = p.lastIndexOf('/'); return i < 0 ? '' : p.slice(0, i) }

export const METHOD_ORDER = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS', 'ANY']

/** "file#Type.Method" → { file, name } */
export function splitID(id: string): { file: string; name: string } {
  const i = id.indexOf('#')
  return i < 0 ? { file: id, name: '' } : { file: id.slice(0, i), name: id.slice(i + 1) }
}
