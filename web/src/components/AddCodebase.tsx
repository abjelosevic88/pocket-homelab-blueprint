// "Add a codebase": a git repository, an archive, a folder in Pocket Files or a folder on this server.
import { useEffect, useState } from 'react'
import { api, uploadForm, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Codebase, Source, Sources } from '../lib/types'
import { Icon } from './icons'
import { Select } from './Select'
import { SwitchLine } from './SwitchLine'
import { CoreHint, errMsg, Field, Modal, Segmented, Spinner, useToast } from './ui'

export const RESCAN_OPTIONS = [
  { value: '0', label: 'Never on its own' },
  { value: '6', label: 'Every 6 hours' },
  { value: '24', label: 'Every day' },
  { value: '168', label: 'Every week' },
]

interface DirList { path: string; parent?: string; dirs: { name: string; path: string; project?: string }[]; project?: string; spaces?: { id: string; name: string }[] }

/** Browses folders: on this server (sources/browse) or in Pocket Files (sources/files). */
function FolderPicker({ kind, value, onPick }: { kind: 'path' | 'files'; value: string; onPick: (loc: string, name: string) => void }) {
  const [space, setSpace] = useState(kind === 'files' ? '' : '-')
  const [path, setPath] = useState('')
  const [list, setList] = useState<DirList | null>(null)
  const [err, setErr] = useState('')
  const [spaces, setSpaces] = useState<{ id: string; name: string }[]>([])
  useEffect(() => {
    setErr('')
    if (kind === 'files' && !space) {
      api.get<DirList>(`${V1}/sources/files`).then(r => { setSpaces(r.spaces || []); if (r.spaces?.length) setSpace(r.spaces[0].id) }).catch(e => setErr(errMsg(e)))
      return
    }
    const url = kind === 'files' ? `${V1}/sources/files?space=${encodeURIComponent(space)}&path=${encodeURIComponent(path || '/')}` : `${V1}/sources/browse?path=${encodeURIComponent(path)}`
    setList(null)
    api.get<DirList>(url).then(setList).catch(e => setErr(errMsg(e)))
  }, [kind, space, path])
  const loc = kind === 'files' ? `${space}:${list?.path || path || '/'}` : (list?.path || '')
  const up = kind === 'files' ? (path && path !== '/' ? path.slice(0, path.lastIndexOf('/')) || '/' : null) : (list?.path ? list.parent || '' : null)
  return (
    <div className="picker">
      {kind === 'files' && spaces.length > 1 && <Select value={space} onChange={v => { setSpace(v); setPath('') }} options={spaces.map(s => ({ value: s.id, label: s.name }))} ariaLabel="Place" />}
      <div className="picker-bar">
        <button type="button" className="btn sm ghost icon-only" disabled={up === null} onClick={() => setPath(up || '')} aria-label="Up one folder" title="Up"><Icon name="arrow-up" size={15} /></button>
        <span className="mono ellipsis grow">{(kind === 'files' ? list?.path : list?.path) || (kind === 'files' ? '/' : 'Shared folders')}</span>
        {list?.project && <span className="badge ok" title="Looks like a project">{list.project}</span>}
      </div>
      <div className="picker-list">
        {err && <p className="bad small pad-sm">{err}</p>}
        {!list && !err && <p className="muted small pad-sm"><Spinner /> Loading…</p>}
        {list?.dirs.map(d => (
          <button type="button" key={d.path} className="picker-row" onClick={() => setPath(d.path)}>
            <Icon name="folder" size={15} className="muted" /><span className="grow ellipsis left">{d.name}</span>
            {d.project && <span className="badge">{d.project}</span>}
            <Icon name="chevron-right" size={14} className="faint" />
          </button>
        ))}
        {list && list.dirs.length === 0 && <p className="muted small pad-sm">No folders in here.</p>}
      </div>
      <div className="row">
        <button type="button" className="btn sm" disabled={!list || (kind === 'path' && !list.path)} onClick={() => onPick(loc, (list?.path || '').split('/').filter(Boolean).pop() || '')}>
          <Icon name="check" size={14} />Use this folder
        </button>
        {value && <span className="small muted ellipsis">Chosen: <span className="mono">{value}</span></span>}
      </div>
    </div>
  )
}

