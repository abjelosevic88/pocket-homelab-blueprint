// Environment variables, services, entry points and outside packages.
import { useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { api, V1 } from '../../lib/api'
import type { EntryPoint, EnvVar, External, Service, SpecFile } from '../../lib/types'
import { Icon } from '../../components/icons'
import { Card, Loading, Segmented, useAsync } from '../../components/ui'
import { Select } from '../../components/Select'
import { useCB } from './Frame'

interface Cfg { env_vars: EnvVar[]; services: Service[]; entry_points: EntryPoint[]; externals: External[]; spec_files: SpecFile[] }

export default function Config() {
  const { scanId, source, open } = useCB()
  const [sp] = useSearchParams()
  const { data, error } = useAsync(() => api.get<Cfg>(`${V1}/scans/${scanId}/config`), [scanId])
  const [q, setQ] = useState(sp.get('q') || '')
  const [envFilter, setEnvFilter] = useState<'all' | 'undocumented' | 'unused'>('all')
  const [eco, setEco] = useState('')
  const [pq, setPq] = useState('')
  const envs = useMemo(() => (data?.env_vars || []).filter(v => (!q || v.name.toLowerCase().includes(q.toLowerCase())) &&
    (envFilter === 'all' || (envFilter === 'undocumented' ? !v.documented && v.uses.length > 0 : v.uses.length === 0))), [data, q, envFilter])
  const ecos = useMemo(() => [...new Set((data?.externals || []).map(x => x.ecosystem))].sort(), [data])
  const pkgs = useMemo(() => (data?.externals || []).filter(x => (!eco || x.ecosystem === eco) && (!pq || x.name.toLowerCase().includes(pq.toLowerCase())))
    .sort((a, b) => b.files - a.files || a.name.localeCompare(b.name)), [data, eco, pq])
  if (error) return <p className="bad">{error}</p>
  if (!data) return <Loading />
  const undocumented = data.env_vars.filter(v => !v.documented && v.uses.length).length
  const unused = data.env_vars.filter(v => !v.uses.length).length
  const openUse = (u: string) => { const i = u.lastIndexOf(':'); source(u.slice(0, i), Number(u.slice(i + 1))) }
  return (
    <div className="stack loose">
      <Card title="Environment variables" sub={`${data.env_vars.length} found${undocumented ? `, ${undocumented} read but not documented` : ''}${unused ? `, ${unused} documented but never read` : ''}`} flush>
        <div className="row wrap filters pad-x">
          <div className="search-box"><Icon name="search" size={15} /><input value={q} onChange={e => setQ(e.target.value)} placeholder="Filter variables" aria-label="Filter variables" /></div>
          <Segmented size="sm" value={envFilter} onChange={setEnvFilter} options={[{ value: 'all', label: 'All' }, { value: 'undocumented', label: 'Not documented' }, { value: 'unused', label: 'Never read' }]} />
        </div>
        {envs.length === 0 ? <p className="pad muted small">No variables{data.env_vars.length ? ' match' : ' found'}.</p> :
          <div className="scroll-x"><table className="table compact"><thead><tr><th>Name</th><th>Read in</th><th>Default</th><th /></tr></thead><tbody>
            {envs.map(v => (
              <tr key={v.name}>
                <td className="mono">{v.name}</td>
                <td className="small">{v.uses.length ? <><button type="button" className="link-btn mono small" onClick={() => openUse(v.uses[0])}>{v.uses[0]}</button>{v.uses.length > 1 && <span className="faint"> +{v.uses.length - 1}</span>}</> : <span className="faint">never</span>}</td>
                <td className="mono small muted ellipsis" style={{ maxWidth: 220 }}>{v.default || ''}</td>
                <td className="right">{v.uses.length > 0 && !v.documented && <span className="badge warn">not documented</span>}{v.uses.length === 0 && <span className="badge">only documented</span>}</td>
              </tr>))}
          </tbody></table></div>}
      </Card>
      {data.services.length > 0 && <Card title="Services" sub="From the compose files" flush>
        <ul className="list">{data.services.map(s => (
          <li key={s.file + s.name}><button type="button" className="list-row" onClick={() => source(s.file)}>
            <Icon name="box" size={15} className="muted" />
            <span className="grow min0">
              <span className="block small"><b>{s.name}</b> {s.image && <span className="mono muted">{s.image}</span>}{s.build && <span className="muted"> built from <span className="mono">{s.build}</span></span>}</span>
              <span className="block xsmall muted">{s.ports?.length ? `ports ${s.ports.join(', ')}` : 'no ports'}{s.depends_on?.length ? ` · needs ${s.depends_on.join(', ')}` : ''}</span>
            </span>
          </button></li>))}</ul>
      </Card>}
      {data.entry_points.length > 0 && <Card title="Where it starts" flush>
        <ul className="list">{data.entry_points.map((e, i) => (
          <li key={i}><button type="button" className="list-row" onClick={() => open(e.path)}>
            <Icon name="entry" size={15} className="muted" /><span className="grow min0"><span className="block small"><b>{e.kind}</b> <span className="mono">{e.path || '.'}</span></span>{e.detail && <span className="block xsmall muted mono ellipsis">{e.detail}</span>}</span>
          </button></li>))}</ul>
      </Card>}
      <Card title="Outside packages" sub={`${data.externals.length} packages`} flush>
        <div className="row wrap filters pad-x">
          <div className="search-box"><Icon name="search" size={15} /><input value={pq} onChange={e => setPq(e.target.value)} placeholder="Filter packages" aria-label="Filter packages" /></div>
          {ecos.length > 1 && <Select value={eco} onChange={setEco} ariaLabel="Ecosystem" options={[{ value: '', label: 'Every ecosystem' }, ...ecos.map(e => ({ value: e, label: e }))]} />}
        </div>
        {pkgs.length === 0 ? <p className="pad muted small">None.</p> :
          <div className="scroll-x"><table className="table compact"><thead><tr><th>Package</th><th>Version</th><th>Used in</th><th>Listed in</th></tr></thead><tbody>
            {pkgs.map(x => (
              <tr key={x.ecosystem + x.name}>
                <td><span className="mono">{x.name}</span> <span className="faint xsmall">{x.ecosystem}</span>{x.dev && <span className="badge xs">dev</span>}</td>
                <td className="mono small muted">{x.version || ''}</td>
                <td className="small tnum">{x.files ? `${x.files} file${x.files > 1 ? 's' : ''}` : <span className="faint">not imported</span>}</td>
                <td className="small">{x.declared ? <button type="button" className="link-btn mono small" onClick={() => source(x.declared)}>{x.declared}</button> : <span className="badge warn" title="Imported but not listed in a manifest">not listed</span>}</td>
              </tr>))}
          </tbody></table></div>}
      </Card>
    </div>
  )
}
