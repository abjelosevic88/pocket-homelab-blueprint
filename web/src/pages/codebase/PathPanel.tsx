// The side panel for one file or folder: what is in it, what it uses, what uses it, notes and the AI's explanation.
import React, { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, V1 } from '../../lib/api'
import { base, dirOf, splitID } from '../../lib/code'
import { ago } from '../../lib/format'
import type { Call, DirDetail, FileDetail, Note, PathDetail, Symbol } from '../../lib/types'
import { Icon, langColor } from '../../components/icons'
import { errMsg, Spinner, useToast } from '../../components/ui'
import { useCB } from './Frame'

export const KIND_ICON: Record<string, string> = { func: 'braces', method: 'braces', type: 'box', struct: 'box', interface: 'layers', class: 'box', enum: 'list', const: 'variable', var: 'variable', component: 'grid', trait: 'layers', module: 'package' }

/** Light Markdown: paragraphs, bullets, **bold** and `code`. */
export function Md({ text }: { text: string }) {
  const inline = (s: string, k: string) => s.split(/(`[^`]+`|\*\*[^*]+\*\*)/g).map((p, i) =>
    p.startsWith('`') && p.endsWith('`') ? <code key={k + i}>{p.slice(1, -1)}</code> : p.startsWith('**') && p.endsWith('**') ? <b key={k + i}>{p.slice(2, -2)}</b> : <React.Fragment key={k + i}>{p}</React.Fragment>)
  const blocks = text.trim().split(/\n\s*\n/)
  return <div className="md">{blocks.map((b, i) => {
    const lines = b.split('\n')
    if (lines.every(l => /^\s*[-*] /.test(l))) return <ul key={i}>{lines.map((l, j) => <li key={j}>{inline(l.replace(/^\s*[-*] /, ''), `${i}-${j}-`)}</li>)}</ul>
    return <p key={i}>{inline(b, `${i}-`)}</p>
  })}</div>
}

export function SymbolRow({ s, onSource }: { s: Symbol; onSource: () => void }) {
  const { scanId, open } = useCB()
  const [more, setMore] = useState<{ callers: Call[]; callees: Call[]; imported_by: string[] } | null>(null)
  const [opened, setOpened] = useState(false)
  const toggle = async () => {
    setOpened(o => !o)
    if (!more) {
      const q = new URLSearchParams({ file: s.file, name: s.name, parent: s.parent || '' })
      api.get<{ callers: Call[]; callees: Call[]; imported_by: string[] }>(`${V1}/scans/${scanId}/symbol?${q}`).then(setMore).catch(() => setMore({ callers: [], callees: [], imported_by: [] }))
    }
  }
  const callLink = (id: string, n: number) => {
    const { file, name } = splitID(id)
    return <button type="button" key={id} className="call-chip" onClick={() => open(file)} title={file}><span className="mono">{name}</span>{n > 1 && <span className="faint">×{n}</span>}<span className="faint ellipsis">{base(file)}</span></button>
  }
  return (
    <li className={`sym ${s.exported ? '' : 'private'}`}>
      <div className="sym-row">
        <button type="button" className="sym-toggle" onClick={toggle} aria-expanded={opened} title="Who calls it, what it calls">
          <Icon name={opened ? 'chevron-down' : 'chevron-right'} size={13} />
        </button>
        <Icon name={KIND_ICON[s.kind] || 'braces'} size={14} className="muted" />
        <button type="button" className="sym-name" onClick={onSource} title={`Show line ${s.line}`}>
          <span className="mono">{s.parent ? <span className="faint">{s.parent}.</span> : null}{s.name}</span>
        </button>
        <span className="faint xsmall">{s.kind}</span>
        <span className="spacer" />
        {s.refs > 0 && <span className="badge" title="Other files that use it">{s.refs}</span>}
        <span className="faint xsmall tnum">:{s.line}</span>
      </div>
      {opened && <div className="sym-more">
        {s.signature && <code className="sig">{s.signature}</code>}
        {s.doc && <p className="small muted">{s.doc}</p>}
        {!more ? <Spinner /> : <>
          {more.callers.length > 0 && <div className="calls"><span className="xsmall muted">Called by</span>{more.callers.map(c => callLink(c.from, c.count))}</div>}
          {more.callees.length > 0 && <div className="calls"><span className="xsmall muted">Calls</span>{more.callees.map(c => callLink(c.to, c.count))}</div>}
          {more.imported_by.length > 0 && <div className="calls"><span className="xsmall muted">Imported by</span>{more.imported_by.map(f => <button type="button" key={f} className="call-chip" onClick={() => open(f)}>{base(f)}</button>)}</div>}
          {more.callers.length + more.callees.length + more.imported_by.length === 0 && <p className="xsmall faint">No calls found{s.file.endsWith('.go') ? '' : ' (calls are traced in Go code)'}.</p>}
        </>}
      </div>}
    </li>
  )
}

function Section({ title, count, children, open = true }: { title: string; count?: number; children: React.ReactNode; open?: boolean }) {
  return <details className="pp-sec" open={open}><summary><span>{title}</span>{count !== undefined && <span className="badge">{count}</span>}</summary>{children}</details>
}

function Notes({ path, notes, reload }: { path: string; notes: Note[]; reload: () => void }) {
  const { c, scanId } = useCB()
  const { toast } = useToast()
  const [text, setText] = useState('')
  const [asking, setAsking] = useState(false)
  const ai = notes.find(n => n.kind === 'ai' && n.path === path)
  const mine = notes.filter(n => n.kind === 'note')
  const explain = async () => {
    setAsking(true)
    try {
      const r = await api.post<{ ok: boolean; error?: string }>(`${V1}/scans/${scanId}/explain`, { path })
      if (!r.ok) toast(r.error || 'The AI did not answer', 'err')
      reload()
    } catch (e) { toast(errMsg(e), 'err') } finally { setAsking(false) }
  }
  const add = async () => {
    if (!text.trim()) return
    await api.post(`${V1}/codebases/${c.id}/notes`, { path, text })
    setText('')
    reload()
  }
  return <>
    <div className="pp-ai">
      <div className="row"><Icon name="sparkles" size={14} className="muted" /><b className="small grow">What it is for</b>
        <button type="button" className="btn sm ghost" onClick={explain} disabled={asking}>{asking ? <Spinner /> : <Icon name={ai ? 'refresh' : 'sparkles'} size={13} />}{ai ? 'Explain again' : 'Explain with AI'}</button></div>
      {ai ? <><Md text={ai.text} /><p className="xsmall faint">{ai.model === 'demo' ? 'Example explanation' : `Explained ${ago(ai.updated_at)}`}{ai.scan_id && ai.scan_id !== scanId ? ' from an older scan' : ''}</p></>
        : !asking && <p className="small muted">The AI reads the files, functions and connections here and explains them in a few lines.</p>}
    </div>
    <Section title="Your notes" count={mine.length} open={mine.length > 0}>
      <ul className="notes">{mine.map(n => (
        <li key={n.id}>{n.path !== path && <span className="xsmall faint mono">{n.path.split('#')[1]}</span>}<Md text={n.text} />
          <span className="row xsmall faint">{ago(n.updated_at)}<span className="spacer" /><button type="button" className="link-btn xsmall" onClick={async () => { await api.del(`${V1}/notes/${n.id}`); reload() }}>Delete</button></span></li>
      ))}</ul>
      <div className="note-add"><textarea rows={2} value={text} onChange={e => setText(e.target.value)} placeholder="Write a note about this…" onKeyDown={e => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) add() }} />
        <button type="button" className="btn sm" disabled={!text.trim()} onClick={add}>Add note</button></div>
    </Section>
  </>
}

function FileView({ d, reload }: { d: FileDetail; reload: () => void }) {
  const { open, source } = useCB()
  const f = d.file
  return <>
    <div className="pp-stats">
      <span><b className="tnum">{f.lines.toLocaleString()}</b> lines</span>
      <span><b className="tnum">{d.symbols.length}</b> symbols</span>
      <span><b className="tnum">{f.imports}</b> uses</span>
      <span><b className="tnum">{f.imported_by}</b> used by</span>
      {f.churn > 0 && <span title={`${f.added} lines added, ${f.deleted} removed`}><b className="tnum">{f.churn}</b> commits</span>}
      {f.authors > 0 && <span><b className="tnum">{f.authors}</b> {f.authors === 1 ? 'author' : 'authors'}</span>}
    </div>
    {f.last_change && <p className="xsmall muted">Last changed {ago(f.last_change)}{f.todos > 0 && ` · ${f.todos} TODO${f.todos > 1 ? 's' : ''}`}</p>}
    {!f.parsed && <p className="xsmall muted">Counted, not read: {f.lang ? 'too big or not a language Blueprint reads' : 'not code'}.</p>}
    <Notes path={f.path} notes={d.notes} reload={reload} />
    {d.symbols.length > 0 && <Section title="Functions and types" count={d.symbols.length}>
      <ul className="syms">{d.symbols.map(s => <SymbolRow key={`${s.parent}.${s.name}:${s.line}`} s={s} onSource={() => source(f.path, s.line)} />)}</ul>
    </Section>}
    {d.endpoints.length > 0 && <Section title="Endpoints here" count={d.endpoints.length}>
      <ul className="plain">{d.endpoints.map((e, i) => <li key={i}><button type="button" className="ep-line" onClick={() => source(f.path, e.line)}><span className={`method m-${e.method.toLowerCase()}`}>{e.method}</span><span className="mono ellipsis">{e.path}</span></button></li>)}</ul>
    </Section>}
    <Section title="Uses" count={d.imports.length} open={d.imports.length > 0 && d.imports.length < 25}>
      <ul className="plain">{d.imports.map(l => <li key={l.path}><button type="button" className="path-link" onClick={() => open(l.path)}><Icon name="file" size={13} />{base(l.path)}<span className="faint ellipsis">{dirOf(l.path)}</span></button>{l.names && l.names.length > 0 && <span className="names mono">{l.names.slice(0, 8).join(', ')}{l.names.length > 8 ? '…' : ''}</span>}</li>)}</ul>
      {d.imports.length === 0 && <p className="xsmall faint">No other files in this codebase.</p>}
    </Section>
    <Section title="Used by" count={d.imported_by.length} open={d.imported_by.length > 0 && d.imported_by.length < 25}>
      <ul className="plain">{d.imported_by.map(l => <li key={l.path}><button type="button" className="path-link" onClick={() => open(l.path)}><Icon name="file" size={13} />{base(l.path)}<span className="faint ellipsis">{dirOf(l.path)}</span></button></li>)}</ul>
      {d.imported_by.length === 0 && <p className="xsmall faint">Nothing imports this file{f.lang === 'Go' ? ' (Go imports whole folders)' : ''}.</p>}
    </Section>
    {d.env.length > 0 && <Section title="Environment variables" count={d.env.length}>
      <ul className="plain">{d.env.map(v => <li key={v.use}><button type="button" className="path-link" onClick={() => source(f.path, Number(v.use.split(':').pop()))}><Icon name="variable" size={13} /><span className="mono">{v.name}</span>{!v.documented && <span className="badge warn">not documented</span>}</button></li>)}</ul>
    </Section>}
    {d.tables.length > 0 && <Section title="Tables defined here" count={d.tables.length}>
      <ul className="plain">{d.tables.map(t => <li key={t.name}><button type="button" className="path-link" onClick={() => source(f.path, t.line)}><Icon name="database" size={13} /><span className="mono">{t.name}</span><span className="faint">{t.columns.length} columns</span></button></li>)}</ul>
    </Section>}
  </>
}

function DirView({ d, reload }: { d: DirDetail; reload: () => void }) {
  const { open, source } = useCB()
  return <>
    <div className="pp-stats">
      <span><b className="tnum">{d.dir.lines.toLocaleString()}</b> lines</span>
      <span><b className="tnum">{d.files.length}</b> files here</span>
      <span><b className="tnum">{d.subdirs.length}</b> folders</span>
      <span><b className="tnum">{d.symbols.toLocaleString()}</b> symbols</span>
      {d.endpoints_total > 0 && <span><b className="tnum">{d.endpoints_total}</b> endpoints</span>}
    </div>
    <Notes path={d.dir.path} notes={d.notes} reload={reload} />
    {d.subdirs.length > 0 && <Section title="Folders" count={d.subdirs.length}>
      <ul className="plain">{d.subdirs.map(s => <li key={s.path}><button type="button" className="path-link" onClick={() => open(s.path)}><Icon name="folder" size={13} />{base(s.path)}<span className="faint tnum">{s.lines.toLocaleString()} lines</span></button></li>)}</ul>
    </Section>}
    {d.files.length > 0 && <Section title="Files" count={d.files.length}>
      <ul className="plain">{d.files.map(f => <li key={f.path}><button type="button" className="path-link" onClick={() => open(f.path)}><i className="ldot" style={{ background: langColor(f.lang) }} />{base(f.path)}<span className="faint tnum">{f.lines.toLocaleString()}</span></button></li>)}</ul>
    </Section>}
    <Section title="Uses folders" count={d.depends_on.length} open={d.depends_on.length > 0}>
      <ul className="plain">{d.depends_on.map(l => <li key={l.path}><button type="button" className="path-link" onClick={() => open(l.path)}><Icon name="folder" size={13} />{l.path || '(top level)'}<span className="faint tnum">{l.count} imports</span></button></li>)}</ul>
      {d.depends_on.length === 0 && <p className="xsmall faint">Nothing outside this folder.</p>}
    </Section>
    <Section title="Used by folders" count={d.used_by.length} open={d.used_by.length > 0}>
      <ul className="plain">{d.used_by.map(l => <li key={l.path}><button type="button" className="path-link" onClick={() => open(l.path)}><Icon name="folder" size={13} />{l.path || '(top level)'}<span className="faint tnum">{l.count} imports</span></button></li>)}</ul>
      {d.used_by.length === 0 && <p className="xsmall faint">Nothing outside this folder uses it.</p>}
    </Section>
    {d.endpoints.length > 0 && <Section title="Endpoints" count={d.endpoints_total} open={false}>
      <ul className="plain">{d.endpoints.map((e, i) => <li key={i}><button type="button" className="ep-line" onClick={() => source(e.file, e.line)}><span className={`method m-${e.method.toLowerCase()}`}>{e.method}</span><span className="mono ellipsis">{e.path}</span></button></li>)}</ul>
    </Section>}
  </>
}

export function PathPanel({ path, onClose }: { path: string; onClose: () => void }) {
  const { scanId, open, source, c } = useCB()
  const navigate = useNavigate()
  const [d, setD] = useState<PathDetail | null>(null)
  const [err, setErr] = useState('')
  const load = () => api.get<PathDetail>(`${V1}/scans/${scanId}/file?path=${encodeURIComponent(path)}`).then(setD).catch(e => setErr(errMsg(e)))
  useEffect(() => { load() }, [path, scanId]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    const h = (e: KeyboardEvent) => { if (e.key === 'Escape' && !document.querySelector('.modal-backdrop')) onClose() }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [onClose])
  const isFile = d?.kind === 'file'
  const crumbs = path ? path.split('/') : []
  return (
    <aside className="pp" aria-label={path || c.name}>
      <header className="pp-head">
        <Icon name={isFile ? 'file' : 'folder-open'} size={18} className="muted" />
        <div className="grow min0">
          <h2 className="ellipsis">{path ? base(path) : c.name}</h2>
          <div className="pp-crumbs xsmall">
            <button type="button" className="link-btn" onClick={() => open('')}>{c.name}</button>
            {crumbs.slice(0, -1).map((p, i) => <React.Fragment key={i}><span className="faint">/</span><button type="button" className="link-btn" onClick={() => open(crumbs.slice(0, i + 1).join('/'))}>{p}</button></React.Fragment>)}
          </div>
        </div>
        <button type="button" className="btn ghost icon-only" onClick={onClose} aria-label="Close" title="Close (esc)"><Icon name="x" size={18} /></button>
      </header>
      <div className="pp-actions">
        {isFile && <button type="button" className="btn sm" onClick={() => source(path)}><Icon name="code" size={14} />Source</button>}
        <button type="button" className="btn sm" onClick={() => navigate(`/c/${c.id}/graph?focus=${encodeURIComponent(path)}${isFile ? '&level=file' : ''}`)}><Icon name="graph" size={14} />In the graph</button>
        {isFile && d.file.lang && <span className="badge" style={{ background: `color-mix(in srgb, ${langColor(d.file.lang)} 18%, transparent)` }}>{d.file.lang}</span>}
        {isFile && d.file.test && <span className="badge">test</span>}
        {isFile && d.file.generated && <span className="badge">generated</span>}
      </div>
      <div className="pp-body">
        {err ? <p className="bad small">{err}</p> : !d ? <p className="muted small"><Spinner /> Loading…</p>
          : d.kind === 'file' ? <FileView d={d} reload={load} /> : <DirView d={d} reload={load} />}
      </div>
    </aside>
  )
}
