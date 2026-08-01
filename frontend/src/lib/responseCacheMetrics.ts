export const MIB = 1024 * 1024

export interface ResponseCacheBudgetMiB {
  totalMiB: number
  entryMiB: number
  reconstructMiB: number
}

export type ResponseCacheBudgetValidationError =
  | 'total_integer'
  | 'total_range'
  | 'entry_integer'
  | 'entry_range'
  | 'entry_exceeds_total'
  | 'reconstruct_integer'
  | 'reconstruct_range'

export interface ResponseCacheBudgetPatch {
  response_cache_local_max_bytes: number
  response_cache_local_max_entry_bytes: number
  response_cache_reconstruct_max_bytes: number
}

export function mibToBytes(mib: number): number {
  return mib * MIB
}

export function bytesToMiB(bytes: number): number {
  return bytes / MIB
}

export function validateResponseCacheBudget(
  budget: ResponseCacheBudgetMiB,
): ResponseCacheBudgetValidationError | null {
  if (!Number.isFinite(budget.totalMiB) || !Number.isInteger(budget.totalMiB)) {
    return 'total_integer'
  }
  if (budget.totalMiB < 8 || budget.totalMiB > 4096) {
    return 'total_range'
  }
  if (!Number.isFinite(budget.entryMiB) || !Number.isInteger(budget.entryMiB)) {
    return 'entry_integer'
  }
  if (budget.entryMiB < 1 || budget.entryMiB > 256) {
    return 'entry_range'
  }
  if (budget.entryMiB > budget.totalMiB) {
    return 'entry_exceeds_total'
  }
  if (!Number.isFinite(budget.reconstructMiB) || !Number.isInteger(budget.reconstructMiB)) {
    return 'reconstruct_integer'
  }
  if (budget.reconstructMiB < 8 || budget.reconstructMiB > 512) {
    return 'reconstruct_range'
  }
  return null
}

export function buildResponseCacheBudgetPatch(
  budget: ResponseCacheBudgetMiB,
): ResponseCacheBudgetPatch {
  const validationError = validateResponseCacheBudget(budget)
  if (validationError) {
    throw new RangeError(validationError)
  }
  return {
    response_cache_local_max_bytes: mibToBytes(budget.totalMiB),
    response_cache_local_max_entry_bytes: mibToBytes(budget.entryMiB),
    response_cache_reconstruct_max_bytes: mibToBytes(budget.reconstructMiB),
  }
}

export function mergeResponseCacheGeneration(
  currentGeneration: number,
  returnedGeneration: unknown,
): number {
  const current = Number.isFinite(currentGeneration)
    && Number.isInteger(currentGeneration)
    && currentGeneration >= 0
    ? currentGeneration
    : 0
  if (
    typeof returnedGeneration !== 'number'
    || !Number.isFinite(returnedGeneration)
    || !Number.isInteger(returnedGeneration)
    || returnedGeneration < 0
  ) {
    return current
  }
  return Math.max(current, returnedGeneration)
}

export function cacheUtilizationPercent(currentBytes: number, maxBytes: number): number {
  if (!Number.isFinite(currentBytes) || !Number.isFinite(maxBytes) || maxBytes <= 0) {
    return 0
  }
  return Math.min(100, Math.max(0, (currentBytes / maxBytes) * 100))
}

export function formatIECBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) {
    return '0 B'
  }
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let value = bytes
  let unitIndex = 0
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024
    unitIndex += 1
  }
  const formatted = Number.isInteger(value) ? value.toFixed(0) : value.toFixed(1)
  return `${formatted} ${units[unitIndex]}`
}
