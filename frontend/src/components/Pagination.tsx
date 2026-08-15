import { Button } from '@/components/ui/button'
import { Select } from '@/components/ui/select'
import { useTranslation } from 'react-i18next'

interface PaginationProps {
  page: number
  totalPages: number
  onPageChange: (page: number) => void
  totalItems: number
  pageSize: number
  onPageSizeChange?: (pageSize: number) => void
  pageSizeOptions?: number[]
}

type PagerItem = number | 'ellipsis-left' | 'ellipsis-right'

function buildPagerItems(page: number, totalPages: number): PagerItem[] {
  if (totalPages <= 7) {
    return Array.from({ length: totalPages }, (_, index) => index + 1)
  }

  if (page <= 4) {
    return [1, 2, 3, 4, 5, 'ellipsis-right', totalPages]
  }

  if (page >= totalPages - 3) {
    return [1, 'ellipsis-left', totalPages - 4, totalPages - 3, totalPages - 2, totalPages - 1, totalPages]
  }

  return [1, 'ellipsis-left', page - 1, page, page + 1, 'ellipsis-right', totalPages]
}

export default function Pagination({
  page,
  totalPages,
  onPageChange,
  totalItems,
  pageSize,
  onPageSizeChange,
  pageSizeOptions = [],
}: PaginationProps) {
  const { t } = useTranslation()
  if (totalItems <= 0) return null

  const normalizedTotalPages = Math.max(1, totalPages)
  const currentPage = Math.min(Math.max(page, 1), normalizedTotalPages)
  const pagerItems = buildPagerItems(currentPage, normalizedTotalPages)
  const selectOptions = pageSizeOptions.map((size) => ({
    label: t('common.pageSizeOption', { size }),
    value: String(size),
  }))

  const start = (currentPage - 1) * pageSize + 1
  const end = Math.min(currentPage * pageSize, totalItems)

  return (
    <div className="mt-3.5 flex flex-col gap-2.5 border-t border-border pt-3.5 sm:flex-row sm:items-center sm:justify-between">
      <span className="text-xs text-muted-foreground max-sm:text-center">
        {t('common.showingRange', { start, end, total: totalItems })}
      </span>
      <div className="flex flex-wrap items-center justify-between gap-2 max-sm:w-full sm:justify-end">
        {onPageSizeChange && selectOptions.length > 0 ? (
          <div className="flex items-center gap-1.5">
            <span className="text-xs font-medium text-muted-foreground">{t('common.pageSize')}</span>
            <div className="w-[96px] sm:w-[108px]">
              <Select
                value={String(pageSize)}
                onValueChange={(value) => onPageSizeChange(Number(value))}
                options={selectOptions}
                compact
              />
            </div>
          </div>
        ) : null}
        {normalizedTotalPages > 1 ? (
          <div className="flex items-center gap-1.5 max-sm:ml-auto">
            <Button
              variant="outline"
              size="sm"
              disabled={currentPage <= 1}
              onClick={() => onPageChange(currentPage - 1)}
              className="px-2.5 sm:px-3"
            >
              {t('common.prev')}
            </Button>
            <span className="px-1 text-xs font-semibold tabular-nums text-foreground sm:hidden">
              {currentPage} / {normalizedTotalPages}
            </span>
            <div className="hidden items-center gap-1 sm:flex">
              {pagerItems.map((item) => {
                if (typeof item !== 'number') {
                  return (
                    <span
                      key={item}
                      className="flex h-8 min-w-8 items-center justify-center px-1 text-sm text-muted-foreground"
                    >
                      ...
                    </span>
                  )
                }

                const isActive = item === currentPage
                return (
                  <Button
                    key={item}
                    variant={isActive ? 'default' : 'outline'}
                    size="sm"
                    aria-current={isActive ? 'page' : undefined}
                    className="min-w-8 px-2.5"
                    onClick={() => onPageChange(item)}
                  >
                    {item}
                  </Button>
                )
              })}
            </div>
            <Button
              variant="outline"
              size="sm"
              disabled={currentPage >= normalizedTotalPages}
              onClick={() => onPageChange(currentPage + 1)}
              className="px-2.5 sm:px-3"
            >
              {t('common.next')}
            </Button>
          </div>
        ) : (
          <span className="min-w-[80px] text-right text-[13px] font-semibold text-muted-foreground">
            {t('common.pageInfo', { current: currentPage, total: normalizedTotalPages })}
          </span>
        )}
      </div>
    </div>
  )
}
