import assert from 'node:assert/strict'
import test from 'node:test'

import {
  getPromptFilterScoreBand,
  normalizePromptFilterScore,
} from './promptFilterScore.ts'

test('score visualization is clamped without replacing the raw audit value', () => {
  const rawAuditScore = 240
  assert.equal(normalizePromptFilterScore(rawAuditScore), 100)
  assert.equal(rawAuditScore, 240)
  assert.equal(getPromptFilterScoreBand(rawAuditScore), 'high')
})

test('score visualization handles lower bounds and non-finite values', () => {
  assert.equal(normalizePromptFilterScore(-30), 0)
  assert.equal(normalizePromptFilterScore(Number.NaN), 0)
  assert.equal(getPromptFilterScoreBand(49), 'low')
  assert.equal(getPromptFilterScoreBand(50), 'medium')
  assert.equal(getPromptFilterScoreBand(90), 'high')
})
