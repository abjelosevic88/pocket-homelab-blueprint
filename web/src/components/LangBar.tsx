// A thin bar of the languages by lines, with an optional legend.
import type { LangStat } from '../lib/types'
import { langColor } from './icons'

export function LangBar({ langs, legend = false, height = 8, max = 6 }: { langs: LangStat[]; legend?: boolean; height?: number; max?: number }) {
  const total = langs.reduce((n, l) => n + l.lines, 0) || 1
  const shown = langs.slice(0, max)
  const rest = langs.slice(max).reduce((n, l) => n + l.lines, 0)
  return (
    <div className="langbar">
      <div className="stacked" style={{ height }} role="img" aria-label={shown.map(l => `${l.lang} ${Math.round(l.lines / total * 100)}%`).join(', ')}>
        {shown.map(l => <span key={l.lang} style={{ width: `${l.lines / total * 100}%`, background: langColor(l.lang) }} title={`${l.lang}: ${l.lines.toLocaleString()} lines`} />)}
        {rest > 0 && <span style={{ width: `${rest / total * 100}%`, background: 'var(--border-strong)' }} title="Other" />}
      </div>
      {legend && <div className="legend small muted">
        {shown.map(l => <span key={l.lang}><i style={{ background: langColor(l.lang) }} />{l.lang} <span className="tnum">{(l.lines / total * 100).toFixed(l.lines / total < .1 ? 1 : 0)}%</span></span>)}
        {rest > 0 && <span><i style={{ background: 'var(--border-strong)' }} />Other</span>}
      </div>}
    </div>
  )
}
