import assert from 'node:assert/strict'
import test from 'node:test'

import {
  emptyModelMappingEntries,
  parseModelMappingEntries,
  serializeModelMappingEntries,
} from './modelMapping.ts'

test('model mapping entries round-trip exact and wildcard aliases', () => {
  const parsed = parseModelMappingEntries(
    '{"gpt-5.5":"grok-4.5","gpt-5.*":"grok-4.6"}',
  )
  assert.equal(parsed.ok, true)
  assert.deepEqual(parsed.entries, [
    { from: 'gpt-5.5', to: 'grok-4.5' },
    { from: 'gpt-5.*', to: 'grok-4.6' },
  ])
  assert.deepEqual(serializeModelMappingEntries(parsed.entries), {
    ok: true,
    value: '{"gpt-5.5":"grok-4.5","gpt-5.*":"grok-4.6"}',
  })
})

test('model mapping parser rejects invalid JSON and non-string targets', () => {
  assert.deepEqual(parseModelMappingEntries('{bad json'), { ok: false })
  assert.deepEqual(parseModelMappingEntries('{"gpt-5.5":42}'), { ok: false })
})

test('model mapping serializer ignores blank rows and rejects partial or duplicate sources', () => {
  assert.deepEqual(serializeModelMappingEntries(emptyModelMappingEntries()), {
    ok: true,
    value: '',
  })
  assert.deepEqual(
    serializeModelMappingEntries([{ from: 'gpt-5.5', to: '' }]),
    { ok: false },
  )
  assert.deepEqual(
    serializeModelMappingEntries([
      { from: 'gpt-5.5', to: 'grok-4.5' },
      { from: 'GPT-5.5', to: 'grok-4.6' },
    ]),
    { ok: false },
  )
})
