export type PromptFilterScoreBand = 'low' | 'medium' | 'high'

export function normalizePromptFilterScore(score: number): number {
  if (!Number.isFinite(score)) return 0
  return Math.min(100, Math.max(0, score))
}

export function getPromptFilterScoreBand(score: number): PromptFilterScoreBand {
  const normalizedScore = normalizePromptFilterScore(score)
  if (normalizedScore >= 90) return 'high'
  if (normalizedScore >= 50) return 'medium'
  return 'low'
}
