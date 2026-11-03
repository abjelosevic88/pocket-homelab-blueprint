// The app shell: sidebar, top bar, search palette, shortcuts — the Pocket Calendar layout.
import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'
import { Outlet, useLocation, useNavigate, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { DataProvider, useData } from '../lib/data'
import type { SearchHit } from '../lib/types'
import { Icon } from './icons'
import { Sidebar } from './Sidebar'
import { CommandPalette, type PaletteCommand } from './CommandPalette'
import { ShortcutsSheet } from './ShortcutsSheet'
import { AddCodebase } from './AddCodebase'
import { Avatar, type MenuItem, MenuButton, modalOpen, useIsMobile, useMediaQuery } from './ui'
import { AskButton } from './AskButton'

const SIDEBAR_KEY = 'pbp-sidebar'

/** The page's own name for the top bar breadcrumb. */
const CrumbCtx = createContext<(t: string) => void>(() => {})
export function useCrumb(title: string | undefined) {
  const set = useContext(CrumbCtx)
  useEffect(() => { set(title || ''); return () => set('') }, [title, set])
}

/** Opens "Add a codebase" from anywhere. */
export const openAdd = () => window.dispatchEvent(new CustomEvent('pbp:add'))

export default function Layout() {
  return <DataProvider><Shell /></DataProvider>
}

function Shell() {
  const navigate = useNavigate()
  const loc = useLocation()
  const [sp, setSp] = useSearchParams()
  const mobile = useIsMobile()
  const narrow = useMediaQuery('(max-width: 1099px)')
  const drawer = mobile || narrow
  const { codebases, reload } = useData()
  const [sidebarOpen, setSidebarOpen] = useState(() => !drawer && localStorage.getItem(SIDEBAR_KEY) !== '0')
  useEffect(() => { setSidebarOpen(!drawer && localStorage.getItem(SIDEBAR_KEY) !== '0') }, [drawer])
  const drawerOpenedAt = useRef(0)
  const toggleSidebar = () => setSidebarOpen(o => { drawerOpenedAt.current = performance.now(); const n = !o; if (!drawer) localStorage.setItem(SIDEBAR_KEY, n ? '1' : '0'); return n })
  const closeDrawer = () => { if (drawer) setSidebarOpen(false) }
  useEffect(() => { document.querySelector('.content')?.scrollTo({ top: 0 }) }, [loc.pathname])

  const [crumb, setCrumbRaw] = useState('')
  const setCrumb = useCallback((t: string) => setCrumbRaw(t), [])
  const [palette, setPalette] = useState(false)
  const [shortcuts, setShortcuts] = useState(false)
  const [adding, setAdding] = useState(false)

  // /?add=1 (the PWA shortcut) opens "add a codebase"
  useEffect(() => {
    if (sp.get('add')) { setAdding(true); sp.delete('add'); setSp(sp, { replace: true }) }
  }, []) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    const open = () => setShortcuts(true)
    const add = () => setAdding(true)
    window.addEventListener('pbp:shortcuts', open)
    window.addEventListener('pbp:add', add)
    return () => { window.removeEventListener('pbp:shortcuts', open); window.removeEventListener('pbp:add', add) }
  }, [])

  const current = loc.pathname.match(/^\/c\/(\d+)/)?.[1]
  const rescan = async () => {
    if (!current || codebases?.find(c => c.id === Number(current))?.role === 'view') return
    await api.post(`${V1}/codebases/${current}/scan`).catch(() => {})
    reload()
  }
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); setPalette(p => !p); return }
      if (e.metaKey || e.ctrlKey || e.altKey) return
      if (t.closest('input, textarea, select, [contenteditable="true"]') || modalOpen() || document.querySelector('.menu, .popover, .sel-pop')) return
      const go = (to: string) => { e.preventDefault(); navigate(to) }
      const tabs = ['', 'structure', 'graph', 'api', 'data', 'config', 'hotspots', 'scans']
      if (current && /^[1-8]$/.test(e.key)) { go(`/c/${current}${tabs[+e.key - 1] ? '/' + tabs[+e.key - 1] : ''}`); return }
      switch (e.key) {
        case '/': e.preventDefault(); setPalette(true); break
        case '?': e.preventDefault(); setShortcuts(true); break
        case 'n': e.preventDefault(); setAdding(true); break
        case 'r': if (current) { e.preventDefault(); rescan() } break
        case 'h': go('/'); break
        case ',': go('/settings'); break
        case '[': e.preventDefault(); toggleSidebar(); break
      }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }) // eslint-disable-line react-hooks/exhaustive-deps

  const commands: PaletteCommand[] = [
    { id: 'add', label: 'Add a codebase', icon: 'plus', hint: 'N', keywords: 'new repository git folder upload zip scan', run: () => setAdding(true) },
    ...(current ? [{ id: 'rescan', label: 'Scan this codebase again', icon: 'refresh', hint: 'R', keywords: 'rescan refresh update', run: rescan }] : []),
    { id: 'home', label: 'All codebases', icon: 'grid', hint: 'H', run: () => navigate('/') },
    ...(codebases || []).map(c => ({ id: `c${c.id}`, label: c.name, icon: 'blueprint', keywords: `open codebase ${c.location}`, run: () => navigate(`/c/${c.id}`) })),
    { id: 'settings', label: 'Settings', icon: 'settings', hint: ',', run: () => navigate('/settings') },
    { id: 'apps', label: 'Settings: connected apps', icon: 'layers', keywords: 'files core integrations', run: () => navigate('/settings/apps') },
    { id: 'shortcuts', label: 'Keyboard shortcuts', icon: 'keyboard', hint: '?', run: () => setShortcuts(true) },
  ]
  const onHit = (h: SearchHit) => {
    const q = new URLSearchParams({ path: h.path })
    if (h.kind === 'endpoint') { navigate(`/c/${h.codebase_id}/api?q=${encodeURIComponent(h.title.split(' ').slice(1).join(' '))}`); return }
    if (h.kind === 'table') { navigate(`/c/${h.codebase_id}/data?table=${encodeURIComponent(h.title)}`); return }
    if (h.kind === 'env') { navigate(`/c/${h.codebase_id}/config?q=${encodeURIComponent(h.title)}`); return }
    if (h.line) q.set('line', String(h.line))
    navigate(`/c/${h.codebase_id}/structure?${q}`)
  }

  return (
    <CrumbCtx.Provider value={setCrumb}>
      <div className={`app ${sidebarOpen ? 'with-sidebar' : 'no-sidebar'} ${mobile ? 'is-mobile' : ''} ${drawer ? 'drawer-mode' : ''}`}>
        {drawer && sidebarOpen && <div className="drawer-backdrop" onClick={e => { if (e.timeStamp - drawerOpenedAt.current > 400) setSidebarOpen(false) }} />}
        <div className={`sidebar-wrap ${sidebarOpen ? 'open' : ''}`}>
          <Sidebar onNavigate={closeDrawer} onAdd={() => { closeDrawer(); setAdding(true) }} />
        </div>
        <div className="main">
          <TopBar onMenu={toggleSidebar} onPalette={() => setPalette(true)} mobile={mobile} compact={narrow} sidebarOpen={sidebarOpen} crumb={crumb} />
          <main className="content"><div className="content-inner"><Outlet /></div></main>
        </div>
        {mobile && !current && <button type="button" className="fab" aria-label="Add a codebase" title="Add a codebase" onClick={() => setAdding(true)}>
          <Icon name="plus" size={24} strokeWidth={2.2} />
        </button>}
        {palette && <CommandPalette onClose={() => setPalette(false)} commands={commands} onHit={onHit} />}
        {shortcuts && <ShortcutsSheet onClose={() => setShortcuts(false)} />}
        {adding && <AddCodebase onClose={() => setAdding(false)} onAdded={c => { setAdding(false); reload(); navigate(`/c/${c.id}`) }} />}
      </div>
    </CrumbCtx.Provider>
  )
}

