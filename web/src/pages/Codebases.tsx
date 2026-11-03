// All codebases: one card each with the latest numbers and the scan state.
import { Link } from 'react-router-dom'
import { useData, isBusy } from '../lib/data'
import { SOURCE_ICON, SOURCE_LABEL, compact, where } from '../lib/code'
import { ago } from '../lib/format'
import type { Codebase, LangStat } from '../lib/types'
import { Icon } from '../components/icons'
import { Empty, Loading, PageHeader } from '../components/ui'
import { openAdd } from '../components/Layout'
import { ScanProgress } from '../components/ScanState'
import { LangBar } from '../components/LangBar'
import { useEffect, useState } from 'react'
import { api, V1 } from '../lib/api'

/** The languages of a codebase's latest scan (from its overview, cached per scan). */
const langCache = new Map<number, LangStat[]>()
function useLangs(scanId?: number) {
  const [langs, setLangs] = useState<LangStat[] | null>(scanId ? langCache.get(scanId) || null : null)
  useEffect(() => {
    if (!scanId || langCache.has(scanId)) { setLangs(scanId ? langCache.get(scanId) || null : null); return }
    api.get<{ languages: LangStat[] }>(`${V1}/scans/${scanId}/overview`).then(o => { langCache.set(scanId, o.languages); setLangs(o.languages) }).catch(() => {})
  }, [scanId])
  return langs
}

function Card({ c, reload }: { c: Codebase; reload: () => void }) {
  const s = c.done?.stats
  const langs = useLangs(c.done?.id)
  const busy = isBusy(c)
  return (
    <Link to={`/c/${c.id}`} className="cb-card">
      <div className="cb-head">
        <span className="cb-ico"><Icon name={SOURCE_ICON[c.source] || 'folder'} size={17} /></span>
        <span className="grow min0">
          <span className="cb-name ellipsis">{c.name}</span>
          <span className="cb-where ellipsis small muted" title={SOURCE_LABEL[c.source]}>{where(c)}</span>
        </span>
        {c.pinned && <Icon name="pin" size={13} className="faint" />}
        {c.role !== 'owner' ? <Icon name="users" size={13} className="faint" title={`Shared by ${c.owner}`} />
          : c.shares > 0 && <Icon name="users" size={13} className="faint" title={`Shared with ${c.shares}`} />}
      </div>
      {s ? <>
        <div className="cb-nums">
          <span><b className="tnum">{compact(s.files)}</b> files</span>
          <span><b className="tnum">{compact(s.lines)}</b> lines</span>
          <span><b className="tnum">{compact(s.endpoints)}</b> endpoints</span>
          <span><b className="tnum">{compact(s.tables)}</b> tables</span>
        </div>
        {langs ? <LangBar langs={langs} /> : <div className="stacked" style={{ height: 8 }} />}
      </> : !busy && <p className="muted small">{c.latest?.status === 'failed' ? 'The scan failed — open it to see why.' : 'Not scanned yet.'}</p>}
      <div className="cb-foot small muted">
        {busy && c.latest ? <ScanProgress scan={c.latest} onCancel={c.role !== 'view' ? reload : undefined} />
          : <>
            {c.latest?.status === 'failed' ? <span className="bad row"><Icon name="alert" size={13} />Last scan failed</span> : c.done ? <span>Scanned {ago(c.done.finished_at)}</span> : <span />}
            {!!s?.cycles && <span className="cb-warn" title="Folders that import each other"><Icon name="loop" size={13} />{s.cycles} {s.cycles === 1 ? 'loop' : 'loops'}</span>}
          </>}
      </div>
    </Link>
  )
}

export default function Codebases() {
  const { codebases, reload } = useData()
  if (!codebases) return <Loading />
  return (
    <div className="stack loose">
      <PageHeader title="Codebases" sub="How each of your codebases is built: folders, connections, endpoints and data." actions={
        <button type="button" className="btn primary" onClick={openAdd}><Icon name="plus" size={15} />Add codebase</button>} />
      {codebases.length === 0
        ? <Empty title="No codebases yet" icon="blueprint">
          <p>Add a git repository, upload an archive or pick a folder. Blueprint reads it and shows its structure, a graph of which file uses which, the API endpoints, database tables and more.</p>
          <button type="button" className="btn primary" onClick={openAdd}><Icon name="plus" size={15} />Add your first codebase</button>
        </Empty>
        : <div className="cb-grid">{codebases.map(c => <Card key={c.id} c={c} reload={reload} />)}</div>}
    </div>
  )
}
