import { Navigate, Route, Routes } from 'react-router-dom'
import AuthGate from './components/AuthGate'
import Layout from './components/Layout'
import Accounts from './pages/Accounts'
import Dashboard from './pages/Dashboard'
import Operations from './pages/Operations'
import Proxies from './pages/Proxies'
import SchedulerBoard from './pages/SchedulerBoard'
import Settings from './pages/Settings'
import Guide from './pages/Guide'
import ApiReference from './pages/ApiReference'
import APIKeys from './pages/APIKeys'
import Usage from './pages/Usage'
import ImageStudio from './pages/ImageStudio'
import PromptFilter from './pages/PromptFilter'

export default function App() {
  return (
    <AuthGate>
      <Layout>
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/accounts" element={<Accounts />} />
          <Route path="/api-keys" element={<APIKeys />} />
          <Route path="/proxies" element={<Proxies />} />
          <Route path="/images" element={<Navigate to="/images/studio" replace />} />
          <Route path="/images/:view" element={<ImageStudio />} />
          <Route path="/prompt-filter" element={<Navigate to="/prompt-filter/overview" replace />} />
          <Route path="/prompt-filter/:view" element={<PromptFilter />} />
          <Route path="/ops" element={<Operations />} />
          <Route path="/ops/scheduler" element={<SchedulerBoard />} />
          <Route path="/usage" element={<Usage />} />
          <Route path="/settings" element={<Settings />} />
          <Route path="/docs" element={<Guide />} />
          <Route path="/api-reference" element={<ApiReference />} />
        </Routes>
      </Layout>
    </AuthGate>
  )
}
