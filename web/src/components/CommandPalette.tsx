// ⌘K / "/" — find a file, function, endpoint, table or variable in any codebase, jump to a page, or run a command.
import React, { useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { api, V1 } from '../lib/api'
import type { SearchHit } from '../lib/types'
import { Icon } from './icons'
import { useDebounce } from './ui'

export interface PaletteCommand { id: string; label: string; icon: string; hint?: string; keywords?: string; run: () => void }

const HIT_ICON: Record<SearchHit['kind'], string> = { file: 'file', symbol: 'braces', endpoint: 'api', table: 'database', env: 'variable' }
const HIT_GROUP: Record<SearchHit['kind'], string> = { file: 'Files', symbol: 'Functions and types', endpoint: 'Endpoints', table: 'Tables', env: 'Environment variables' }

export function CommandPalette({ onClose, commands, onHit }: { onClose: () => void; commands: PaletteCommand[]; onHit: (h: SearchHit) => void }) {
  const [q, setQ] = useState('')
  const [hi, setHi] = useState(0)
  const [hits, setHits] = useState<SearchHit[]>([])
  const list = useRef<HTMLDivElement>(null)
  const dq = useDebounce(q.trim(), 180)
  useEffect(() => {
    if (dq.length < 2) { setHits([]); return }
    let live = true
    api.get<SearchHit[]>(`${V1}/search?q=${encodeURIComponent(dq)}&limit=40`).then(h => { if (live) setHits(h) }).catch(() => {})
    return () => { live = false }
  }, [dq])

  type Row = { key: string; group: string; icon: React.ReactNode; label: React.ReactNode; sub?: React.ReactNode; hint?: string; run: () => void }
  const rows = useMemo<Row[]>(() => {
    const n = q.trim().toLowerCase()
    const words = n.split(/\s+/).filter(Boolean)
    const hit = (text: string) => words.every(w => text.toLowerCase().includes(w))
    const cmds = commands.filter(c => !n || hit(`${c.label} ${c.keywords || ''}`)).map(c => ({ key: c.id, group: 'Commands', icon: <Icon name={c.icon} size={16} />, label: c.label, hint: c.hint, run: c.run }))
    const order: SearchHit['kind'][] = ['symbol', 'endpoint', 'file', 'table', 'env']
    const found = [...hits].sort((a, b) => order.indexOf(a.kind) - order.indexOf(b.kind)).map((h, i) => ({
      key: `h${i}`, group: HIT_GROUP[h.kind], icon: <Icon name={HIT_ICON[h.kind]} size={16} />, label: h.title,
      sub: `${h.codebase} · ${h.detail}${h.line ? `:${h.line}` : ''}`, run: () => onHit(h),
    }))
    return n ? [...found, ...cmds.slice(0, 6)] : cmds
  }, [q, hits, commands, onHit])
  useEffect(() => setHi(0), [q, hits])
  useEffect(() => { list.current?.querySelector(`[data-i="${hi}"]`)?.scrollIntoView({ block: 'nearest' }) }, [hi])

  const run = (r: Row | undefined) => { if (!r) return; onClose(); r.run() }
  let last = ''
  return createPortal(
    <div className="palette-backdrop" onMouseDown={e => { if (e.target === e.currentTarget) onClose() }}>
      <div className="palette-box" role="dialog" aria-label="Search and commands">
        <div className="pal-input">
          <Icon name="search" size={18} />
          <input autoFocus value={q} onChange={e => setQ(e.target.value)} placeholder="Find a file, function, endpoint or table… or run a command" aria-label="Search"
            onKeyDown={e => {
              if (e.key === 'ArrowDown') { e.preventDefault(); setHi(h => Math.min(rows.length - 1, h + 1)) }
              else if (e.key === 'ArrowUp') { e.preventDefault(); setHi(h => Math.max(0, h - 1)) }
              else if (e.key === 'Enter') { e.preventDefault(); run(rows[hi]) }
              else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onClose() }
            }} />
          <kbd>esc</kbd>
        </div>
        <div className="pal-list" ref={list} role="listbox">
          {rows.length === 0 && <div className="pal-empty">{q.trim().length < 2 ? 'Type at least two letters.' : 'Nothing matches.'}</div>}
          {rows.map((r, i) => {
            const head = r.group !== last ? <div className="pal-group" key={'g' + r.group}>{r.group}</div> : null
            last = r.group
            return (
              <React.Fragment key={r.key}>
                {head}
                <div data-i={i} role="option" aria-selected={i === hi} className={`pal-row ${i === hi ? 'hi' : ''}`} onMouseEnter={() => setHi(i)} onClick={() => run(r)}>
                  <span className="pal-ico">{r.icon}</span>
                  <span className="grow min0"><span className="pal-label ellipsis">{r.label}</span>{r.sub && <span className="pal-sub ellipsis">{r.sub}</span>}</span>
                  {r.hint && <kbd>{r.hint}</kbd>}
                  {i === hi && <Icon name="chevron-right" size={14} className="muted" />}
                </div>
              </React.Fragment>
            )
          })}
        </div>
      </div>
    </div>, document.body)
}
