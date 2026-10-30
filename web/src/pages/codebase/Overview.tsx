// The headline: numbers, languages, the top folders, entry points, what to look at, history and outside packages.
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../../lib/api'
import { compact, dur, num } from '../../lib/code'
import { ago, fmtDateTime } from '../../lib/format'
import type { Note, Overview as OV } from '../../lib/types'
import { Icon, langColor } from '../../components/icons'
import { Card, errMsg, Loading, Spinner, useAsync, useToast } from '../../components/ui'
import { LangBar } from '../../components/LangBar'
import { useCB } from './Frame'
import { Md } from './PathPanel'

function Tile({ label, value, icon, to, title }: { label: string; value: number; icon: string; to?: string; title?: string }) {
  const body = <><span className="stat-l"><Icon name={icon} size={13} />{label}</span><span className="stat-v tnum" title={value.toLocaleString()}>{compact(value)}</span></>
  return to ? <Link to={to} className="stat link" title={title}>{body}</Link> : <div className="stat" title={title}>{body}</div>
}

function Ask() {
  const { scanId, c, open } = useCB()
  const { toast } = useToast()
  const notes = useAsync(() => api.get<Note[]>(`${V1}/codebases/${c.id}/notes?path=`), [c.id])
  const [q, setQ] = useState('')
  const [answer, setAnswer] = useState('')
  const [busy, setBusy] = useState<'' | 'ask' | 'explain'>('')
  const ai = notes.data?.find(n => n.kind === 'ai' && n.path === '')
  const ask = async () => {
    if (!q.trim()) return
    setBusy('ask'); setAnswer('')
    try {
      const r = await api.post<{ ok: boolean; text?: string; error?: string }>(`${V1}/scans/${scanId}/ask`, { question: q })
      if (r.ok) setAnswer(r.text || ''); else toast(r.error || 'The AI did not answer', 'err')
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy('') }
  }
  const explain = async () => {
    setBusy('explain')
    try {
      const r = await api.post<{ ok: boolean; error?: string }>(`${V1}/scans/${scanId}/explain`, { path: '' })
      if (!r.ok) toast(r.error || 'The AI did not answer', 'err')
      notes.reload()
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy('') }
  }
  return (
    <Card title="In a few words" actions={<button type="button" className="btn sm ghost" onClick={explain} disabled={!!busy}>{busy === 'explain' ? <Spinner /> : <Icon name={ai ? 'refresh' : 'sparkles'} size={13} />}{ai ? 'Explain again' : 'Explain with AI'}</button>}>
      <div className="stack tight">
        {ai ? <><Md text={ai.text} /><span className="xsmall faint">{ai.model === 'demo' ? 'Example explanation' : `Explained ${ago(ai.updated_at)}`}</span></>
          : <p className="muted small">Let the AI read the structure, the main files and the connections and explain this codebase. Or ask it something below.</p>}
        <div className="ask-row">
          <input value={q} onChange={e => setQ(e.target.value)} onKeyDown={e => { if (e.key === 'Enter') ask() }} placeholder="Ask about this code: “Where are payments handled?”" aria-label="Ask about this code" />
          <button type="button" className="btn" onClick={ask} disabled={!q.trim() || !!busy}>{busy === 'ask' ? <Spinner /> : <Icon name="sparkles" size={14} />}Ask</button>
        </div>
        {answer && <div className="recap"><Md text={answer} /></div>}
        {!!notes.data?.filter(n => n.kind === 'note').length && <p className="xsmall muted">{notes.data.filter(n => n.kind === 'note').length} notes on this codebase — <button type="button" className="link-btn xsmall" onClick={() => open('')}>see them</button></p>}
      </div>
    </Card>
  )
}

