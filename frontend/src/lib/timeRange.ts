export type TimeRangeKey = '1h' | '6h' | '24h' | '7d' | '30d'

export const TIME_RANGE_OPTIONS: TimeRangeKey[] = ['1h', '6h', '24h', '7d', '30d']

export function getBucketConfig(range: TimeRangeKey): { bucketMinutes: number; bucketCount: number } {
  switch (range) {
    case '1h':
      return { bucketMinutes: 5, bucketCount: 12 }
    case '6h':
      return { bucketMinutes: 15, bucketCount: 24 }
    case '24h':
      return { bucketMinutes: 30, bucketCount: 48 }
    case '7d':
      return { bucketMinutes: 360, bucketCount: 28 }
    case '30d':
      return { bucketMinutes: 1440, bucketCount: 30 }
    default:
      return { bucketMinutes: 5, bucketCount: 12 }
  }
}

function toLocalRFC3339(date: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  const offset = date.getTimezoneOffset()
  const sign = offset <= 0 ? '+' : '-'
  const absOffset = Math.abs(offset)
  const tzH = pad(Math.floor(absOffset / 60))
  const tzM = pad(absOffset % 60)
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}${sign}${tzH}:${tzM}`
}

export function getTimeRangeISO(range: TimeRangeKey): { start: string; end: string } {
  const now = new Date()
  const end = toLocalRFC3339(now)
  let offsetMs: number
  switch (range) {
    case '1h':
      offsetMs = 60 * 60 * 1000
      break
    case '6h':
      offsetMs = 6 * 60 * 60 * 1000
      break
    case '24h':
      offsetMs = 24 * 60 * 60 * 1000
      break
    case '7d':
      offsetMs = 7 * 24 * 60 * 60 * 1000
      break
    case '30d':
      offsetMs = 30 * 24 * 60 * 60 * 1000
      break
    default:
      offsetMs = 60 * 60 * 1000
  }
  const start = toLocalRFC3339(new Date(now.getTime() - offsetMs))
  return { start, end }
}
