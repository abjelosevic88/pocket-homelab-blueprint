// Database tables from migrations and schema files, as a list or a diagram of how they refer to each other.
import { useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { api, V1 } from '../../lib/api'
import type { Table } from '../../lib/types'
import { Icon } from '../../components/icons'
import { Empty, Loading, Segmented, useAsync, useIsMobile } from '../../components/ui'
import { GraphCanvas, type CanvasEdge, type CanvasNode } from '../../components/GraphCanvas'
import { useCB } from './Frame'

export default function Data() {
  const { scanId, source } = useCB()
  const mobile = useIsMobile()
  const [sp] = useSearchParams()
  const want = sp.get('table') || ''
  const { data, error } = useAsync(() => api.get<{ tables: Table[]; relations: { from: string; to: string }[] }>(`${V1}/scans/${scanId}/data`), [scanId])
  const [view, setView] = useState<'list' | 'diagram'>('list')
  const [q, setQ] = useState('')
  useEffect(() => {
    if (!want || !data) return
    requestAnimationFrame(() => document.getElementById(`t-${want}`)?.scrollIntoView({ block: 'start', behavior: 'smooth' }))
  }, [want, data])
  const { nodes, edges } = useMemo(() => {
    if (!data) return { nodes: [] as CanvasNode[], edges: [] as CanvasEdge[] }
    const accent = getComputedStyle(document.documentElement).getPropertyValue('--accent').trim()
    const names = new Set(data.tables.map(t => t.name))
    const nodes: CanvasNode[] = data.tables.map(t => ({ id: t.name, label: `${t.name} (${t.columns.length})`, color: accent, size: 18 + Math.min(30, t.columns.length * 2) }))
    const edges: CanvasEdge[] = data.relations.map(r => ({ from: r.from.split('.')[0], to: r.to.split('.')[0], width: 1.5, label: r.from.split('.')[1] }))
      .filter(e => names.has(e.from) && names.has(e.to) && e.from !== e.to)
    return { nodes, edges }
  }, [data])
  if (error) return <p className="bad">{error}</p>
  if (!data) return <Loading />
  if (!data.tables.length) return <Empty title="No database tables found" icon="database">Blueprint reads <code>CREATE TABLE</code> and <code>ALTER TABLE</code> in .sql files (migrations, in order) and Prisma schemas.</Empty>
  const n = q.trim().toLowerCase()
  const shown = data.tables.filter(t => !n || t.name.toLowerCase().includes(n) || t.columns.some(c => c.name.toLowerCase().includes(n)))
  const refsTo = (name: string) => data.relations.filter(r => r.to.split('.')[0] === name).map(r => r.from)
  return (
    <div className="stack">
      <div className="row wrap filters">
        <Segmented size="sm" value={view} onChange={setView} options={[{ value: 'list', label: 'Tables' }, { value: 'diagram', label: 'Diagram' }]} />
        {view === 'list' && <div className="search-box"><Icon name="search" size={15} /><input value={q} onChange={e => setQ(e.target.value)} placeholder="Filter tables and columns" aria-label="Filter tables" /></div>}
        <span className="small muted tnum">{data.tables.length} tables, {data.relations.length} references</span>
      </div>
      {view === 'diagram'
        ? <div className="card graph-card"><GraphCanvas nodes={nodes} edges={edges} layout="layers" direction={mobile ? 'TB' : 'LR'} tableMode height={mobile ? 460 : 'calc(100dvh - 300px)'}
          onTap={id => { setView('list'); requestAnimationFrame(() => document.getElementById(`t-${id}`)?.scrollIntoView({ block: 'start' })) }} /></div>
        : <div className="tables-grid">{shown.map(t => {
          const back = refsTo(t.name)
          return (
            <section key={t.name} id={`t-${t.name}`} className={`card table-card ${want === t.name ? 'hl' : ''}`}>
              <header className="table-head">
                <Icon name="database" size={15} className="muted" /><b className="mono grow ellipsis">{t.name}</b>
                <button type="button" className="link-btn xsmall mono" onClick={() => source(t.file, t.line)} title="Where it is defined">{t.file.split('/').pop()}:{t.line}</button>
              </header>
              <table className="table compact cols"><tbody>{t.columns.map(c => (
                <tr key={c.name}>
                  <td className="mono">{c.pk ? <Icon name="key" size={12} className="pk" /> : null}{c.name}</td>
                  <td className="mono muted small">{c.type}</td>
                  <td className="small right">{c.not_null && !c.pk && <span className="faint">required </span>}{c.unique && <span className="faint">unique </span>}
                    {c.ref && <a href={`#t-${c.ref.split('.')[0]}`} className="mono ref">→ {c.ref}</a>}</td>
                </tr>))}</tbody></table>
              {back.length > 0 && <p className="xsmall muted pad-x">Referred to by {back.map((b, i) => <span key={b}>{i > 0 && ', '}<a href={`#t-${b.split('.')[0]}`} className="mono">{b}</a></span>)}</p>}
            </section>
          )
        })}</div>}
    </div>
  )
}
