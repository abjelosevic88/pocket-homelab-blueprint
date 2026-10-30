// How the latest scan is doing: a progress bar while it runs, the error when it failed.
import { api, V1 } from '../lib/api'
import { scanLabel } from '../lib/code'
import type { Scan } from '../lib/types'
import { Icon } from './icons'
import { ago } from '../lib/format'

export function ScanProgress({ scan, onCancel }: { scan: Scan; onCancel?: () => void }) {
  const running = scan.status === 'running'
  return (
    <div className="scan-prog">
      <div className="row small">
        <span className="grow ellipsis">{scanLabel(scan)}</span>
        {running && <span className="tnum muted">{scan.progress}%</span>}
        {onCancel && <button type="button" className="btn sm ghost" onClick={async () => { await api.post(`${V1}/scans/${scan.id}/cancel`).catch(() => {}); onCancel() }}>Stop</button>}
      </div>
      <span className="progress"><span style={{ width: `${running ? Math.max(3, scan.progress) : 0}%` }} /></span>
    </div>
  )
}

export function ScanFailed({ scan }: { scan: Scan }) {
  return (
    <div className="callout danger">
      <Icon name="alert" size={16} />
      <span><b>{scan.status === 'cancelled' ? 'The scan was stopped' : 'The last scan failed'}</b> {scan.finished_at && <span className="small">({ago(scan.finished_at)})</span>}{scan.error && <><br />{scan.error}</>}</span>
    </div>
  )
}
