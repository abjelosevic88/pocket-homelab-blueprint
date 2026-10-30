// A file's source with line numbers and its outline; opens at a line.
import { useEffect, useMemo, useRef, useState } from 'react'
import { api, V1 } from '../../lib/api'
import { base } from '../../lib/code'
import type { FileDetail } from '../../lib/types'
import { Icon } from '../../components/icons'
import { copyText, errMsg, Modal, Spinner, useToast } from '../../components/ui'
import { useCB } from './Frame'
import { KIND_ICON } from './PathPanel'

interface Src { path: string; lang?: string; text?: string; changed?: boolean; too_big?: boolean; binary?: boolean; bytes: number }

export function SourceView({ path, line, onClose }: { path: string; line: number; onClose: () => void }) {
  const { scanId, open } = useCB()
  const { toast } = useToast()
  const [src, setSrc] = useState<Src | null>(null)
  const [detail, setDetail] = useState<FileDetail | null>(null)
  const [err, setErr] = useState('')
  const [hl, setHl] = useState(line)
  const [find, setFind] = useState('')
  const body = useRef<HTMLDivElement>(null)
  useEffect(() => {
    setSrc(null); setErr('')
    api.get<Src>(`${V1}/scans/${scanId}/source?path=${encodeURIComponent(path)}`).then(setSrc).catch(e => setErr(errMsg(e)))
    api.get<FileDetail>(`${V1}/scans/${scanId}/file?path=${encodeURIComponent(path)}`).then(d => setDetail(d.kind === 'file' ? d : null)).catch(() => {})
  }, [scanId, path])
  useEffect(() => setHl(line), [line])
  const lines = useMemo(() => (src?.text ?? '').replace(/\n$/, '').split('\n'), [src])
  useEffect(() => {
    if (!src?.text || !hl) return
    requestAnimationFrame(() => body.current?.querySelector(`[data-l="${hl}"]`)?.scrollIntoView({ block: 'center' }))
  }, [src, hl])
  const q = find.trim().toLowerCase()
  const matches = useMemo(() => q.length < 2 ? [] : lines.map((l, i) => l.toLowerCase().includes(q) ? i + 1 : 0).filter(Boolean), [lines, q])
  const next = () => { if (!matches.length) return; const n = matches.find(m => m > hl) || matches[0]; setHl(n) }
  return (
    <Modal title={<span className="row"><Icon name="code" size={16} className="muted" /><span className="mono ellipsis">{path}</span></span>} onClose={onClose} size="xl" className="src-modal">
      <div className="src-wrap">
        {detail && detail.symbols.length > 0 && <nav className="src-outline" aria-label="Outline">
          {detail.symbols.map(s => <button type="button" key={`${s.parent}.${s.name}:${s.line}`} className={`src-sym ${hl >= s.line && hl <= (s.end_line || s.line) ? 'on' : ''}`} onClick={() => setHl(s.line)} title={s.signature || s.name}>
            <Icon name={KIND_ICON[s.kind] || 'braces'} size={12} /><span className="ellipsis mono">{s.parent ? `${s.parent}.` : ''}{s.name}</span></button>)}
        </nav>}
        <div className="src-main">
          <div className="src-bar">
            <div className="search-box sm"><Icon name="search" size={14} /><input value={find} onChange={e => setFind(e.target.value)} onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); next() } }} placeholder="Find in file" aria-label="Find in file" /></div>
            {q.length >= 2 && <span className="xsmall muted tnum">{matches.length} found</span>}
            <span className="spacer" />
            {src?.changed && <span className="badge warn" title="The file changed since this scan">changed since the scan</span>}
            {src?.text && <button type="button" className="btn sm ghost" onClick={async () => { if (await copyText(src.text!)) toast('Copied') }}><Icon name="copy" size={13} />Copy</button>}
            <button type="button" className="btn sm ghost" onClick={() => { onClose(); open(path) }}><Icon name="info" size={13} />Details</button>
          </div>
          <div className="src-body" ref={body}>
            {err ? <p className="bad small pad-sm">{err}</p>
              : !src ? <p className="muted small pad-sm"><Spinner /> Loading {base(path)}…</p>
                : src.too_big ? <p className="muted pad-sm">This file is too big to show here ({Math.round(src.bytes / 1024)} KB).</p>
                  : src.binary ? <p className="muted pad-sm">This is not a text file.</p>
                    : <table className="src"><tbody>{lines.map((l, i) => (
                      <tr key={i} data-l={i + 1} className={`${i + 1 === hl ? 'hl' : ''} ${q.length >= 2 && l.toLowerCase().includes(q) ? 'found' : ''}`}>
                        <td className="ln" onClick={() => setHl(i + 1)}>{i + 1}</td><td className="lc">{l || ' '}</td>
                      </tr>))}</tbody></table>}
          </div>
        </div>
      </div>
    </Modal>
  )
}
