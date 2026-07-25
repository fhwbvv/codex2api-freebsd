import assert from 'node:assert/strict'
import test from 'node:test'

import {
  createDefaultPromptGuardRollout,
  parsePromptGuardRollout,
} from './promptGuardRollout.ts'

test('stable rollout is safely disabled by default', () => {
  assert.deepEqual(createDefaultPromptGuardRollout(), {
    enabled: false,
    percent: 0,
    fallback_mode: 'warn',
    newapi_user_allowlist: [],
    api_key_allowlist: [],
    protocols: [],
    providers: [],
  })
  assert.deepEqual(parsePromptGuardRollout(undefined), createDefaultPromptGuardRollout())
})

test('stable rollout normalizes unsafe values while preserving future filters', () => {
  assert.deepEqual(parsePromptGuardRollout({
    enabled: true,
    percent: 145.6,
    fallback_mode: 'enforce',
    newapi_user_allowlist: [' 42 ', '42', '', 7, 7.5],
    api_key_allowlist: [' 101 ', 102, 102, Number.MAX_SAFE_INTEGER + 1],
    protocols: ['responses', 'future_protocol'],
    providers: ['openai', 'future_provider'],
  }), {
    enabled: true,
    percent: 100,
    fallback_mode: 'warn',
    newapi_user_allowlist: ['42', '7'],
    api_key_allowlist: ['101', '102'],
    protocols: ['responses', 'future_protocol'],
    providers: ['openai', 'future_provider'],
  })
})
