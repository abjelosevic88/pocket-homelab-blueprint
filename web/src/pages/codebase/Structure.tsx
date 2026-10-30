// The folder tree with sizes and connections, and a search over every function and type.
import { useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { api, V1 } from '../../lib/api'
import { base, compact } from '../../lib/code'
import type { DirInfo, FileInfo, Symbol } from '../../lib/types'
import { Icon, langColor } from '../../components/icons'
import { Loading, Segmented, useAsync, useDebounce } from '../../components/ui'
import { Select } from '../../components/Select'
import { useCB } from './Frame'
import { KIND_ICON } from './PathPanel'

interface Tree { dirs: DirInfo[]; files: FileInfo[] }
type Filter = 'all' | 'code' | 'orphans' | 'tests' | 'todos'

function Symbols() {
  const { scanId, source } = useCB()
  const [q, setQ] = useState('')
  const [kind, setKind] = useState('')
  const dq = useDebounce(q, 200)
  const { data } = useAsync(() => api.get<{ symbols: Symbol[]; total: number }>(`${V1}/scans/${scanId}/symbols?q=${encodeURIComponent(dq)}&kind=${kind}&limit=300`), [scanId, dq, kind])
  return (
    <div className="stack">
      <div className="row wrap filters">
        <div className="search-box"><Icon name="search" size={15} /><input autoFocus value={q} onChange={e => setQ(e.target.value)} placeholder="Find a function, type or class" aria-label="Find a symbol" /></div>
        <Select value={kind} onChange={setKind} ariaLabel="Kind" options={[{ value: '', label: 'Every kind' }, ...['func', 'method', 'struct', 'interface', 'type', 'class', 'component', 'enum', 'const', 'var', 'trait'].map(k => ({ value: k, label: k }))]} />
        {data && <span className="small muted tnum">{data.total.toLocaleString()} found</span>}
      </div>
      <div className="card"><ul className="list">
        {!data ? <li className="pad muted small">Loading…</li> : data.symbols.length === 0 ? <li className="pad muted small">Nothing matches.</li> : data.symbols.map(s => (
          <li key={`${s.file}:${s.line}:${s.name}`}><button type="button" className="list-row sym-hit" onClick={() => source(s.file, s.line)}>
            <Icon name={KIND_ICON[s.kind] || 'braces'} size={15} className="muted" />
            <span className="grow min0">
              <span className="block mono small ellipsis"><b>{s.parent ? `${s.parent}.` : ''}{s.name}</b>{!s.exported && <span className="faint"> · private</span>}</span>
              <span className="block xsmall muted ellipsis">{s.doc || s.signature || s.kind} — <span className="mono">{s.file}:{s.line}</span></span>
            </span>
            {s.refs > 0 && <span className="badge" title="Other files that use it">{s.refs}</span>}
          </button></li>))}
      </ul></div>
    </div>
  )
}

export default function Structure() {
  const { scanId, open } = useCB()
  const [sp] = useSearchParams()
  const [view, setView] = useState<'tree' | 'symbols'>('tree')
  const [filter, setFilter] = useState<Filter>((sp.get('filter') as Filter) || 'all')
  const [q, setQ] = useState('')
  const [expanded, setExpanded] = useState<Set<string>>(new Set(['']))
  const { data, error } = useAsync(() => api.get<Tree>(`${V1}/scans/${scanId}/tree`), [scanId])
  const dq = useDebounce(q.trim().toLowerCase(), 150)

  const { children, dirMap } = useMemo(() => {
    const children = new Map<string, { dirs: string[]; files: FileInfo[] }>()
    const dirMap = new Map<string, DirInfo>()
    if (!data) return { children, dirMap }
    const get = (p: string) => { let c = children.get(p); if (!c) { c = { dirs: [], files: [] }; children.set(p, c) } return c }
    for (const d of data.dirs) {
      dirMap.set(d.path, d)
      if (d.path === '') continue
      const parent = d.path.includes('/') ? d.path.slice(0, d.path.lastIndexOf('/')) : ''
      get(parent).dirs.push(d.path)
    }
    for (const f of data.files) get(f.dir).files.push(f)
    for (const c of children.values()) { c.dirs.sort(); c.files.sort((a, b) => a.path.localeCompare(b.path)) }
    return { children, dirMap }
  }, [data])

  const keep = (f: FileInfo) => {
    switch (filter) {
      case 'code': if (!f.lang || f.generated) return false; break
      case 'orphans': if (!f.lang || f.imported_by > 0 || f.test || f.generated || !['TypeScript', 'JavaScript', 'Python'].includes(f.lang)) return false; break
      case 'tests': if (!f.test) return false; break
      case 'todos': if (!f.todos) return false; break
    }
    return !dq || f.path.toLowerCase().includes(dq)
  }
  const flat = filter !== 'all' || !!dq
  const matches = useMemo(() => flat && data ? data.files.filter(keep).slice(0, 2000) : [], [flat, data, filter, dq]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { if (sp.get('filter')) setFilter(sp.get('filter') as Filter) }, [sp])

  if (error) return <p className="bad">{error}</p>
  if (!data) return <Loading />
  const toggle = (p: string) => setExpanded(s => { const n = new Set(s); if (n.has(p)) n.delete(p); else n.add(p); return n })
  const rows: React.ReactNode[] = []
  const walk = (dir: string, depth: number) => {
    const c = children.get(dir)
    if (!c) return
    for (const d of c.dirs) {
      const info = dirMap.get(d)!
      const isOpen = expanded.has(d)
      rows.push(
        <div key={'d' + d} className="tree-row" style={{ ['--depth' as string]: depth }}>
          <button type="button" className="tree-toggle" onClick={() => toggle(d)} aria-expanded={isOpen} aria-label={isOpen ? 'Close folder' : 'Open folder'}><Icon name={isOpen ? 'chevron-down' : 'chevron-right'} size={14} /></button>
          <button type="button" className="tree-name" onClick={() => toggle(d)} onDoubleClick={() => open(d)}><Icon name={isOpen ? 'folder-open' : 'folder'} size={15} className="tree-folder" /><span className="ellipsis">{base(d)}</span></button>
          <button type="button" className="tree-info" onClick={() => open(d)} title="Details"><Icon name="info" size={13} /></button>
          <span className="tree-n tnum">{compact(info.lines)}</span>
          <span className="tree-n tnum hide-sm" title="Folders it uses">{info.depends_on || ''}</span>
          <span className="tree-n tnum hide-sm" title="Folders that use it">{info.used_by || ''}</span>
          <span className="tree-n tnum hide-sm" />
        </div>)
      if (isOpen) walk(d, depth + 1)
    }
    for (const f of c.files) rows.push(<FileRow key={f.path} f={f} depth={depth} onOpen={() => open(f.path)} />)
  }
  if (!flat) walk('', 0)
  return (
    <div className="stack">
      <div className="row wrap">
        <Segmented value={view} onChange={setView} options={[{ value: 'tree', label: 'Folders and files' }, { value: 'symbols', label: 'Functions and types' }]} />
      </div>
      {view === 'symbols' ? <Symbols /> : <>
        <div className="row wrap filters">
          <div className="search-box"><Icon name="search" size={15} /><input value={q} onChange={e => setQ(e.target.value)} placeholder="Filter files by path" aria-label="Filter files" /></div>
          <Select value={filter} onChange={v => setFilter(v as Filter)} ariaLabel="Show" options={[
            { value: 'all', label: 'Every file' }, { value: 'code', label: 'Only code' }, { value: 'orphans', label: 'Nothing imports them' },
            { value: 'tests', label: 'Tests' }, { value: 'todos', label: 'With TODOs' }]} />
          {!flat && <button type="button" className="btn sm ghost" onClick={() => setExpanded(expanded.size > 1 ? new Set(['']) : new Set(data.dirs.map(d => d.path)))}>{expanded.size > 1 ? 'Close all' : 'Open all'}</button>}
          {flat && <span className="small muted tnum">{matches.length.toLocaleString()} files</span>}
        </div>
        <div className="card tree">
          <div className="tree-row tree-head">
            <span /><span className="tree-name-h">Name</span><span /><span className="tree-n">Lines</span><span className="tree-n hide-sm" title="Uses">Uses</span><span className="tree-n hide-sm" title="Used by">Used by</span><span className="tree-n hide-sm" title="Commits">Commits</span>
          </div>
          {flat ? matches.map(f => <FileRow key={f.path} f={f} depth={0} full onOpen={() => open(f.path)} />) : rows}
          {flat && matches.length === 0 && <p className="pad muted small">No files match.</p>}
        </div>
      </>}
    </div>
  )
}

function FileRow({ f, depth, onOpen, full }: { f: FileInfo; depth: number; onOpen: () => void; full?: boolean }) {
  return (
    <div className={`tree-row file ${f.generated ? 'gen' : ''}`} style={{ ['--depth' as string]: depth }}>
      <span />
      <button type="button" className="tree-name" onClick={onOpen} title={f.path}>
        <i className="ldot" style={{ background: langColor(f.lang) }} /><span className="ellipsis">{full ? f.path : base(f.path)}</span>
        {f.test && <span className="badge xs">test</span>}{f.todos > 0 && <span className="badge xs warn" title="TODO comments">{f.todos}</span>}
      </button>
      <span />
      <span className="tree-n tnum">{compact(f.lines)}</span>
      <span className="tree-n tnum hide-sm">{f.imports || ''}</span>
      <span className="tree-n tnum hide-sm">{f.imported_by || ''}</span>
      <span className="tree-n tnum hide-sm">{f.churn || ''}</span>
    </div>
  )
}
