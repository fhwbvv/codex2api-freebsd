import type { MouseEvent as ReactMouseEvent, ReactNode } from 'react'
import { Check, Moon, Palette, Sun } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import PageHeader from '../components/PageHeader'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { COLOR_THEMES, type ColorThemeDef, type Theme, useTheme } from '../hooks/useTheme'
import { cn } from '@/lib/utils'

type ThemeModeOption = {
  id: Theme
  icon: ReactNode
  labelKey: string
  descriptionKey: string
}

const modeOptions: ThemeModeOption[] = [
  {
    id: 'light',
    icon: <Sun className="size-4" />,
    labelKey: 'themeSettings.modeLight',
    descriptionKey: 'themeSettings.modeLightDesc',
  },
  {
    id: 'dark',
    icon: <Moon className="size-4" />,
    labelKey: 'themeSettings.modeDark',
    descriptionKey: 'themeSettings.modeDarkDesc',
  },
]

function ThemePreviewCard() {
  const { t } = useTranslation()

  return (
    <Card className="min-w-0 py-0">
      <CardContent className="min-w-0 p-5">
        <div className="mb-4">
          <h3 className="text-base font-semibold leading-tight text-foreground">
            {t('themeSettings.previewTitle')}
          </h3>
          <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
            {t('themeSettings.previewDesc')}
          </p>
        </div>
        <div className="overflow-hidden rounded-lg border border-border bg-background shadow-sm">
          <div className="flex min-h-[220px] min-w-0">
            <div className="w-[132px] shrink-0 border-r border-border bg-[hsl(var(--sidebar-background))] p-3 max-sm:w-[96px]">
              <div className="mb-4 flex items-center gap-2">
                <span className="size-7 rounded-lg bg-primary/15 ring-1 ring-primary/20" />
                <span className="h-3 w-14 rounded-full bg-foreground/16 max-sm:hidden" />
              </div>
              <div className="space-y-2">
                <span className="block h-7 rounded-md bg-primary/12 ring-1 ring-primary/20" />
                <span className="block h-7 rounded-md bg-muted/70" />
                <span className="block h-7 rounded-md bg-muted/50" />
              </div>
            </div>
            <div className="min-w-0 flex-1 p-4">
              <div className="mb-4 flex items-start justify-between gap-3">
                <div className="min-w-0 flex-1">
                  <span className="mb-2 block h-4 w-36 max-w-full rounded-full bg-foreground/18" />
                  <span className="block h-3 w-56 max-w-full rounded-full bg-muted" />
                </div>
                <Button size="sm">
                  <Palette className="size-3.5" />
                  {t('themeSettings.previewAction')}
                </Button>
              </div>
              <div className="grid gap-3 sm:grid-cols-3">
                <div className="min-h-[74px] rounded-lg border border-border bg-card p-3">
                  <span className="mb-3 block h-3 w-16 rounded-full bg-muted" />
                  <span className="block h-5 w-20 rounded-full bg-primary/18" />
                </div>
                <div className="min-h-[74px] rounded-lg border border-border bg-card p-3">
                  <span className="mb-3 block h-3 w-20 rounded-full bg-muted" />
                  <span className="block h-5 w-16 rounded-full bg-emerald-500/18" />
                </div>
                <div className="min-h-[74px] rounded-lg border border-border bg-card p-3">
                  <span className="mb-3 block h-3 w-14 rounded-full bg-muted" />
                  <span className="block h-5 w-24 rounded-full bg-amber-500/18" />
                </div>
              </div>
              <div className="mt-3 overflow-hidden rounded-lg border border-border bg-card">
                {[0, 1, 2].map((row) => (
                  <div key={row} className="grid grid-cols-[1fr_72px_56px] items-center gap-3 border-b border-border px-3 py-2.5 last:border-b-0">
                    <span className="h-3 rounded-full bg-muted" />
                    <span className="h-3 rounded-full bg-muted/80" />
                    <span className={cn('h-5 rounded-full', row === 0 ? 'bg-primary/18' : 'bg-muted/70')} />
                  </div>
                ))}
              </div>
            </div>
          </div>
        </div>
      </CardContent>
    </Card>
  )
}

