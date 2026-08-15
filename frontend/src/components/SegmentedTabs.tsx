import type { ReactNode } from 'react'
import { NavLink } from 'react-router-dom'
import { cn } from '../lib/utils'

export type SegmentedTab = { value: string; label: ReactNode; to?: string }

/**
 * 分段选择控件（互斥视图切换），统一各页手搓的 segmented control。
 * 滑块动画来自 PayloadRules/PromptFilter 的 tablist 实现。
 * tab 带 to 时渲染为 NavLink（路由页签），否则为按钮（onValueChange 驱动）。
 */
export function SegmentedTabs({
  tabs,
  value,
  onValueChange,
  size = 'md',
  className,
}: {
  tabs: SegmentedTab[]
  value: string
  onValueChange?: (value: string) => void
  size?: 'sm' | 'md'
  className?: string
}) {
  const activeIndex = Math.max(0, tabs.findIndex((tab) => tab.value === value))
  const itemClass = (active: boolean) =>
    cn(
      'relative z-10 flex shrink-0 items-center justify-center gap-1.5 rounded-lg font-semibold transition-colors whitespace-nowrap',
      size === 'sm' ? 'h-8 px-2.5 text-[13px]' : 'h-9 px-3 text-sm',
      active ? 'text-primary' : 'text-muted-foreground hover:text-foreground',
      'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50',
    )

  return (
    <div
      role="tablist"
      className={cn(
        'relative flex max-w-full items-center gap-1 overflow-x-auto rounded-xl border border-border bg-background/80 p-1 shadow-sm [-ms-overflow-style:none] [scrollbar-width:none] [&::-webkit-scrollbar]:hidden sm:grid',
        className,
      )}
      style={{
        gridTemplateColumns: `repeat(${tabs.length}, minmax(0, 1fr))`,
      }}
    >
      <div
        aria-hidden
        className="pointer-events-none absolute left-1 top-1 hidden h-[calc(100%-0.5rem)] rounded-lg border border-primary/15 bg-primary/8 transition-transform duration-300 ease-out sm:block"
        style={{
          width: `calc((100% - 0.5rem) / ${tabs.length})`,
          transform: `translateX(${activeIndex * 100}%)`,
        }}
      />
      {tabs.map((tab) => {
        const active = tab.value === value
        return tab.to ? (
          <NavLink
            key={tab.value}
            to={tab.to}
            role="tab"
            aria-selected={active}
            className={cn(
              itemClass(active),
              active && 'max-sm:bg-primary/10 max-sm:border max-sm:border-primary/20',
            )}
          >
            {tab.label}
          </NavLink>
        ) : (
          <button
            key={tab.value}
            type="button"
            role="tab"
            aria-selected={active}
            onClick={() => onValueChange?.(tab.value)}
            className={cn(
              itemClass(active),
              active && 'max-sm:bg-primary/10 max-sm:border max-sm:border-primary/20',
            )}
          >
            {tab.label}
          </button>
        )
      })}
    </div>
  )
}
