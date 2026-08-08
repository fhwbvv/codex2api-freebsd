import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('../pages/PromptFilter.tsx', import.meta.url), 'utf8')
const types = readFileSync(new URL('../types.ts', import.meta.url), 'utf8')

test('CY incidents and local logs use independent pagination state', () => {
  assert.match(source, /usePersistedPageSize\('prompt_policy_incidents'/)
  assert.match(source, /page: incidentPage,\s+pageSize: incidentPageSize/)
  assert.match(source, /page: logPage,/)
  assert.match(source, /page=\{incidentPage\}[\s\S]*totalItems=\{incidentTotal\}/)
  assert.match(source, /page=\{logPage\}[\s\S]*totalItems=\{total\}/)
  assert.doesNotMatch(source, /Math\.max\(total, incidentTotal\)/)
})

test('CY routing snapshots and NewAPI audit passthrough are visible', () => {
  for (const field of [
    'account_name',
    'account_group_names',
    'api_key_allowed_group_names',
	'routing_snapshot_state',
    'local_comparison',
    'prompt_available',
    'newapi_policy_status',
    'newapi_request_id',
    'newapi_decision_id',
  ]) {
    assert.match(types, new RegExp(`${field}[?:]`))
  }
  assert.match(source, /cyberComparisonStatus/)
	assert.match(source, /cyberRoutingState/)
  assert.match(source, /newapiPolicyStatus/)
})

test('Prompt log tables show the complete API key name inside the fixed-width column', () => {
  assert.match(source, /const apiKeyLabel = log\.api_key_name \|\| log\.api_key_masked \|\| '-'/)
  assert.match(source, /className="whitespace-normal break-all font-mono text-\[11px\] leading-4 text-foreground" title=\{apiKeyLabel\}/)
  assert.doesNotMatch(source, /max-w-\[110px\] truncate[^\n]*api_key_name/)
})
