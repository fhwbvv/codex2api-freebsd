import { lazy, Suspense } from 'react'
import { Navigate, Route, Routes } from 'react-router-dom'
import AuthGate from './components/AuthGate'
import Layout from './components/Layout'
import RouteErrorBoundary from './components/RouteErrorBoundary'
import StateShell from './components/StateShell'
import Dashboard from './pages/Dashboard'

const Accounts = lazy(() => import('./pages/Accounts'))
const Operations = lazy(() => import('./pages/Operations'))
const OperationsErrors = lazy(() => import('./pages/OperationsErrors'))
const Proxies = lazy(() => import('./pages/Proxies'))
const SchedulerBoard = lazy(() => import('./pages/SchedulerBoard'))
const Settings = lazy(() => import('./pages/Settings'))
const Guide = lazy(() => import('./pages/Guide'))
const ApiReference = lazy(() => import('./pages/ApiReference'))
const APIKeys = lazy(() => import('./pages/APIKeys'))
const Usage = lazy(() => import('./pages/Usage'))
const ImageStudio = lazy(() => import('./pages/ImageStudio'))
const PromptFilter = lazy(() => import('./pages/PromptFilter'))

export default function App() {
  return (
    <AuthGate>
      <Layout>
        <RouteErrorBoundary>
          <Suspense fallback={<StateShell variant="page" loading>{null}</StateShell>}>
            <Routes>
              <Route path="/" element={<Dashboard />} />
              <Route path="/accounts" element={<Accounts />} />
              <Route path="/api-keys" element={<APIKeys />} />
              <Route path="/proxies" element={<Proxies />} />
              <Route path="/images" element={<Navigate to="/images/studio" replace />} />
              <Route path="/images/:view" element={<ImageStudio />} />
              <Route path="/prompt-filter" element={<Navigate to="/prompt-filter/overview" replace />} />
              <Route path="/prompt-filter/:view" element={<PromptFilter />} />
              <Route path="/ops" element={<Navigate to="/ops/overview" replace />} />
              <Route path="/ops/overview" element={<Operations />} />
              <Route path="/ops/errors" element={<OperationsErrors />} />
              <Route path="/ops/scheduler" element={<SchedulerBoard />} />
              <Route path="/usage" element={<Usage />} />
              <Route path="/settings" element={<Settings />} />
              <Route path="/docs" element={<Guide />} />
              <Route path="/api-reference" element={<ApiReference />} />
            </Routes>
          </Suspense>
        </RouteErrorBoundary>
      </Layout>
    </AuthGate>
  )
}
