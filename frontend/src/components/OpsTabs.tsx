import { NavLink } from 'react-router-dom'
import { Activity, AlertCircle, ServerCog, Workflow } from 'lucide-react'
import { useTranslation } from 'react-i18next'

const tabs = [
  { to: '/ops/overview', labelKey: 'ops.tabs.overview', icon: <Activity className="size-4" /> },
  { to: '/ops/runtime', labelKey: 'ops.tabs.runtime', icon: <ServerCog className="size-4" /> },
  { to: '/ops/errors', labelKey: 'ops.tabs.errors', icon: <AlertCircle className="size-4" /> },
  { to: '/ops/scheduler', labelKey: 'ops.tabs.scheduler', icon: <Workflow className="size-4" /> },
]

export default function OpsTabs() {
  const { t } = useTranslation()

  return (
    <div className="mb-5 flex max-w-full items-center gap-1.5 overflow-x-auto border-b border-border pb-3 [-ms-overflow-style:none] [scrollbar-width:none] [&::-webkit-scrollbar]:hidden sm:justify-center">
      {tabs.map((tab) => (
        <NavLink
          key={tab.to}
          to={tab.to}
          className={({ isActive }) =>
            `inline-flex h-9 shrink-0 items-center gap-2 rounded-lg border px-3 text-[13px] font-semibold transition-colors whitespace-nowrap ${
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
