// Every HTTP endpoint (from the code and from API documents), grouped, with the OpenAPI document to download.
import { useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { api, V1 } from '../../lib/api'
import { METHOD_ORDER } from '../../lib/code'
import type { Endpoint, SpecFile } from '../../lib/types'
import { Icon } from '../../components/icons'
import { Card, copyText, download, Empty, Loading, Modal, Segmented, useAsync, useToast } from '../../components/ui'
import { useCB } from './Frame'

const norm = (p: string) => p.replace(/\{[^}]+\}/g, '{}').replace(/\/+$/, '') || '/'

function PathText({ p }: { p: string }) {
  return <span className="mono ep-path">{p.split(/(\{[^}]+\})/g).map((part, i) => part.startsWith('{') ? <span key={i} className="ep-param">{part}</span> : part)}</span>
}

function Spec({ onClose }: { onClose: () => void }) {
  const { scanId } = useCB()
  const { toast } = useToast()
  const [fmt, setFmt] = useState<'yaml' | 'json'>('yaml')
  const { data } = useAsync(() => api.getText(`${V1}/scans/${scanId}/openapi.${fmt}`), [scanId, fmt])
  return (
    <Modal title="OpenAPI document" onClose={onClose} size="xl" footer={<>
      <Segmented size="sm" value={fmt} onChange={setFmt} options={[{ value: 'yaml', label: 'YAML' }, { value: 'json', label: 'JSON' }]} />
      <span className="grow" />
      <button type="button" className="btn" onClick={async () => { if (data && await copyText(data)) toast('Copied') }}><Icon name="copy" size={14} />Copy</button>
      <button type="button" className="btn primary" onClick={() => download(`${V1}/scans/${scanId}/openapi.${fmt}?download=1`)}><Icon name="download" size={14} />Download</button>
    </>}>
      <p className="small muted">Paths, methods, path parameters and where each handler is. Request and response bodies are not guessed — fill them in where you need them. Import it into Postman, Bruno, Insomnia or Swagger UI.</p>
      <pre className="set-pre spec-pre">{data ?? 'Loading…'}</pre>
    </Modal>
  )
}

