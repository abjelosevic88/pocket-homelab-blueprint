import { Modal } from './ui'

const GROUPS: { title: string; keys: [string[], string][] }[] = [
  { title: 'Codebases', keys: [[['n'], 'Add a codebase'], [['r'], 'Scan the open codebase again']] },
  { title: 'In a codebase', keys: [[['1'], 'Overview'], [['2'], 'Structure'], [['3'], 'Graph'], [['4'], 'API'], [['5'], 'Data'], [['6'], 'Config'], [['7'], 'Hotspots'], [['8'], 'Scans']] },
  { title: 'Go to', keys: [[['h'], 'All codebases'], [[','], 'Settings'], [['/'], 'Search & commands'], [['⌘', 'K'], 'Search & commands']] },
  { title: 'Window', keys: [[['['], 'Show or hide the sidebar'], [['esc'], 'Close a dialog or panel'], [['?'], 'This sheet']] },
]

export function ShortcutsSheet({ onClose }: { onClose: () => void }) {
  return (
    <Modal title="Keyboard shortcuts" onClose={onClose} size="lg">
      <div className="shortcuts">
        {GROUPS.map(g => (
          <section key={g.title}>
            <h4 className="sc-h">{g.title}</h4>
            {g.keys.map(([keys, label]) => (
              <div key={label + keys.join('')} className="sc-row"><span>{label}</span><span className="sc-keys">{keys.map(k => <kbd key={k}>{k}</kbd>)}</span></div>
            ))}
          </section>
        ))}
      </div>
    </Modal>
  )
}
