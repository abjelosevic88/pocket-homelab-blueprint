// One codebase: its header, the tabs, the side panel for a file or folder and the source viewer.
import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import { NavLink, Outlet, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { api, uploadForm, V1 } from '../../lib/api'
import { useData, isBusy } from '../../lib/data'
import { SOURCE_ICON, SOURCE_LABEL, where } from '../../lib/code'
import { ago, fmtDateTime } from '../../lib/format'
import type { Codebase, Scan } from '../../lib/types'
import { Icon } from '../../components/icons'
import { Confirm, download, errMsg, Field, Loading, MenuButton, type MenuItem, Modal, Spinner, useToast } from '../../components/ui'
import { Select } from '../../components/Select'
import { SwitchLine } from '../../components/SwitchLine'
import { useCrumb } from '../../components/Layout'
import { ScanFailed, ScanProgress } from '../../components/ScanState'
import { RESCAN_OPTIONS } from '../../components/AddCodebase'
import { PathPanel } from './PathPanel'
import { SourceView } from './SourceView'

interface CBState {
  c: Codebase
  scanId: number | null
  /** the scan being looked at (latest finished unless ?scan= picks an older one) */
  viewing: Scan | null
  reload: () => void
  /** open the side panel for a file or folder ("" = the whole codebase) */
  open: (path: string) => void
  /** show a file's source, at a line */
  source: (path: string, line?: number) => void
}
const Ctx = createContext<CBState>(null!)
export const useCB = () => useContext(Ctx)

const TABS: { to: string; label: string; icon: string }[] = [
  { to: '', label: 'Overview', icon: 'blueprint' },
  { to: 'structure', label: 'Structure', icon: 'folder' },
  { to: 'graph', label: 'Graph', icon: 'graph' },
  { to: 'api', label: 'API', icon: 'api' },
  { to: 'data', label: 'Data', icon: 'database' },
  { to: 'config', label: 'Config', icon: 'sliders' },
  { to: 'hotspots', label: 'Hotspots', icon: 'flame' },
  { to: 'scans', label: 'Scans', icon: 'git-commit' },
]

export default function Frame() {
  const { id } = useParams()
  const cid = Number(id)
  const navigate = useNavigate()
  const [sp, setSp] = useSearchParams()
  const { codebases, reload } = useData()
  const { toast } = useToast()
  const fromList = codebases?.find(c => c.id === cid)
  const [fetched, setFetched] = useState<Codebase | null>(null)
  const [missing, setMissing] = useState(false)
  useEffect(() => {
    if (codebases && !fromList) api.get<Codebase>(`${V1}/codebases/${cid}`).then(setFetched).catch(() => setMissing(true))
  }, [cid, codebases, fromList])
  const c = fromList || fetched
  useCrumb(c?.name)
  const [editing, setEditing] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [viewing, setViewing] = useState<Scan | null>(null)

  const pick = sp.get('scan')
  const scanId = pick ? Number(pick) : c?.done?.id ?? null
  useEffect(() => {
    if (!scanId) { setViewing(null); return }
    if (c?.done?.id === scanId) { setViewing(c.done); return }
    api.get<Scan>(`${V1}/scans/${scanId}`).then(setViewing).catch(() => setViewing(null))
  }, [scanId, c?.done])

  const open = useCallback((path: string) => { const n = new URLSearchParams(sp); n.set('path', path); n.delete('line'); setSp(n) }, [sp, setSp])
  const source = useCallback((path: string, line?: number) => {
    const n = new URLSearchParams(sp); n.set('src', path); if (line) n.set('line', String(line)); else n.delete('line'); setSp(n)
  }, [sp, setSp])
  const closeParam = (...keys: string[]) => { const n = new URLSearchParams(sp); keys.forEach(k => n.delete(k)); setSp(n, { replace: true }) }
  // ?path=x&line=n from search: open the source right away
  useEffect(() => {
    if (sp.get('path') && sp.get('line') && !sp.get('src')) { const n = new URLSearchParams(sp); n.set('src', sp.get('path')!); setSp(n, { replace: true }) }
  }, [sp, setSp])

  const state = useMemo<CBState | null>(() => c ? { c, scanId, viewing, reload, open, source } : null, [c, scanId, viewing, reload, open, source])
  if (missing) return <div className="empty"><h3>This codebase is not here</h3><button type="button" className="btn" onClick={() => navigate('/')}>All codebases</button></div>
  if (!c || !state) return <Loading />

  const busy = isBusy(c)
  const failedNewer = c.latest && (c.latest.status === 'failed' || c.latest.status === 'cancelled') && (!c.done || c.latest.id > c.done.id)
  const rescan = async () => {
    try { await api.post(`${V1}/codebases/${c.id}/scan`); reload() } catch (e) { toast(errMsg(e), 'err') }
  }
  const qs = (extra = '') => { const n = new URLSearchParams(); if (pick) n.set('scan', pick); const s = n.toString(); return s ? `?${s}${extra}` : extra ? `?${extra.slice(1)}` : '' }
  const menu = (): MenuItem[] => [
    { label: 'Edit', icon: 'edit', onSelect: () => setEditing(true) },
    { label: c.pinned ? 'Unpin' : 'Pin to the top', icon: 'pin', onSelect: async () => { await api.put(`${V1}/codebases/${c.id}`, { pinned: !c.pinned }); reload() } },
    ...(scanId ? ['sep' as const,
      { label: 'Download report (Markdown)', icon: 'download', onSelect: () => download(`${V1}/scans/${scanId}/report.md?download=1`) },
      { label: 'Download OpenAPI (YAML)', icon: 'download', onSelect: () => download(`${V1}/scans/${scanId}/openapi.yaml?download=1`) },
      { label: 'Download everything found (JSON)', icon: 'download', onSelect: () => download(`${V1}/scans/${scanId}/export.json`) }] : []),
    'sep',
    { label: 'Delete codebase', icon: 'trash', danger: true, onSelect: () => setDeleting(true) },
  ]
  return (
    <Ctx.Provider value={state}>
      <div className="cb-page">
        <header className="cb-top">
          <span className="cb-ico lg"><Icon name={SOURCE_ICON[c.source] || 'folder'} size={20} /></span>
          <div className="grow min0">
            <h1 className="cb-title ellipsis">{c.name}</h1>
            <div className="small muted row wrap">
              <span title={SOURCE_LABEL[c.source]} className="mono ellipsis cb-loc">{where(c)}</span>
              {c.done && <span>· scanned {ago(c.done.finished_at)}{c.done.commit && <> at <span className="mono">{c.done.commit}</span></>}</span>}
              {c.rescan_hours > 0 && <span title="Scans again on its own">· <Icon name="repeat" size={12} /> every {c.rescan_hours >= 24 ? `${c.rescan_hours / 24} day${c.rescan_hours > 24 ? 's' : ''}` : `${c.rescan_hours} h`}</span>}
            </div>
          </div>
          <div className="row">
            <button type="button" className="btn" onClick={rescan} disabled={busy} title="Scan again (r)">{busy ? <Spinner /> : <Icon name="refresh" size={15} />}<span className="hide-sm">Scan again</span></button>
            <MenuButton label="More" items={menu} align="right"><Icon name="more" size={18} /></MenuButton>
          </div>
        </header>
        {c.description && <p className="muted cb-desc">{c.description}</p>}
        {busy && c.latest && <ScanProgress scan={c.latest} onCancel={reload} />}
        {!busy && failedNewer && c.latest && <ScanFailed scan={c.latest} />}
        {pick && viewing && c.done && viewing.id !== c.done.id && (
          <div className="callout"><Icon name="clock" size={16} /><span className="grow">You are looking at the scan from {fmtDateTime(viewing.finished_at)}.</span>
            <button type="button" className="btn sm" onClick={() => closeParam('scan')}>Back to the latest</button></div>
        )}
        <nav className="tabs cb-tabs" aria-label="Codebase">
          {TABS.map(t => <NavLink key={t.to} to={`/c/${c.id}${t.to ? '/' + t.to : ''}${qs()}`} end={t.to === ''} className={({ isActive }) => isActive ? 'active' : ''}><Icon name={t.icon} size={15} />{t.label}</NavLink>)}
        </nav>
        {scanId ? <Outlet /> : busy ? <div className="empty"><h3>Scanning for the first time…</h3><p className="muted">The results show up here as soon as the scan is done.</p></div>
          : <div className="empty"><h3>Nothing to show yet</h3><p className="muted">This codebase has no finished scan.</p><button type="button" className="btn primary" onClick={rescan}><Icon name="scan" size={15} />Scan now</button></div>}
      </div>
      {scanId && sp.get('path') !== null && <PathPanel key={sp.get('path')} path={sp.get('path') || ''} onClose={() => closeParam('path', 'line')} />}
      {scanId && sp.get('src') && <SourceView path={sp.get('src')!} line={Number(sp.get('line')) || 0} onClose={() => closeParam('src', 'line')} />}
      {editing && <EditCodebase c={c} onClose={() => setEditing(false)} onSaved={() => { setEditing(false); reload(); setFetched(null) }} />}
      {deleting && <Confirm title={`Delete ${c.name}?`} message="Its scans and notes are deleted, and the copy of the code Blueprint keeps (a clone or an archive). The code itself is not touched." confirmLabel="Delete"
        onCancel={() => setDeleting(false)} onConfirm={async () => { await api.del(`${V1}/codebases/${c.id}`); setDeleting(false); await reload(); navigate('/') }} />}
    </Ctx.Provider>
  )
}

function EditCodebase({ c, onClose, onSaved }: { c: Codebase; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const [f, setF] = useState({ name: c.name, description: c.description, location: c.location, branch: c.branch, subdir: c.subdir, git_user: c.git_user, git_token: c.has_token ? '••••••••' : '', vendored: c.vendored, rescan_hours: String(c.rescan_hours) })
  const [file, setFile] = useState<File | null>(null)
  const [busy, setBusy] = useState(false)
  const set = (k: keyof typeof f) => (v: string | boolean) => setF(x => ({ ...x, [k]: v }))
  const save = async (scan: boolean) => {
    setBusy(true)
    try {
      await api.put(`${V1}/codebases/${c.id}`, { ...f, rescan_hours: +f.rescan_hours, scan: scan && !file })
      if (file) await uploadForm(`${V1}/codebases/upload`, file, { codebase: String(c.id) })
      toast(scan || file ? 'Saved: scanning again' : 'Saved')
      onSaved()
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  const rescanOpts = RESCAN_OPTIONS.some(o => o.value === f.rescan_hours) ? RESCAN_OPTIONS : [...RESCAN_OPTIONS, { value: f.rescan_hours, label: `Every ${f.rescan_hours} hours` }]
  return (
    <Modal title={`Edit ${c.name}`} onClose={onClose} size="lg" footer={<>
      <span className="grow" />
      <button type="button" className="btn" onClick={() => save(false)} disabled={busy}>Save</button>
      <button type="button" className="btn primary" onClick={() => save(true)} disabled={busy}>{busy ? <Spinner /> : <Icon name="scan" size={15} />}Save and scan</button>
    </>}>
      <div className="stack">
        <Field label="Name"><input value={f.name} onChange={e => set('name')(e.target.value)} /></Field>
        <Field label="Description"><textarea rows={2} value={f.description} onChange={e => set('description')(e.target.value)} placeholder="What is this code?" /></Field>
        {c.source === 'git' && <>
          <Field label="Repository address"><input value={f.location} onChange={e => set('location')(e.target.value)} spellCheck={false} /></Field>
          <div className="grid two">
            <Field label="Branch"><input value={f.branch} onChange={e => set('branch')(e.target.value)} placeholder="the default branch" spellCheck={false} /></Field>
            <Field label="User name" help="Only for servers that need one with the token."><input value={f.git_user} onChange={e => set('git_user')(e.target.value)} /></Field>
          </div>
          <Field label="Access token" help="For a private repository. Empty it to remove the token."><input type="password" value={f.git_token} onFocus={e => { if (e.target.value.includes('••')) set('git_token')('') }} onChange={e => set('git_token')(e.target.value)} autoComplete="off" /></Field>
        </>}
        {c.source === 'upload' && <Field label="New archive" help="Replace the code with a newer archive; it is scanned right away.">
          <input type="file" accept=".zip,.tar,.tgz,.gz" onChange={e => setFile(e.target.files?.[0] || null)} /></Field>}
        {(c.source === 'path' || c.source === 'files') && <Field label="Folder"><input value={c.location} disabled /></Field>}
        <Field label="Only this folder" help="Empty = everything."><input value={f.subdir} onChange={e => set('subdir')(e.target.value)} spellCheck={false} /></Field>
        <div className="grid two">
          {c.source !== 'upload' && <Field label="Scan again"><Select value={f.rescan_hours} onChange={set('rescan_hours')} options={rescanOpts} ariaLabel="Scan again" /></Field>}
          <div className="field"><span className="label">&nbsp;</span><SwitchLine checked={f.vendored} onChange={set('vendored')} label="Also read vendor/ and node_modules/" /></div>
        </div>
      </div>
    </Modal>
  )
}
