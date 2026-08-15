import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('../pages/PromptFilter.tsx', import.meta.url), 'utf8')
const api = readFileSync(new URL('../api.ts', import.meta.url), 'utf8')
const types = readFileSync(new URL('../types.ts', import.meta.url), 'utf8')
const zh = JSON.parse(readFileSync(new URL('../locales/zh.json', import.meta.url), 'utf8'))

test('model review history has independent filtering and pagination from local audit rows', () => {
  assert.match(source, /usePersistedPageSize\('prompt_review_logs'/)
  assert.match(source, /page: reviewPage,[\s\S]*reviewed: true/)
  assert.match(source, /page: logPage,[\s\S]*reviewed: false/)
  assert.match(source, /const loadReviewLogs = useCallback/)
  assert.match(source, /const loadLocalLogs = useCallback/)
  assert.match(source, /reviewResult: reviewFilters\.reviewResult/)
  assert.match(source, /showReviewResult/)
  assert.match(source, /<PromptReviewLogsTable logs=\{reviewLogs\}/)
  assert.match(source, /page=\{reviewPage\}[\s\S]*totalItems=\{reviewTotal\}/)
  assert.match(api, /typeof params\.reviewed === 'boolean'/)
  assert.match(api, /search\.set\('review_result', params\.reviewResult\)/)
  assert.doesNotMatch(source, /const loadLogs = useCallback/)
  assert.equal(zh.promptFilter.sectionRefreshHint, '筛选、翻页和刷新仅更新当前区域。')
})

test('model review history exposes parsed request and response metadata without secrets', () => {
  for (const field of [
    'reviewed',
    'review_confidence',
    'review_threshold',
    'review_reason',
    'review_endpoint',
    'review_request_mode',
    'review_latency_ms',
  ]) {
    assert.match(types, new RegExp(`${field}:`))
  }
  assert.match(source, /log\.text_preview/)
  assert.match(source, /log\.review_confidence/)
  assert.match(source, /log\.review_reason/)
  assert.match(source, /moderation decision:\\s\+\(\\S\+\)/)
  assert.match(source, /moderationCategory \? `\$\{moderationCategory\} `/)
  assert.doesNotMatch(source, /review_api_key.*PromptReviewLogsTable/)
  assert.match(zh.promptFilter.reviewHistoryDesc, /不保存审核 Key、Authorization 或原始 Payload/)
})

test('each audit section clears independently and explains that risk profiles are retained', () => {
  assert.match(source, /clearLogSection\('incidents'\)/)
  assert.match(source, /clearLogSection\('review'\)/)
  assert.match(source, /clearLogSection\('local'\)/)
  assert.match(source, /clearPromptFilterLogs\(\{ reviewed: section === 'review' \}\)/)
  assert.match(source, /clearPromptPolicyIncidents\(\)/)
  assert.doesNotMatch(source, /clearLogs\(\)\.then\(refreshAll\)/)
  assert.match(api, /clearPromptFilterLogs: \(params: \{ reviewed\?: boolean \} = \{\}\)/)
  assert.match(api, /clearPromptPolicyIncidents: \(\)/)
  assert.match(zh.promptFilter.cyberIncidentsCleared, /风险画像已保留/)
  assert.match(zh.promptFilter.reviewLogsCleared, /风险画像已保留/)
  assert.match(zh.promptFilter.localLogsCleared, /风险画像已保留/)
})

test('final actions distinguish model-review blocks from local-rule and conversation-lock blocks', () => {
  assert.match(source, /function promptFilterDecisionSource/)
  assert.match(source, /review_flagged/)
  assert.match(source, /conversation_cyber_locked/)
  assert.match(source, /promptFilter\.decisionSource/)
  assert.equal(zh.promptFilter.decisionSource.model, '模型复核拦截')
  assert.equal(zh.promptFilter.decisionSource.local, '本地规则拦截')
})
