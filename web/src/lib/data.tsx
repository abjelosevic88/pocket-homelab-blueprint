// The person's codebases, kept fresh while a scan is queued or running.
import React, { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'
import { api, V1 } from './api'
import type { Codebase } from './types'

interface DataState {
  codebases: Codebase[] | null
  reload: () => Promise<void>
  busy: (c: Codebase) => boolean
}
const Ctx = createContext<DataState>({ codebases: null, reload: async () => {}, busy: () => false })
export const useData = () => useContext(Ctx)

export const isBusy = (c: Codebase) => c.latest?.status === 'queued' || c.latest?.status === 'running'

export function DataProvider({ children }: { children: React.ReactNode }) {
  const [codebases, setCodebases] = useState<Codebase[] | null>(null)
  const timer = useRef<number>(0)
  const reload = useCallback(async () => {
    const list = await api.get<Codebase[]>(`${V1}/codebases`).catch(() => null)
    if (list) setCodebases(list)
  }, [])
  useEffect(() => { reload() }, [reload])
  // poll only while something is scanning; every 2 s, never while the tab is hidden
  const scanning = !!codebases?.some(isBusy)
  useEffect(() => {
    window.clearInterval(timer.current)
    if (!scanning) return
    timer.current = window.setInterval(() => { if (!document.hidden) reload() }, 2000)
    return () => window.clearInterval(timer.current)
  }, [scanning, reload])
  return <Ctx.Provider value={{ codebases, reload, busy: isBusy }}>{children}</Ctx.Provider>
}