function TopBar({ onMenu, onPalette, mobile, compact, sidebarOpen, crumb }: { onMenu: () => void; onPalette: () => void; mobile: boolean; compact: boolean; sidebarOpen: boolean; crumb: string }) {
  const { user, themeMode, setThemeMode, status, setUser } = useApp()
  const navigate = useNavigate()
  const loc = useLocation()
  const inCodebase = loc.pathname.startsWith('/c/')
  const signOut = async () => { await api.post(`${V1}/auth/logout`).catch(() => {}); setUser(null); navigate('/login') }
  const avatarMenu = (): MenuItem[] => [
    { heading: user?.name || user?.email || '' },
    { label: 'Profile', icon: 'user', onSelect: () => navigate('/settings/profile') },
    { label: 'Settings', icon: 'settings', onSelect: () => navigate('/settings'), hint: ',' },
    { label: 'Keyboard shortcuts', icon: 'keyboard', onSelect: () => window.dispatchEvent(new CustomEvent('pbp:shortcuts')), hint: '?' },
    'sep',
    { heading: 'Appearance' },
    { label: 'System', checked: themeMode === 'system', onSelect: () => setThemeMode('system') },
    { label: 'Light', checked: themeMode === 'light', onSelect: () => setThemeMode('light') },
    { label: 'Dark', checked: themeMode === 'dark', onSelect: () => setThemeMode('dark') },
    ...(status?.auth_mode === 'local' ? ['sep' as const, { label: 'Sign out', icon: 'log-out', onSelect: signOut }] : []),
  ]
  return (
    <header className="topbar">
      <button type="button" className="btn ghost icon-only" onClick={onMenu} aria-label={sidebarOpen ? 'Hide the sidebar' : 'Show the sidebar'} title="Menu ([)"><Icon name={mobile || compact ? 'menu' : 'sidebar'} size={18} /></button>
      <h1 className="range-title">
        {inCodebase && crumb ? <><button type="button" className="crumb" onClick={() => navigate('/')}>Codebases</button><Icon name="chevron-right" size={16} className="crumb-sep" /><span className="ellipsis">{crumb}</span></> : 'Codebases'}
      </h1>
      <span className="spacer" />
      <button type="button" className="btn ghost icon-only" onClick={onPalette} aria-label="Search" title="Search (⌘K or /)"><Icon name="search" size={18} /></button>
      <AskButton />
      <MenuButton label="Account" className="avatar-btn" align="right" width={220} items={avatarMenu}>
        <Avatar name={user?.name} email={user?.email} size={30} />
      </MenuButton>
    </header>
  )
}
