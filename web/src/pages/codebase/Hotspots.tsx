// Big files that change often (from the git history): where bugs and slow reviews tend to live.
import { useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { api, V1 } from '../../lib/api'
import { ago } from '../../lib/format'
import type { Author, Hotspot } from '../../lib/types'
import { langColor } from '../../components/icons'
import { Card, Loading, Segmented, useAsync } from '../../components/ui'
import { useCB } from './Frame'

type Sort = 'score' | 'churn' | 'lines' | 'imported_by' | 'todos'

export default function Hotspots() {
  const { scanId, open } = useCB()
  const [sp] = useSearchParams()
  const { data, error } = useAsync(() => api.get<{ hotspots: Hotspot[]; git: boolean; authors: Author[] }>(`${V1}/scans/${scanId}/hotspots`), [scanId])
  const [sort, setSort] = useState<Sort>((sp.get('sort') as Sort) || 'score')
  const list = useMemo(() => [...(data?.hotspots || [])].sort((a, b) => (b[sort] as number) - (a[sort] as number) || a.path.localeCompare(b.path)).slice(0, 150), [data, sort])
  if (error) return <p className="bad">{error}</p>
  if (!data) return <Loading />
  const max = Math.max(1, ...list.map(h => h[sort] as number))
  return (
    <div className="stack">
      {!data.git && <div className="callout"><span>There is no git history in this scan, so “score” uses only how big each file is and how many others use it. Scan a git repository, or upload an archive with its <code>.git</code> folder, to see what changes most.</span></div>}
      <div className="row wrap">
        <Segmented size="sm" value={sort} onChange={setSort} options={[
          { value: 'score', label: 'Score' }, ...(data.git ? [{ value: 'churn' as Sort, label: 'Commits' }] : []),
          { value: 'lines', label: 'Lines' }, { value: 'imported_by', label: 'Used by' }, { value: 'todos', label: 'TODOs' }]} />
      </div>
      <Card flush>
        <div className="scroll-x"><table className="table compact hot"><thead><tr>
          <th>File</th><th className="bar-col" /><th className="right">Lines</th>{data.git && <th className="right">Commits</th>}{data.git && <th className="right hide-sm">Authors</th>}<th className="right hide-sm">Used by</th><th className="right hide-sm">TODOs</th>{data.git && <th className="hide-sm">Last change</th>}
        </tr></thead><tbody>{list.map(h => (
          <tr key={h.path} onClick={() => open(h.path)} className="clickable">
            <td className="mono small"><i className="ldot" style={{ background: langColor(h.lang) }} />{h.path}</td>
            <td className="bar-col"><span className="hbar-track"><span className="hbar-fill" style={{ width: `${(h[sort] as number) / max * 100}%`, background: sort === 'score' ? 'var(--danger)' : 'var(--accent)' }} /></span></td>
            <td className="right tnum small">{h.lines.toLocaleString()}</td>
            {data.git && <td className="right tnum small">{h.churn || ''}</td>}
            {data.git && <td className="right tnum small hide-sm">{h.authors || ''}</td>}
            <td className="right tnum small hide-sm">{h.imported_by || ''}</td>
            <td className="right tnum small hide-sm">{h.todos || ''}</td>
            {data.git && <td className="small muted hide-sm">{h.last_change ? ago(h.last_change) : ''}</td>}
          </tr>))}</tbody></table></div>
      </Card>
      {data.authors.length > 0 && <Card title="Who commits" sub="In the history that was read">
        <div className="hbars">{data.authors.slice(0, 12).map(a => (
          <div key={a.name} className="hbar"><span className="ellipsis small">{a.name}</span><span className="hbar-track"><span className="hbar-fill" style={{ width: `${a.commits / data.authors[0].commits * 100}%`, background: 'var(--accent)' }} /></span><span className="hbar-v tnum small">{a.commits}</span></div>
        ))}</div>
      </Card>}
    </div>
  )
}
