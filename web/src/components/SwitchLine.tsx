import { Switch } from './ui'

/** A switch with its label next to it. */
export function SwitchLine({ checked, onChange, label, disabled }: { checked: boolean; onChange: (v: boolean) => void; label: string; disabled?: boolean }) {
  return <div className="check-line"><Switch checked={checked} onChange={onChange} label={label} disabled={disabled} /><span className="small">{label}</span></div>
}