function ThemeStyleCard({
  item,
  active,
  onSelect,
}: {
  item: ColorThemeDef
  active: boolean
  onSelect: () => void
}) {
  const { t } = useTranslation()

  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onSelect}
      className={cn(
        'group min-w-0 rounded-lg border bg-card p-3 text-left shadow-sm outline-none transition-all hover:-translate-y-0.5 hover:border-primary/40 hover:shadow-md focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/35',
        active ? 'border-primary/55 ring-2 ring-primary/20' : 'border-border',
      )}
    >
      <div
        className="relative aspect-[16/10] overflow-hidden rounded-md border shadow-inner"
        style={{ backgroundColor: item.previewBg, borderColor: item.previewMuted }}
      >
        <div
          className="absolute inset-y-0 left-0 w-[28%] border-r"
          style={{ backgroundColor: item.previewSurface, borderColor: item.previewMuted }}
        >
          <span className="mx-2 mt-2 block h-4 rounded-md" style={{ backgroundColor: item.previewPrimary }} />
          <span className="mx-2 mt-2 block h-2 rounded-full" style={{ backgroundColor: item.previewMuted }} />
          <span className="mx-2 mt-1.5 block h-2 rounded-full" style={{ backgroundColor: item.previewMuted }} />
        </div>
        <div className="ml-[28%] p-2.5">
          <span className="mb-2 block h-3 w-20 rounded-full" style={{ backgroundColor: item.previewPrimary }} />
          <div className="grid grid-cols-2 gap-1.5">
            <span className="h-8 rounded-md" style={{ backgroundColor: item.previewSurface }} />
            <span className="h-8 rounded-md" style={{ backgroundColor: item.previewMuted }} />
          </div>
          <span className="mt-2 block h-2 rounded-full" style={{ backgroundColor: item.previewMuted }} />
          <span className="mt-1.5 block h-2 w-2/3 rounded-full" style={{ backgroundColor: item.previewMuted }} />
        </div>
        {active ? (
          <span className="absolute right-2 top-2 inline-flex size-6 items-center justify-center rounded-full bg-primary text-primary-foreground shadow-sm">
            <Check className="size-3.5" />
          </span>
        ) : null}
      </div>
      <div className="mt-3 flex min-w-0 items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="truncate text-sm font-semibold text-foreground">{t(item.nameKey)}</div>
          <p className="mt-1 line-clamp-2 text-xs leading-relaxed text-muted-foreground">
            {t(item.descriptionKey)}
          </p>
        </div>
        <div className="flex shrink-0 items-center pt-0.5">
          <span className="size-3.5 rounded-full border border-black/5 shadow-inner" style={{ backgroundColor: item.previewPrimary }} />
          <span className="-ml-1.5 size-3.5 rounded-full border border-black/5 shadow-inner" style={{ backgroundColor: item.previewBg }} />
        </div>
      </div>
    </button>
  )
}

export default function ThemeSettings() {
  const { t } = useTranslation()
  const { theme, setTheme, colorTheme, setColorTheme } = useTheme()
  const activeColorTheme = COLOR_THEMES.find((item) => item.id === colorTheme) ?? COLOR_THEMES[0]

  const handleModeChange = (nextTheme: Theme, event: ReactMouseEvent<HTMLButtonElement>) => {
    setTheme(nextTheme, event)
  }

  return (
    <div className="mx-auto max-w-7xl">
      <PageHeader
        title={t('themeSettings.title')}
        description={t('themeSettings.description')}
        actions={(
          <span className="inline-flex min-h-9 items-center gap-2 rounded-lg border border-border bg-card px-3 text-sm font-semibold text-foreground shadow-sm">
            <Palette className="size-4 text-primary" />
            {t('themeSettings.currentTheme', { theme: t(activeColorTheme.nameKey) })}
          </span>
        )}
      />

      <div className="grid items-start gap-4 lg:grid-cols-[minmax(0,0.8fr)_minmax(0,1.2fr)]">
        <Card className="min-w-0 py-0">
          <CardContent className="min-w-0 p-5">
            <div>
              <h3 className="text-base font-semibold leading-tight text-foreground">
                {t('themeSettings.modeTitle')}
              </h3>
              <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
                {t('themeSettings.modeDesc')}
              </p>
            </div>
            <div className="mt-4 grid min-w-0 gap-2 sm:grid-cols-2 lg:grid-cols-1 xl:grid-cols-2">
              {modeOptions.map((item) => {
                const active = theme === item.id
                return (
                  <button
                    key={item.id}
                    type="button"
                    aria-pressed={active}
                    onClick={(event) => handleModeChange(item.id, event)}
                    className={cn(
                      'flex min-h-[96px] min-w-0 items-start gap-3 rounded-lg border p-3 text-left outline-none transition-all hover:border-primary/40 hover:bg-muted/35 focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/35',
                      active ? 'border-primary/55 bg-primary/10 text-primary' : 'border-border bg-background text-foreground',
                    )}
                  >
                    <span className={cn('mt-0.5 inline-flex size-9 shrink-0 items-center justify-center rounded-lg', active ? 'bg-primary text-primary-foreground' : 'bg-muted text-muted-foreground')}>
                      {item.icon}
                    </span>
                    <span className="min-w-0">
                      <span className="flex items-center gap-2 text-sm font-semibold">
                        {t(item.labelKey)}
                        {active ? <Check className="size-3.5" /> : null}
                      </span>
                      <span className="mt-1 block text-xs leading-relaxed text-muted-foreground">
                        {t(item.descriptionKey)}
                      </span>
                    </span>
                  </button>
                )
              })}
            </div>
          </CardContent>
        </Card>

        <ThemePreviewCard />
      </div>

      <section className="mt-6">
        <div className="mb-4">
          <h3 className="text-lg font-semibold leading-tight text-foreground">
            {t('themeSettings.stylesTitle')}
          </h3>
          <p className="mt-1 text-sm leading-relaxed text-muted-foreground">
            {t('themeSettings.stylesDesc')}
          </p>
        </div>
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
          {COLOR_THEMES.map((item) => (
            <ThemeStyleCard
              key={item.id}
              item={item}
              active={item.id === colorTheme}
              onSelect={() => setColorTheme(item.id)}
            />
          ))}
        </div>
      </section>
    </div>
  )
}
