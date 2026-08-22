export type CountBreakdownRow = {
  key: string
  count: number
  percent: number
}

export type ErrorStatusRow = {
  code: string
  count: number
  percent: number
}

function buildCountBreakdown(
  counts: Record<string, number> | undefined,
  totalHint: number | undefined,
  compareKeys: (a: string, b: string) => number,
): CountBreakdownRow[] {
  const rows = Object.entries(counts ?? {})
    .map(([key, count]) => ({
      key,
      count: Number(count) || 0,
    }))
    .filter((row) => row.count > 0)
    .sort((a, b) => b.count - a.count || compareKeys(a.key, b.key))
  const total =
    totalHint && totalHint > 0
      ? totalHint
      : rows.reduce((sum, row) => sum + row.count, 0)
  return rows.map((row) => ({
    ...row,
    percent: total > 0 ? (row.count / total) * 100 : 0,
  }))
}

export function buildErrorStatusBreakdown(
  counts: Record<string, number> | undefined,
  errorTotal?: number,
): ErrorStatusRow[] {
  return buildCountBreakdown(counts, errorTotal, (a, b) => Number(a) - Number(b)).map(
    (row) => ({
      code: row.key,
      count: row.count,
      percent: row.percent,
    }),
  )
}

export function buildModelCountBreakdown(
  counts: Record<string, number> | undefined,
  successTotal?: number,
): CountBreakdownRow[] {
  return buildCountBreakdown(counts, successTotal, (a, b) => a.localeCompare(b))
}

export function formatErrorStatusPercent(percent: number): string {
  if (!Number.isFinite(percent) || percent <= 0) {
    return "0%"
  }
  if (percent >= 99.95 || percent < 0.05) {
    return `${percent.toFixed(0)}%`
  }
  return `${percent.toFixed(1)}%`
}
