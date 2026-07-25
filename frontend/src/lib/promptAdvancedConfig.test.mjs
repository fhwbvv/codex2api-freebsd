import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

import {
  parseAdvancedConfigDocument,
  patchAdvancedConfigDocument,
  readAdvancedConfigPath,
} from '../types.ts'

test('advanced config parsing keeps invalid input distinct from defaults', () => {
  assert.deepEqual(parseAdvancedConfigDocument('{broken'), {
    ok: false,
    value: null,
    error: 'invalid_json',
  })
  assert.deepEqual(parseAdvancedConfigDocument('[]'), {
    ok: false,
    value: null,
    error: 'root_not_object',
  })

  const failedPatch = patchAdvancedConfigDocument('{broken', [
    { path: ['guard', 'mode'], value: 'enforce' },
  ])
  assert.equal(failedPatch.ok, false)
  assert.equal(failedPatch.serialized, '{broken')
})

test('field-level advanced config patches preserve unknown nested fields and enum values', () => {
  const raw = JSON.stringify({
    future_top_level: { enabled: true, revision: 7 },
    guard: {
      mode: 'future_mode',
      future_guard_option: { sample: 0.25 },
      rollout: {
        enabled: true,
        percent: 17,
        future_rollout_policy: 'keep-me',
      },
      provider_profiles: {
        openai: 'future_profile',
        future_provider: 'strict-plus',
      },
      layers: {
        current_user: { mode: 'enforce', future_layer_option: 'keep-me' },
        future_layer: { mode: 'observe-plus' },
      },
      performance: {
        shadow_workers: 2,
        future_queue_policy: 'adaptive',
      },
    },
    sidecar: { mode: 'future_sidecar_mode', future_sidecar_option: 9 },
  })

  const result = patchAdvancedConfigDocument(raw, [
    { path: ['guard', 'layers', 'current_user', 'mode'], value: 'warn' },
  ])
  assert.equal(result.ok, true)
  const saved = JSON.parse(result.serialized)
  assert.deepEqual(saved.future_top_level, { enabled: true, revision: 7 })
  assert.equal(saved.guard.mode, 'future_mode')
  assert.deepEqual(saved.guard.future_guard_option, { sample: 0.25 })
  assert.equal(saved.guard.provider_profiles.openai, 'future_profile')
  assert.equal(saved.guard.provider_profiles.future_provider, 'strict-plus')
  assert.equal(saved.guard.layers.current_user.future_layer_option, 'keep-me')
  assert.equal(saved.guard.layers.current_user.mode, 'warn')
  assert.equal(saved.guard.layers.future_layer.mode, 'observe-plus')
  assert.equal(saved.guard.rollout.enabled, true)
  assert.equal(saved.guard.rollout.percent, 17)
  assert.equal(saved.guard.rollout.future_rollout_policy, 'keep-me')
  assert.equal(saved.guard.performance.shadow_workers, 2)
  assert.equal(saved.guard.performance.future_queue_policy, 'adaptive')
  assert.equal(saved.sidecar.mode, 'future_sidecar_mode')
  assert.equal(saved.sidecar.future_sidecar_option, 9)
})

test('removing one known override does not rebuild its parent object', () => {
  const raw = JSON.stringify({
    guard: {
      provider_profiles: {
        openai: 'balanced',
        future_provider: 'future_profile',
      },
    },
  })
  const result = patchAdvancedConfigDocument(raw, [{
    path: ['guard', 'provider_profiles', 'openai'],
    remove: true,
  }])
  assert.equal(result.ok, true)
  assert.equal(readAdvancedConfigPath(result.value, ['guard', 'provider_profiles', 'openai']), undefined)
  assert.equal(readAdvancedConfigPath(result.value, ['guard', 'provider_profiles', 'future_provider']), 'future_profile')
})

