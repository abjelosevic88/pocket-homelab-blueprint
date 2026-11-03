// Left column: add a codebase, the codebases with their scan state — the Pocket Calendar sidebar.
import { NavLink, Link } from 'react-router-dom'
import { useApp } from '../lib/app-context'
import { useData, isBusy } from '../lib/data'
import { SOURCE_ICON } from '../lib/code'
import { Icon } from './icons'

export function Sidebar({ onNavigate, onAdd }: { onNavigate: () => void; onAdd: () => void }) {
  const { status, settings } = useApp()
  const { codebases } = useData()
  const title = settings?.title || status?.title || 'Pocket Blueprint'
  return (
    <aside className="sidebar" aria-label="Blueprint">
      <div className="sb-brand">
        <img src="/favicon.svg" alt="" className="sb-logo" />
        <span className="sb-title">{title !== 'Pocket Blueprint' ? title : <>Pocket <b>Blueprint</b></>}</span>
        {status?.version && <span className="sb-version" title={`Version ${status.version}`}>{/^v?\d+\.\d+/.test(status.version) ? 'v' + status.version.replace(/^v/, '').replace(/[-+].*$/, '') : status.version}</span>}
      </div>
      <button type="button" className="btn primary create-btn" onClick={onAdd} title="Add a codebase (n)">
        <Icon name="plus" size={15} strokeWidth={2.2} /><span className="grow left">Add codebase</span>
      </button>

      <nav className="sb-nav" aria-label="Sections">
        <NavLink to="/" end onClick={onNavigate} className={({ isActive }) => `sb-nav-item ${isActive ? 'active' : ''}`}>
          <Icon name="grid" size={16} />All codebases
          {!!codebases?.length && <span className="sb-count tnum">{codebases.length}</span>}
        </NavLink>
      </nav>

      {[{ title: 'Codebases', list: codebases?.filter(c => c.role === 'owner') }, { title: 'Shared with me', list: codebases?.filter(c => c.role !== 'owner') }].map(sec => !!sec.list?.length && (
        <section className="sb-sec" key={sec.title}>
          <h4 className="sb-h"><span className="sb-h-btn">{sec.title}</span></h4>
          <ul className="sb-cbs">
            {sec.list.map(c => {
              const busy = isBusy(c)
              const failed = c.latest?.status === 'failed'
              return (
                <li key={c.id}>
                  <NavLink to={`/c/${c.id}`} onClick={onNavigate} className={({ isActive }) => `sb-cb ${isActive ? 'active' : ''}`} title={c.owner ? `${c.name} · shared by ${c.owner}` : c.name}>
                    <Icon name={SOURCE_ICON[c.source] || 'folder'} size={14} className="muted" />
                    <span className="grow ellipsis">{c.name}</span>
                    {busy ? <span className="sb-prog tnum" title={c.latest?.stage}>{c.latest?.status === 'queued' ? '…' : `${c.latest?.progress || 0}%`}</span>
                      : failed ? <Icon name="alert" size={13} className="src-warn" title="The last scan failed" />
                        : c.pinned ? <Icon name="pin" size={12} className="faint" /> : null}
                  </NavLink>
                </li>
              )
            })}
          </ul>
        </section>
      ))}
      {codebases && codebases.length === 0 && <p className="muted small sb-hint">Add a git repository, an archive{settings?.core_configured ? ', a folder from Pocket Files' : ''} or a folder on this server to see how it is built.</p>}

      <div className="sb-foot">
        <Link to="/settings" className="sb-foot-link" onClick={onNavigate}><Icon name="settings" size={15} />Settings</Link>
        <button type="button" className="sb-foot-link" onClick={() => { onNavigate(); window.dispatchEvent(new CustomEvent('pbp:shortcuts')) }}><Icon name="keyboard" size={15} />Shortcuts</button>
      </div>
    </aside>
  )
}
