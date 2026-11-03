// Share a codebase with other people on this box: each can look at every scan, or also scan it again.
import { useCallback, useEffect, useMemo, useState } from 'react'
import { api, V1 } from '../lib/api'
import type { Codebase, Person, Share, ShareRole } from '../lib/types'
import { Icon } from './icons'
import { Select } from './Select'
import { Avatar, errMsg, Loading, Modal, Segmented, useToast } from './ui'

const ROLES: { value: ShareRole; label: string; title: string }[] = [
  { value: 'view', label: 'Can view', title: 'Look at every scan, keep their own notes, ask the AI' },
  { value: 'scan', label: 'Can scan', title: 'Also scan it again' },
]

export function ShareDialog({ c, onClose, onChange }: { c: Codebase; onClose: () => void; onChange: () => void }) {
  const { toast } = useToast()
  const [shares, setShares] = useState<Share[] | null>(null)
  const [people, setPeople] = useState<Person[]>([])
  const [pick, setPick] = useState('')
  const [role, setRole] = useState<ShareRole>('view')
  const base = `${V1}/codebases/${c.id}/shares`

  const load = useCallback(async () => {
    try { setShares(await api.get<Share[]>(base)) } catch (e) { toast(errMsg(e), 'err') }
  }, [base, toast])
  useEffect(() => {
    void load()
    api.get<Person[]>(`${V1}/people`).then(setPeople).catch(() => setPeople([]))
  }, [load])

  const options = useMemo(() => people.filter(p => !shares?.some(s => s.user_id === p.id))
    .map(p => ({ value: String(p.id), label: p.name || p.email, hint: p.name ? p.email : undefined })), [people, shares])

  const set = async (uid: number, r: ShareRole) => {
    try { await api.put(`${base}/${uid}`, { role: r }); await load(); onChange() } catch (e) { toast(errMsg(e), 'err') }
  }
  const add = async () => { if (!pick) return; await set(Number(pick), role); setPick('') }
  const remove = async (s: Share) => {
    try { await api.del(`${base}/${s.user_id}`); await load(); onChange() } catch (e) { toast(errMsg(e), 'err') }
  }

  return (
    <Modal title={`Share ${c.name}`} onClose={onClose}>
      <p className="muted small">
        People on this Pocket Blueprint find it among their codebases and see every scan of it. Their notes stay their own;
        editing, deleting and sharing stay with you.
      </p>
      {people.length === 0 && shares?.length === 0 ? (
        <p className="muted">No one else has signed in to this Pocket Blueprint yet. Once they have, you can share with them here.</p>
      ) : (
        <div className="share-add">
          <Select className="grow" ariaLabel="Person" value={pick} onChange={setPick} options={options} placeholder={options.length ? 'Choose a person…' : 'Shared with everyone here'} disabled={!options.length} />
          <Segmented size="sm" label="Role" value={role} onChange={setRole} options={ROLES} />
          <button type="button" className="btn primary sm" disabled={!pick} onClick={add}>Share</button>
        </div>
      )}
      {!shares ? <Loading /> : shares.length > 0 && (
        <ul className="share-list" aria-label="Shared with">
          {shares.map(s => (
            <li key={s.user_id}>
              <Avatar name={s.name} email={s.email} size={28} />
              <span className="grow ellipsis"><b>{s.name || s.email}</b>{s.name && <span className="muted small"> · {s.email}</span>}</span>
              <Segmented size="sm" label={`What ${s.name || s.email} may do`} value={s.role} onChange={r => void set(s.user_id, r)} options={ROLES} />
              <button type="button" className="btn ghost icon-only sm" onClick={() => void remove(s)} aria-label={`Stop sharing with ${s.name || s.email}`} title="Stop sharing"><Icon name="x" size={15} /></button>
            </li>
          ))}
        </ul>
      )}
      <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Done</button></div>
    </Modal>
  )
}
