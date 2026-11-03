// Every scan of the codebase, and what changed between two of them.
import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, V1 } from '../../lib/api'
import { compact, dur } from '../../lib/code'
import { ago, fmtDateTime } from '../../lib/format'
import type { Endpoint, External, Scan, Stats } from '../../lib/types'
import { Icon } from '../../components/icons'
import { Card, Confirm, Loading, useAsync } from '../../components/ui'
import { Select } from '../../components/Select'
import { useCB } from './Frame'
import { useApp } from '../../lib/app-context'

interface Diff {
  before: Scan; after: Scan; stats: { before: Stats; after: Stats }
  files: { added: string[]; removed: string[]; changed: { path: string; before: number; after: number }[]; changed_total: number }
  endpoints: { added: Endpoint[]; removed: Endpoint[] }
  tables: { added: string[]; removed: string[]; changed: { name: string; added: string[]; removed: string[] }[] }
  externals: { added: External[]; removed: External[]; changed: { name: string; ecosystem: string; before: string; after: string }[] }
  env: { added: string[]; removed: string[] }
  cycles: { before: number; after: number }
}

const TRIGGER: Record<string, string> = { manual: 'by you', auto: 'on schedule', new: 'when added' }
const STATUS: Record<string, string> = { done: 'Done', failed: 'Failed', cancelled: 'Stopped', queued: 'Waiting', running: 'Running' }

function Delta({ a, b }: { a: number; b: number }) {
  const d = b - a
  if (!d) return <span className="faint">—</span>
  return <span className={d > 0 ? 'ok' : 'bad'}>{d > 0 ? '+' : ''}{compact(d)}</span>
}

function Compare({ a, b }: { a: number; b: number }) {
  const { open } = useCB()
  const { data: d, error } = useAsync(() => api.get<Diff>(`${V1}/scans/${b}/compare/${a}`), [a, b])
  if (error) return <p className="bad">{error}</p>
  if (!d) return <Loading />
  const keys: [keyof Stats, string][] = [['files', 'Files'], ['lines', 'Lines'], ['symbols', 'Symbols'], ['endpoints', 'Endpoints'], ['tables', 'Tables'], ['env_vars', 'Env variables'], ['externals', 'Packages'], ['cycles', 'Loops']]
  const nothing = !d.files.added.length && !d.files.removed.length && !d.files.changed_total
  return (
    <div className="stack">
      <div className="stats-row tight">{keys.map(([k, l]) => (
        <div key={k} className="stat"><span className="stat-l">{l}</span><span className="stat-v tnum">{compact(d.stats.after[k])}</span><span className="small tnum"><Delta a={d.stats.before[k]} b={d.stats.after[k]} /></span></div>
      ))}</div>
      {nothing && <p className="muted">Nothing changed between these two scans.</p>}
      {(d.endpoints.added.length > 0 || d.endpoints.removed.length > 0) && <Card title="Endpoints" flush><ul className="list">
        {d.endpoints.added.map(e => <li key={'a' + e.method + e.path} className="list-row"><span className="diff add">+</span><span className={`method m-${e.method.toLowerCase()}`}>{e.method}</span><span className="mono small ellipsis">{e.path}</span></li>)}
        {d.endpoints.removed.map(e => <li key={'r' + e.method + e.path} className="list-row"><span className="diff del">−</span><span className={`method m-${e.method.toLowerCase()}`}>{e.method}</span><span className="mono small ellipsis">{e.path}</span></li>)}
      </ul></Card>}
      {(d.tables.added.length + d.tables.removed.length + d.tables.changed.length > 0) && <Card title="Tables" flush><ul className="list">
        {d.tables.added.map(t => <li key={'a' + t} className="list-row"><span className="diff add">+</span><span className="mono small">{t}</span></li>)}
        {d.tables.removed.map(t => <li key={'r' + t} className="list-row"><span className="diff del">−</span><span className="mono small">{t}</span></li>)}
        {d.tables.changed.map(t => <li key={'c' + t.name} className="list-row"><span className="diff chg">~</span><span className="mono small">{t.name}</span><span className="small muted">{t.added.length > 0 && `+ ${t.added.join(', ')}`} {t.removed.length > 0 && `− ${t.removed.join(', ')}`}</span></li>)}
      </ul></Card>}
      {(d.files.added.length + d.files.removed.length + d.files.changed_total > 0) && <Card title="Files" sub={`${d.files.added.length} added, ${d.files.removed.length} removed, ${d.files.changed_total} changed`} flush><ul className="list">
        {d.files.added.slice(0, 100).map(f => <li key={'a' + f}><button type="button" className="list-row" onClick={() => open(f)}><span className="diff add">+</span><span className="mono small ellipsis">{f}</span></button></li>)}
        {d.files.removed.slice(0, 100).map(f => <li key={'r' + f} className="list-row"><span className="diff del">−</span><span className="mono small ellipsis">{f}</span></li>)}
        {d.files.changed.slice(0, 100).map(f => <li key={'c' + f.path}><button type="button" className="list-row" onClick={() => open(f.path)}><span className="diff chg">~</span><span className="mono small ellipsis grow">{f.path}</span><span className="small tnum"><Delta a={f.before} b={f.after} /> lines</span></button></li>)}
      </ul></Card>}
      {(d.externals.added.length + d.externals.removed.length + d.externals.changed.length > 0) && <Card title="Outside packages" flush><ul className="list">
        {d.externals.added.map(x => <li key={'a' + x.name} className="list-row"><span className="diff add">+</span><span className="mono small">{x.name}</span><span className="small muted">{x.version}</span></li>)}
        {d.externals.removed.map(x => <li key={'r' + x.name} className="list-row"><span className="diff del">−</span><span className="mono small">{x.name}</span></li>)}
        {d.externals.changed.map(x => <li key={'c' + x.name} className="list-row"><span className="diff chg">~</span><span className="mono small">{x.name}</span><span className="small muted">{x.before} → {x.after}</span></li>)}
      </ul></Card>}
      {(d.env.added.length + d.env.removed.length > 0) && <Card title="Environment variables" flush><ul className="list">
        {d.env.added.map(v => <li key={'a' + v} className="list-row"><span className="diff add">+</span><span className="mono small">{v}</span></li>)}
        {d.env.removed.map(v => <li key={'r' + v} className="list-row"><span className="diff del">−</span><span className="mono small">{v}</span></li>)}
      </ul></Card>}
    </div>
  )
}