export default function Api() {
  const { scanId, source } = useCB()
  const [sp] = useSearchParams()
  const { data, error } = useAsync(() => api.get<{ endpoints: Endpoint[]; spec_files: SpecFile[] }>(`${V1}/scans/${scanId}/endpoints`), [scanId])
  const [q, setQ] = useState(sp.get('q') || '')
  const [methods, setMethods] = useState<Set<string>>(new Set())
  const [src, setSrc] = useState<'all' | 'code' | 'spec'>('all')
  const [group, setGroup] = useState<'path' | 'file'>('path')
  const [spec, setSpec] = useState(false)

  const list = useMemo(() => {
    if (!data) return []
    const n = q.trim().toLowerCase()
    return data.endpoints.filter(e => (src === 'all' || e.source === src) && (!methods.size || methods.has(e.method)) &&
      (!n || `${e.method} ${e.path} ${e.handler || ''} ${e.file} ${e.summary || ''}`.toLowerCase().includes(n)))
  }, [data, q, src, methods])
  const groups = useMemo(() => {
    const m = new Map<string, Endpoint[]>()
    for (const e of list) {
      const key = group === 'file' ? e.file : (e.tag || e.path.split('/').filter(s => s && s !== 'api' && !/^v\d+$/.test(s) && !s.startsWith('{'))[0] || '/')
      if (!m.has(key)) m.set(key, [])
      m.get(key)!.push(e)
    }
    for (const l of m.values()) l.sort((a, b) => a.path.localeCompare(b.path) || METHOD_ORDER.indexOf(a.method) - METHOD_ORDER.indexOf(b.method))
    return [...m.entries()].sort((a, b) => a[0].localeCompare(b[0]))
  }, [list, group])
  const drift = useMemo(() => {
    if (!data || !data.spec_files.length) return null
    const code = new Set(data.endpoints.filter(e => e.source === 'code').map(e => `${e.method} ${norm(e.path)}`))
    const specd = new Set(data.endpoints.filter(e => e.source === 'spec').map(e => `${e.method} ${norm(e.path)}`))
    return {
      onlySpec: data.endpoints.filter(e => e.source === 'spec' && !code.has(`${e.method} ${norm(e.path)}`)),
      notInSpec: data.endpoints.filter(e => e.source === 'code' && !specd.has(`${e.method} ${norm(e.path)}`)),
    }
  }, [data])

  if (error) return <p className="bad">{error}</p>
  if (!data) return <Loading />
  if (!data.endpoints.length) return <Empty title="No HTTP endpoints found" icon="api">Blueprint finds routes in Go (chi, gin, echo, gorilla, net/http), JavaScript and TypeScript (Express, Fastify, Hono, Next.js, NestJS), Python (FastAPI, Flask, Django), Java (Spring), C#, PHP (Laravel), Ruby (Rails) and Rust (axum, actix), and in OpenAPI / Swagger documents.</Empty>
  const allMethods = METHOD_ORDER.filter(m => data.endpoints.some(e => e.method === m))
  const toggleMethod = (m: string) => setMethods(s => { const n = new Set(s); if (n.has(m)) n.delete(m); else n.add(m); return n })
  return (
    <div className="stack">
      <div className="row wrap filters">
        <div className="search-box"><Icon name="search" size={15} /><input value={q} onChange={e => setQ(e.target.value)} placeholder="Filter by path, handler or file" aria-label="Filter endpoints" /></div>
        <div className="chips">{allMethods.map(m => <button type="button" key={m} className={`chip method-chip ${methods.has(m) ? 'active' : ''}`} onClick={() => toggleMethod(m)}><span className={`method m-${m.toLowerCase()}`}>{m}</span></button>)}</div>
        <span className="spacer" />
        <button type="button" className="btn" onClick={() => setSpec(true)}><Icon name="file" size={15} />OpenAPI</button>
      </div>
      <div className="row wrap">
        <Segmented size="sm" value={group} onChange={setGroup} options={[{ value: 'path', label: 'By path' }, { value: 'file', label: 'By file' }]} />
        {data.spec_files.length > 0 && <Segmented size="sm" value={src} onChange={setSrc} options={[{ value: 'all', label: 'All' }, { value: 'code', label: 'In the code' }, { value: 'spec', label: 'In API documents' }]} />}
        <span className="small muted tnum">{list.length} of {data.endpoints.length} endpoints</span>
      </div>
      {drift && (drift.onlySpec.length > 0 || drift.notInSpec.length > 0) && src === 'all' && !q && !methods.size && (
        <div className="callout"><Icon name="info" size={16} /><span>
          {drift.notInSpec.length > 0 && <>{drift.notInSpec.length} in the code but not in the API document{drift.notInSpec.length < 6 && <>: {drift.notInSpec.map(e => <span key={e.method + e.path} className="mono"> {e.method} {e.path}</span>)}</>}. </>}
          {drift.onlySpec.length > 0 && <>{drift.onlySpec.length} in the document but not found in the code{drift.onlySpec.length < 6 && <>: {drift.onlySpec.map(e => <span key={e.method + e.path} className="mono"> {e.method} {e.path}</span>)}</>}.</>}
        </span></div>
      )}
      {groups.map(([key, eps]) => (
        <Card key={key} title={<span className="mono">{group === 'file' ? key : key === '/' ? '/' : key}</span>} sub={`${eps.length} endpoint${eps.length > 1 ? 's' : ''}`} flush>
          <ul className="list">{eps.map((e, i) => (
            <li key={i}><button type="button" className="list-row ep-row" onClick={() => source(e.file, e.line)} title={`${e.file}:${e.line}`}>
              <span className={`method m-${e.method.toLowerCase()}`}>{e.method}</span>
              <span className="grow min0">
                <span className="block ellipsis"><PathText p={e.path} /></span>
                <span className="block xsmall muted ellipsis">{e.summary || e.handler || ''}{(e.summary || e.handler) && ' · '}<span className="mono">{e.file}:{e.line}</span></span>
              </span>
              <span className={`badge ${e.source === 'spec' ? 'accent-soft' : ''}`} title={e.source === 'spec' ? 'From an API document' : `Found in the code (${e.framework})`}>{e.source === 'spec' ? 'doc' : e.framework}</span>
            </button></li>))}</ul>
        </Card>
      ))}
      {data.spec_files.length > 0 && <Card title="API documents in the folder" flush>
        <ul className="list">{data.spec_files.map(f => (
          <li key={f.path}><button type="button" className="list-row" onClick={() => source(f.path)}>
            <Icon name="file" size={15} className="muted" /><span className="grow min0"><span className="block small"><b>{f.title || f.path}</b> {f.version && <span className="muted">v{f.version}</span>}</span><span className="block xsmall muted mono">{f.path} · {f.kind}</span></span>
            <span className="badge">{f.endpoints}</span>
          </button></li>))}</ul>
      </Card>}
      {spec && <Spec onClose={() => setSpec(false)} />}
    </div>
  )
}
