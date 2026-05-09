import { NavLink } from 'react-router-dom'
import { Activity, AlertCircle, Workflow } from 'lucide-react'
import { useTranslation } from 'react-i18next'

const tabs = [
  { to: '/ops/overview', labelKey: 'ops.tabs.overview', icon: <Activity className="size-4" /> },
  { to: '/ops/errors', labelKey: 'ops.tabs.errors', icon: <AlertCircle className="size-4" /> },
  { to: '/ops/scheduler', labelKey: 'ops.tabs.scheduler', icon: <Workflow className="size-4" /> },
]

export default function OpsTabs() {
  const { t } = useTranslation()

  return (
    <div className="mb-6 flex flex-wrap items-center justify-center gap-2 border-b border-border pb-3">
      {tabs.map((tab) => (
        <NavLink
          key={tab.to}
          to={tab.to}
          className={({ isActive }) =>
            `inline-flex h-9 items-center gap-2 rounded-lg border px-3 text-[13px] font-semibold transition-colors ${
              isActive
                ? 'border-primary/25 bg-primary/10 text-primary'
                : 'border-transparent text-muted-foreground hover:bg-muted/60 hover:text-foreground'
            }`
          }
        >
          {tab.icon}
          {t(tab.labelKey)}
        </NavLink>
      ))}
    </div>
  )
}