export function AddCodebase({ onClose, onAdded }: { onClose: () => void; onAdded: (c: Codebase) => void }) {
  const { settings } = useApp()
  const { toast } = useToast()
  const [sources, setSources] = useState<Sources | null>(null)
  const [source, setSource] = useState<Source>('git')
  const [name, setName] = useState('')
  const [location, setLocation] = useState('')
  const [branch, setBranch] = useState('')
  const [subdir, setSubdir] = useState('')
  const [priv, setPriv] = useState(false)
  const [token, setToken] = useState('')
  const [gitUser, setGitUser] = useState('')
  const [vendored, setVendored] = useState(!!settings?.vendored)
  const [rescan, setRescan] = useState('0')
  const [file, setFile] = useState<File | null>(null)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  useEffect(() => { api.get<Sources>(`${V1}/sources`).then(setSources).catch(() => {}) }, [])
  const coreOn = !!settings?.core_configured && settings?.core_registered !== false
  const filesOK = !!sources?.files.available
  const pathOK = !!sources?.path.available

  const save = async () => {
    setErr('')
    setBusy(true)
    try {
      let c: Codebase
      if (source === 'upload') {
        if (!file) throw new Error('Choose a .zip or .tar.gz file first')
        c = await uploadForm<Codebase>(`${V1}/codebases/upload`, file, { name, subdir })
      } else {
        c = await api.post<Codebase>(`${V1}/codebases`, {
          source, name, location, branch: source === 'git' ? branch : '', subdir, vendored, rescan_hours: +rescan,
          git_token: source === 'git' && priv ? token : '', git_user: source === 'git' && priv ? gitUser : '',
        })
      }
      toast(`Added ${c.name}: scanning now`, 'ok')
      onAdded(c)
    } catch (e) {
      setErr(errMsg(e))
    } finally {
      setBusy(false)
    }
  }
  const ready = source === 'upload' ? !!file : !!location.trim()
  return (
    <Modal title="Add a codebase" onClose={onClose} size="lg" footer={<>
      <span className="small muted grow">{source === 'upload' && sources ? `Up to ${sources.upload.max_mb} MB.` : 'The first scan starts right away.'}</span>
      <button type="button" className="btn" onClick={onClose}>Cancel</button>
      <button type="button" className="btn primary" disabled={!ready || busy} onClick={save}>{busy ? <Spinner /> : <Icon name="scan" size={15} />}Add and scan</button>
    </>}>
      <div className="stack">
        <Segmented<Source> value={source} onChange={v => { setSource(v); setLocation(''); setErr('') }} options={[
          { value: 'git', label: 'Git repository' },
          { value: 'upload', label: 'Upload archive' },
          { value: 'files', label: 'Pocket Files', disabled: !filesOK, title: !coreOn ? 'Needs Pocket Core…' : !filesOK ? sources?.files.detail : undefined },
          { value: 'path', label: 'Server folder', disabled: !pathOK, title: !pathOK ? (sources?.path.admin ? 'No server folders are shared (SOURCE_ROOTS)' : 'Only admins can scan folders on the server') : undefined },
        ]} />
        {!filesOK && !coreOn && <CoreHint />}
        {!filesOK && coreOn && sources?.files.detail && <p className="muted small">Pocket Files: {sources.files.detail}.</p>}

        {source === 'git' && <>
          <Field label="Repository address" help="The https address, like https://github.com/owner/repo — GitHub, GitLab, Forgejo or any git server.">
            <input autoFocus value={location} onChange={e => setLocation(e.target.value)} placeholder="https://github.com/owner/repo" spellCheck={false} />
          </Field>
          <div className="grid two">
            <Field label="Branch" help="Empty = the default branch."><input value={branch} onChange={e => setBranch(e.target.value)} placeholder="main" spellCheck={false} /></Field>
            <Field label="Only this folder" help="Empty = the whole repository."><input value={subdir} onChange={e => setSubdir(e.target.value)} placeholder="services/api" spellCheck={false} /></Field>
          </div>
          <SwitchLine checked={priv} onChange={setPriv} label="Private repository (needs a token)" />
          {priv && <div className="grid two">
            <Field label="Access token" help="A read-only token. Stored here, never shown again."><input type="password" value={token} onChange={e => setToken(e.target.value)} autoComplete="off" /></Field>
            <Field label="User name" help="Only some servers need it; empty is fine for GitHub."><input value={gitUser} onChange={e => setGitUser(e.target.value)} autoComplete="off" /></Field>
          </div>}
        </>}

        {source === 'upload' && <>
          <Field label="Archive" help="A .zip, .tar.gz or .tar of the folder (GitHub's “Download ZIP” works). Include the .git folder to see the history too.">
            <input type="file" accept=".zip,.tar,.tgz,.gz,application/zip,application/gzip,application/x-tar" onChange={e => { const f = e.target.files?.[0] || null; setFile(f); if (f && !name) setName(f.name.replace(/\.(zip|tar\.gz|tgz|tar)$/i, '')) }} />
          </Field>
          <Field label="Only this folder" help="Empty = everything in the archive."><input value={subdir} onChange={e => setSubdir(e.target.value)} spellCheck={false} /></Field>
        </>}

        {(source === 'files' || source === 'path') && <>
          <FolderPicker key={source} kind={source} value={location} onPick={(loc, n) => { setLocation(loc); if (!name) setName(n) }} />
          {source === 'path' && <p className="muted small">The folder is read where it is, every time you scan. Only the folders shared with Pocket Blueprint ({sources?.path.roots.join(', ')}) can be picked.</p>}
          {source === 'files' && <p className="muted small">Blueprint fetches a copy of the folder from Pocket Files on every scan.</p>}
        </>}

        <Field label="Name"><input value={name} onChange={e => setName(e.target.value)} placeholder={source === 'git' ? 'From the address' : 'From the folder'} /></Field>
        <div className="grid two">
          {source !== 'upload' && <Field label="Scan again"><Select value={rescan} onChange={setRescan} options={RESCAN_OPTIONS} ariaLabel="Scan again" /></Field>}
          <div className="field"><span className="label">&nbsp;</span><SwitchLine checked={vendored} onChange={setVendored} label="Also read vendor/ and node_modules/" /></div>
        </div>
        {err && <div className="callout danger"><Icon name="alert" size={16} /><span>{err}</span></div>}
      </div>
    </Modal>
  )
}