export default function Scans() {
  const { c, scanId, reload } = useCB()
  const { settings } = useApp()
  const navigate = useNavigate()
  const { data, reload: again } = useAsync(() => api.get<Scan[]>(`${V1}/codebases/${c.id}/scans`), [c.id, c.latest?.id, c.latest?.status])
  const done = (data || []).filter(s => s.status === 'done')
  const [a, setA] = useState('')
  const [b, setB] = useState('')
  const [del, setDel] = useState<Scan | null>(null)
  useEffect(() => {
    if (done.length >= 2 && !a && !b) { setB(String(done[0].id)); setA(String(done[1].id)) }
  }, [done.length]) // eslint-disable-line react-hooks/exhaustive-deps
  if (!data) return <Loading />
  const opts = done.map(s => ({ value: String(s.id), label: `${fmtDateTime(s.finished_at)}${s.commit ? ` · ${s.commit}` : ''}` }))
  return (
    <div className="stack loose">
      <Card title="Scans" sub={`The last ${settings?.keep_scans ?? 10} finished scans are kept; change it in Settings → Scanning.`} flush>
        <ul className="list">{data.map(s => {
          const st = s.stats as Partial<Stats>
          return (
            <li key={s.id} className={`list-row scan-row ${s.id === scanId ? 'cur' : ''}`}>
              <span className={`scan-dot ${s.status}`} title={STATUS[s.status]} />
              <span className="grow min0">
                <span className="block small"><b>{s.finished_at ? fmtDateTime(s.finished_at) : fmtDateTime(s.created_at)}</b> <span className="muted">{STATUS[s.status]} {s.trigger.startsWith('link:') ? `by Pocket ${s.trigger.slice(5)}` : TRIGGER[s.trigger] || ''}</span></span>
                <span className="block xsmall muted ellipsis">
                  {s.status === 'done' ? <>{compact(st.files)} files · {compact(st.lines)} lines · {st.endpoints ?? 0} endpoints{s.commit && <> · <span className="mono">{s.commit}</span></>} · took {dur(s.duration_ms)}</> : s.error || s.stage || ago(s.created_at)}
                </span>
              </span>
              {s.status === 'done' && s.id !== scanId && <button type="button" className="btn sm" onClick={() => navigate(`/c/${c.id}?scan=${s.id}`)}>Look at it</button>}
              {s.id === scanId && <span className="badge accent-soft">shown</span>}
              {c.role === 'owner' && s.status !== 'running' && s.status !== 'queued' && <button type="button" className="btn sm ghost icon-only" aria-label="Delete scan" title="Delete" onClick={() => setDel(s)}><Icon name="trash" size={14} /></button>}
            </li>
          )
        })}</ul>
      </Card>
      {done.length >= 2 ? <Card title="What changed" actions={<div className="row wrap compare-pick">
        <Select value={a} onChange={setA} options={opts} ariaLabel="Before" /><Icon name="chevron-right" size={14} className="muted" /><Select value={b} onChange={setB} options={opts} ariaLabel="After" />
      </div>}>
        {a && b && a !== b ? <Compare a={Number(a)} b={Number(b)} /> : <p className="muted small">Pick two different scans.</p>}
      </Card> : <p className="muted small">Scan again later to compare what changed.</p>}
      {del && <Confirm title="Delete this scan?" message="Only what Blueprint found in that scan is deleted." confirmLabel="Delete" onCancel={() => setDel(null)}
        onConfirm={async () => { await api.del(`${V1}/scans/${del.id}`); setDel(null); again(); reload(); if (del.id === scanId) navigate(`/c/${c.id}/scans`) }} />}
    </div>
  )
}