test('Chinese locale labels do not expose internal policy enum values', () => {
  const locale = JSON.parse(readFileSync(new URL('../locales/zh.json', import.meta.url), 'utf8'))
  assert.equal('sync' in locale.promptFilter.guard.performance.overflowModes, false)
  const labels = [
    ...['off', 'shadow', 'warn', 'enforce'].map((mode) => locale.promptFilter.guard.modes[mode].label),
    ...['balanced', 'strict'].map((profile) => locale.promptFilter.guard.profiles[profile].label),
    ...['shadow', 'warn', 'enforce'].map((mode) => locale.promptFilter.extensions.sidecar.modes[mode]),
  ]
  const internalValues = new Set(['off', 'shadow', 'warn', 'enforce', 'balanced', 'strict'])
  for (const label of labels) {
    assert.equal(internalValues.has(String(label).toLowerCase()), false, `internal enum leaked as label: ${label}`)
  }
})

test('hidden release and runtime settings survive visible editor patches', () => {
  const raw = JSON.stringify({
    guard: {
      rollout: {
        enabled: true,
        percent: 25,
        fallback_mode: 'shadow',
      },
      performance: {
        max_segments: 64,
        max_current_user_bytes: 131072,
        max_auxiliary_bytes: 32768,
        scan_chunk_bytes: 8192,
        scan_overlap_bytes: 512,
        future_budget_strategy: 'adaptive',
      },
    },
    sidecar: {
      enabled: false,
      scan_clean_enabled: true,
      sample_percent: 9,
      cache_ttl_seconds: 4321,
    },
    attachment: {
      enabled: false,
      max_bytes: 524288,
      cache_ttl_seconds: 987,
    },
    output: {
      enabled: false,
      strict_only: true,
      buffer_bytes: 8192,
      overlap_bytes: 1024,
    },
    newapi: {
      enabled: false,
      max_clock_skew_seconds: 240,
    },
  })
  const result = patchAdvancedConfigDocument(raw, [
    { path: ['guard', 'default_profile'], value: 'strict' },
    { path: ['sidecar', 'enabled'], value: true },
    { path: ['attachment', 'enabled'], value: true },
    { path: ['output', 'strict_only'], value: false },
    { path: ['newapi', 'enabled'], value: true },
  ])
  assert.equal(result.ok, true)
  const saved = JSON.parse(result.serialized)
  assert.equal(saved.guard.default_profile, 'strict')
  assert.equal(saved.guard.rollout.enabled, true)
  assert.equal(saved.guard.rollout.percent, 25)
  assert.equal(saved.guard.performance.max_segments, 64)
  assert.equal(saved.guard.performance.max_current_user_bytes, 131072)
  assert.equal(saved.guard.performance.future_budget_strategy, 'adaptive')
  assert.equal(saved.sidecar.enabled, true)
  assert.equal(saved.sidecar.scan_clean_enabled, true)
  assert.equal(saved.sidecar.sample_percent, 9)
  assert.equal(saved.sidecar.cache_ttl_seconds, 4321)
  assert.equal(saved.attachment.enabled, true)
  assert.equal(saved.attachment.max_bytes, 524288)
  assert.equal(saved.attachment.cache_ttl_seconds, 987)
  assert.equal(saved.output.strict_only, false)
  assert.equal(saved.output.buffer_bytes, 8192)
  assert.equal(saved.output.overlap_bytes, 1024)
  assert.equal(saved.newapi.enabled, true)
  assert.equal(saved.newapi.max_clock_skew_seconds, 240)
})

test('Prompt Filter editor does not render rollout or runtime tuning controls', () => {
  const source = readFileSync(new URL('../pages/PromptFilter.tsx', import.meta.url), 'utf8')
  const forbiddenFragments = [
    'promptFilter.guard.rollout.',
    'promptFilter.guard.performance.',
    'scan_clean_enabled',
    'sample_percent',
    'async_shadow_auxiliary_enabled',
    'exact_segment_cache_enabled',
    'shadow_queue_size',
    'shadow_workers',
    'buffer_bytes',
    'overlap_bytes',
    'max_clock_skew_seconds',
  ]
  for (const fragment of forbiddenFragments) {
    assert.equal(source.includes(fragment), false, `internal control leaked into editor source: ${fragment}`)
  }
})
