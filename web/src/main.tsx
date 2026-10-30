import React, { lazy, Suspense } from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter, Navigate, Route, Routes, useLocation } from 'react-router-dom'
import './styles/base.css'
import './styles/blueprint.css'
import './styles/app.css'
import './styles/settings.css'
import { AppProvider, useApp } from './lib/app-context'
import { Loading, ToastProvider } from './components/ui'
import Layout from './components/Layout'
import { ErrorBoundary } from './components/ErrorBoundary'
import Login from './pages/Login'
import Codebases from './pages/Codebases'
import Frame from './pages/codebase/Frame'
import Overview from './pages/codebase/Overview'
import Structure from './pages/codebase/Structure'
import Api from './pages/codebase/Api'
import Config from './pages/codebase/Config'
import Hotspots from './pages/codebase/Hotspots'
import Scans from './pages/codebase/Scans'
import Settings from './pages/Settings'

// the graph library is big: load it with the pages that draw graphs
const GraphPage = lazy(() => import('./pages/codebase/GraphPage'))
const Data = lazy(() => import('./pages/codebase/Data'))
const later = (el: React.ReactNode) => <Suspense fallback={<Loading />}>{el}</Suspense>

function Gate({ children }: { children: React.ReactNode }) {
  const { status, user } = useApp()
  const loc = useLocation()
  if (!status) return <div className="boot"><img src="/favicon.svg" alt="" /><Loading /></div>
  if (status.needs_setup) return <Navigate to="/setup" replace />
  if (!user) return <Navigate to="/login" replace state={{ from: loc.pathname + loc.search }} />
  return <>{children}</>
}

function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login mode="login" />} />
      <Route path="/setup" element={<Login mode="setup" />} />
      <Route path="/settings/:tab?" element={<Gate><ErrorBoundary where="settings"><Settings /></ErrorBoundary></Gate>} />
      <Route element={<Gate><ErrorBoundary where="blueprint"><Layout /></ErrorBoundary></Gate>}>
        <Route path="/" element={<Codebases />} />
        <Route path="/c/:id" element={<Frame />}>
          <Route index element={<ErrorBoundary where="overview"><Overview /></ErrorBoundary>} />
          <Route path="structure" element={<ErrorBoundary where="structure"><Structure /></ErrorBoundary>} />
          <Route path="graph" element={<ErrorBoundary where="graph">{later(<GraphPage />)}</ErrorBoundary>} />
          <Route path="api" element={<ErrorBoundary where="api"><Api /></ErrorBoundary>} />
          <Route path="data" element={<ErrorBoundary where="data">{later(<Data />)}</ErrorBoundary>} />
          <Route path="config" element={<ErrorBoundary where="config"><Config /></ErrorBoundary>} />
          <Route path="hotspots" element={<ErrorBoundary where="hotspots"><Hotspots /></ErrorBoundary>} />
          <Route path="scans" element={<ErrorBoundary where="scans"><Scans /></ErrorBoundary>} />
        </Route>
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  )
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter>
      <ErrorBoundary where="app"><ToastProvider><AppProvider><App /></AppProvider></ToastProvider></ErrorBoundary>
    </BrowserRouter>
  </React.StrictMode>,
)
