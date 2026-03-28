import i18n from '../i18n'

export interface RelativeTimeOptions {
  variant?: 'long' | 'compact'
  includeSeconds?: boolean
  fallback?: string
}

export function formatRelativeTime(dateStr?: string | null, options: RelativeTimeOptions = {}): string {
  const {
    variant = 'long',
    includeSeconds = false,
    fallback = '-',
  } = options

  if (!dateStr) {
    return fallback
  }

  const timestamp = new Date(dateStr).getTime()
  if (Number.isNaN(timestamp)) {
    return fallback
  }

  const diff = Math.max(0, Date.now() - timestamp)
  const seconds = Math.floor(diff / 1000)

  if (includeSeconds && seconds < 60) {
    return variant === 'compact'
      ? i18n.t('common.secondsAgoCompact', { count: seconds })
      : i18n.t('common.secondsAgoLong', { count: seconds })
  }

  const minutes = Math.floor(seconds / 60)
  if (minutes < 1) {
    return i18n.t('common.justNow')
  }

  if (minutes < 60) {
    return variant === 'compact'
      ? i18n.t('common.minutesAgoCompact', { count: minutes })
      : i18n.t('common.minutesAgoLong', { count: minutes })
  }

  const hours = Math.floor(minutes / 60)
  if (hours < 24) {
    return variant === 'compact'
      ? i18n.t('common.hoursAgoCompact', { count: hours })
      : i18n.t('common.hoursAgoLong', { count: hours })
  }

  const days = Math.floor(hours / 24)
  return variant === 'compact'
    ? i18n.t('common.daysAgoCompact', { count: days })
    : i18n.t('common.daysAgoLong', { count: days })
}

/**
 * Format a date string as Beijing time (UTC+8)
 * Output format: YYYY-MM-DD HH:mm:ss
 */
export function formatBeijingTime(dateStr?: string | null, fallback = '-'): string {
  if (!dateStr) return fallback

  const timestamp = new Date(dateStr).getTime()
  if (Number.isNaN(timestamp)) return fallback

  // Convert to Beijing time (UTC+8)
  const date = new Date(timestamp)
  const beijingOffset = 8 * 60 // UTC+8 in minutes
  const localOffset = date.getTimezoneOffset() // local offset in minutes (negative for east)
  const beijingTime = new Date(timestamp + (beijingOffset + localOffset) * 60 * 1000)

  const year = beijingTime.getFullYear()
  const month = String(beijingTime.getMonth() + 1).padStart(2, '0')
  const day = String(beijingTime.getDate()).padStart(2, '0')
  const hours = String(beijingTime.getHours()).padStart(2, '0')
  const minutes = String(beijingTime.getMinutes()).padStart(2, '0')
  const seconds = String(beijingTime.getSeconds()).padStart(2, '0')

  return `${year}-${month}-${day} ${hours}:${minutes}:${seconds}`
}
