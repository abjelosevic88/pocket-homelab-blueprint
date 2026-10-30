// Which folder (or file) imports which, as a graph. Loops are red; click for details, double-click a folder to go inside.
import { useEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { api, V1 } from '../../lib/api'
import { useApp } from '../../lib/app-context'
import { base, compact } from '../../lib/code'
import type { DirInfo, FileInfo, Graph } from '../../lib/types'
import { Icon, langColor } from '../../components/icons'
import { Loading, Segmented, useAsync, useIsMobile } from '../../components/ui'
import { Select } from '../../components/Select'
import { SwitchLine } from '../../components/SwitchLine'
import { GraphCanvas, type CanvasEdge, type CanvasNode, type GraphHandle } from '../../components/GraphCanvas'
import { useCB } from './Frame'

export default function GraphPage() {
  const { scanId, open } = useCB()
  const { settings } = useApp()
  const mobile = useIsMobile()
  const [sp, setSp] = useSearchParams()
  const focus = sp.get('focus') || ''
  const [level, setLevel] = useState<'dir' | 'file'>((sp.get('level') as 'file') || 'dir')
  const [depth, setDepth] = useState(Number(sp.get('depth')) || 2)
  const [root, setRoot] = useState(sp.get('root') || '')
  const [radius, setRadius] = useState(1)
  const [tests, setTests] = useState(settings ? !settings.hide_tests : false)
  const [layout, setLayout] = useState<'layers' | 'organic'>(settings?.graph_layout || 'layers')
  const canvas = useRef<GraphHandle>(null)
  const tree = useAsync(() => api.get<{ dirs: DirInfo[]; files: FileInfo[] }>(`${V1}/scans/${scanId}/tree`), [scanId])
  const q = new URLSearchParams({ level, depth: String(depth), root, tests: tests ? '1' : '0' })
  if (focus) { q.set('focus', focus); q.set('radius', String(radius)) }
  const { data: g, error, loading } = useAsync(() => api.get<Graph>(`${V1}/scans/${scanId}/graph?${q}`), [scanId, q.toString()])
  useEffect(() => { if (sp.get('level')) setLevel(sp.get('level') as 'dir' | 'file') }, [sp])

  const { nodes, edges } = useMemo(() => {
    if (!g) return { nodes: [] as CanvasNode[], edges: [] as CanvasEdge[] }
    const maxLines = Math.max(1, ...g.nodes.map(n => n.lines))
    const nodes: CanvasNode[] = g.nodes.map(n => ({
      id: n.id, label: n.kind === 'file' ? base(n.id) : (n.id === g.root ? (n.label || '.') : n.id.slice(g.root ? g.root.length + 1 : 0) || n.label),
      color: langColor(n.lang), size: 14 + Math.sqrt(n.lines / maxLines) * (n.kind === 'file' ? 26 : 44), cycle: n.cycle, focus: !!focus && n.id === focus, kind: n.kind,
      parent: undefined,
    }))
    const maxW = Math.max(1, ...g.edges.map(e => e.weight))
    const edges: CanvasEdge[] = g.edges.map(e => ({ from: e.from, to: e.to, width: 1 + (e.weight / maxW) * 4, cycle: e.cycle, label: level === 'dir' && e.weight > 1 && g.edges.length < 80 ? String(e.weight) : '' }))
    return { nodes, edges }
  }, [g, focus, level])

  const dirOptions = useMemo(() => [{ value: '', label: 'The whole codebase' }, ...(tree.data?.dirs || []).filter(d => d.path && d.files + (d.lines > 0 ? 1 : 0) > 0).map(d => ({ value: d.path, label: d.path, hint: compact(d.lines) }))], [tree.data])
  const langs = useMemo(() => [...new Set((g?.nodes || []).map(n => n.lang).filter(Boolean))].slice(0, 8) as string[], [g])
  const setFocus = (f: string) => { const n = new URLSearchParams(sp); if (f) n.set('focus', f); else n.delete('focus'); setSp(n, { replace: true }) }
  const drill = (id: string) => {
    if (level !== 'dir') return
    setRoot(id)
    if (focus) setFocus('')
  }
  return (
    <div className="stack">
      <div className="row wrap filters graph-tools">
        <Segmented size="sm" value={level} onChange={setLevel} options={[{ value: 'dir', label: 'Folders' }, { value: 'file', label: 'Files' }]} />
        {level === 'dir' && <Segmented size="sm" value={depth} onChange={setDepth} label="Depth" options={[1, 2, 3, 4, 5].map(d => ({ value: d, label: String(d), title: `${d} folder level${d > 1 ? 's' : ''} deep` }))} />}
        <Select value={root} onChange={v => { setRoot(v); if (focus) setFocus('') }} options={dirOptions} ariaLabel="Start in" />
        <SwitchLine checked={tests} onChange={setTests} label="Tests" />
        <span className="spacer" />
        <Segmented size="sm" value={layout} onChange={setLayout} options={[{ value: 'layers', label: 'Layers' }, { value: 'organic', label: 'Organic' }]} />
        <button type="button" className="btn sm ghost icon-only" title="Fit to the screen" aria-label="Fit" onClick={() => canvas.current?.fit()}><Icon name="maximize" size={15} /></button>
        <button type="button" className="btn sm ghost icon-only" title="Save as a picture" aria-label="Save as PNG" onClick={() => { const url = canvas.current?.png(); if (url) { const a = document.createElement('a'); a.href = url; a.download = 'graph.png'; a.click() } }}><Icon name="download" size={15} /></button>
      </div>
      {focus && <div className="callout"><Icon name="target" size={16} /><span className="grow">Only what is near <span className="mono">{focus || 'the top level'}</span>.</span>
        <Segmented size="sm" value={radius} onChange={setRadius} options={[1, 2, 3].map(r => ({ value: r, label: `${r} step${r > 1 ? 's' : ''}` }))} />
        <button type="button" className="btn sm" onClick={() => setFocus('')}>Show everything</button></div>}
      {root && !focus && <div className="row small"><button type="button" className="btn sm ghost" onClick={() => setRoot(root.includes('/') ? root.slice(0, root.lastIndexOf('/')) : '')}><Icon name="arrow-up" size={14} />Up</button><span className="muted">Inside <span className="mono">{root}</span></span></div>}
      <div className="card graph-card">
        {error ? <p className="bad pad">{error}</p> : !g ? <Loading /> : g.nodes.length === 0 ? <p className="muted pad">Nothing to draw here: no code files{tests ? '' : ' (tests are hidden)'}.</p>
          : <GraphCanvas ref={canvas} nodes={nodes} edges={edges} layout={layout} direction={mobile ? 'TB' : 'LR'} height={mobile ? 460 : 'calc(100dvh - 330px)'}
            onTap={id => open(id)} onDblTap={drill} />}
        {loading && g && <span className="graph-loading"><span className="spinner" /></span>}
      </div>
      <div className="row wrap small muted graph-legend">
        {langs.map(l => <span key={l}><i className="ldot" style={{ background: langColor(l) }} />{l}</span>)}
        <span><i className="ledge cycle" />imports each other</span>
        <span>Size = lines. Thicker line = more imports.</span>
        {g && <span className="tnum">{g.nodes.length} {level === 'dir' ? 'folders' : 'files'}, {g.edges.length} links{g.truncated ? ' (only the most connected are shown)' : ''}</span>}
        <span className="hide-sm">Click for details{level === 'dir' ? ', double-click to go inside' : ''}.</span>
      </div>
    </div>
  )
}