export default function Overview() {
  const { c, scanId, open, source } = useCB()
  const { data: o, error } = useAsync(() => api.get<OV>(`${V1}/scans/${scanId}/overview`), [scanId])
  if (error) return <p className="bad">{error}</p>
  if (!o) return <Loading />
  const s = o.stats
  const to = (tab: string) => `/c/${c.id}/${tab}`
  const maxDir = Math.max(1, ...o.top_dirs.map(d => d.lines))
  const looks: { icon: string; text: React.ReactNode; to?: string }[] = []
  if (o.cycles.length) looks.push({ icon: 'loop', text: <>{o.cycles.length} {o.cycles.length === 1 ? 'loop' : 'loops'} between folders that import each other: {o.cycles.slice(0, 2).map(cy => <span key={cy.join()} className="mono"> {cy.join(' ↔ ')}</span>)}</>, to: to('graph') })
  if (s.orphans) looks.push({ icon: 'file', text: <>{s.orphans} code {s.orphans === 1 ? 'file' : 'files'} nothing imports (maybe unused)</>, to: to('structure') + '?filter=orphans' })
  if (s.todos) looks.push({ icon: 'check-square', text: <>{s.todos} TODO / FIXME comments</>, to: to('hotspots') + '?sort=todos' })
  if (o.hotspots[0] && o.git) looks.push({ icon: 'flame', text: <>Changes most: <span className="mono">{o.hotspots[0].path}</span> ({o.hotspots[0].churn} commits)</>, to: to('hotspots') })
  for (const w of o.warnings) looks.push({ icon: 'info', text: w })
  return (
    <div className="stack loose">
      <div className="stats-row eight">
        <Tile label="Files" value={s.files} icon="file" to={to('structure')} title={`${num(s.code_files)} with code, ${num(s.tests)} tests`} />
        <Tile label="Lines" value={s.lines} icon="text" title={`${num(s.code)} lines of code`} />
        <Tile label="Folders" value={s.dirs} icon="folder" to={to('structure')} />
        <Tile label="Symbols" value={s.symbols} icon="braces" to={to('structure')} />
        <Tile label="Endpoints" value={s.endpoints} icon="api" to={to('api')} />
        <Tile label="Tables" value={s.tables} icon="database" to={to('data')} />
        <Tile label="Env variables" value={s.env_vars} icon="variable" to={to('config')} />
        <Tile label="Packages" value={s.externals} icon="package" to={to('config')} title="Outside packages it uses" />
      </div>
      <div className="home-grid">
        <div className="stack">
          <Card title="Languages">
            {o.languages.length ? <LangBar langs={o.languages} legend height={10} max={8} /> : <p className="muted small">No code found.</p>}
          </Card>
          <Card title="Structure" sub="The folders at the top, by lines" flush>
            <ul className="list">{o.top_dirs.map(d => (
              <li key={d.path}><button type="button" className="list-row dir-row" onClick={() => !d.path.startsWith('(') && open(d.path)}>
                <Icon name="folder" size={15} className="muted" />
                <span className="mono ellipsis dir-name">{d.path}</span>
                <span className="dir-bar"><span style={{ width: `${d.lines / maxDir * 100}%`, background: langColor(d.lang) }} /></span>
                <span className="tnum small muted dir-n">{compact(d.lines)}</span>
              </button></li>))}</ul>
          </Card>
          {o.entry_points.length > 0 && <Card title="Where it starts" flush>
            <ul className="list">{o.entry_points.map((e, i) => (
              <li key={i}><button type="button" className="list-row" onClick={() => open(e.path)}>
                <Icon name="entry" size={15} className="muted" /><span className="grow min0"><span className="block small"><b>{e.kind}</b> <span className="mono">{e.path || '.'}</span></span>{e.detail && <span className="block xsmall muted ellipsis mono">{e.detail}</span>}</span>
              </button></li>))}</ul>
          </Card>}
        </div>
        <div className="stack">
          <Ask />
          {looks.length > 0 && <Card title="Worth a look" flush>
            <ul className="list">{looks.map((l, i) => {
              const inner = <><Icon name={l.icon} size={15} className="muted" /><span className="grow small">{l.text}</span>{l.to && <Icon name="chevron-right" size={14} className="faint" />}</>
              return <li key={i}>{l.to ? <Link to={l.to} className="list-row">{inner}</Link> : <div className="list-row">{inner}</div>}</li>
            })}</ul>
          </Card>}
          {o.central.length > 0 && <Card title="Used the most" sub="Files many others import" flush>
            <ul className="list">{o.central.map(f => (
              <li key={f.path}><button type="button" className="list-row" onClick={() => open(f.path)}>
                <i className="ldot" style={{ background: langColor(f.lang) }} /><span className="mono ellipsis grow small">{f.path}</span><span className="badge" title="Files importing it">{f.imported_by}</span>
              </button></li>))}</ul>
          </Card>}
          {o.git && <Card title="History" sub={`${o.git.commits.toLocaleString()} commits read, since ${fmtDateTime(o.git.first_date).split(',')[0]}`}>
            <div className="stack tight">
              <div className="row small"><Icon name="git-branch" size={14} className="muted" /><span className="mono">{o.git.branch}</span><span className="faint">·</span><span className="mono">{o.git.head}</span><span className="muted ellipsis grow">{o.git.head_message}</span></div>
              {o.git.remote && <div className="small muted mono ellipsis">{o.git.remote}</div>}
              <div className="chips">{o.git.authors.slice(0, 6).map(a => <span key={a.name} className="chip static">{a.name} <span className="faint tnum">{a.commits}</span></span>)}</div>
            </div>
          </Card>}
          {o.externals.length > 0 && <Card title="Outside packages" sub={`The ${o.externals.length} used most of ${o.externals_total}`} actions={<Link to={to('config')} className="btn sm ghost">All</Link>}>
            <div className="chips">{o.externals.map(x => <span key={x.ecosystem + x.name} className="chip static" title={`${x.ecosystem}${x.version ? ' ' + x.version : ''} · ${x.files} files`}><span className="mono">{x.name}</span>{x.files > 0 && <span className="faint tnum">{x.files}</span>}</span>)}</div>
          </Card>}
          {o.largest.length > 0 && <Card title="Largest files" flush>
            <ul className="list">{o.largest.map(f => (
              <li key={f.path}><button type="button" className="list-row" onClick={() => source(f.path)}>
                <i className="ldot" style={{ background: langColor(f.lang) }} /><span className="mono ellipsis grow small">{f.path}</span><span className="tnum small muted">{f.lines.toLocaleString()}</span>
              </button></li>))}</ul>
          </Card>}
        </div>
      </div>
      <p className="xsmall faint">Scan took {dur(o.scan.duration_ms)}{s.skipped ? `, ${s.skipped.toLocaleString()} files left out (binaries, limits, ignored folders)` : ''}.</p>
    </div>
  )
}
