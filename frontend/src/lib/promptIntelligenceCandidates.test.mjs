import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const pageSource = readFileSync(new URL('../pages/PromptFilter.tsx', import.meta.url), 'utf8')
const apiSource = readFileSync(new URL('../api.ts', import.meta.url), 'utf8')
const handlerSource = readFileSync(new URL('../../../admin/handler.go', import.meta.url), 'utf8')

test('prompt intelligence uses the persistent candidate lifecycle API', () => {
  for (const fragment of [
    '/prompt-filter/intelligence/candidates?',
    '/prompt-filter/intelligence/candidates/${id}/evidence',
    '/prompt-filter/intelligence/candidates/${id}/draft',
    '/prompt-filter/intelligence/candidates/${id}/publish',
    '/prompt-filter/intelligence/candidates/${id}/dismiss',
  ]) {
    assert.equal(apiSource.includes(fragment), true, `missing candidate API: ${fragment}`)
  }
  assert.equal(apiSource.includes('addPromptIntelligenceRule'), false)
  assert.equal(apiSource.includes("'/prompt-filter/intelligence/rules'"), false)
  assert.equal(handlerSource.includes('POST("/prompt-filter/intelligence/rules"'), false)
})

test('pending evidence cannot be published and every lifecycle action stays in the review queue', () => {
  assert.equal(pageSource.includes("candidate.lifecycle_status === 'pending' && candidate.kind === 'pattern'"), true)
  assert.equal(pageSource.includes("candidate.lifecycle_status === 'pending' ?"), true)
  assert.equal(pageSource.includes('getPromptIntelligenceCandidateEvidence(candidate.id)'), true)
  assert.equal(pageSource.includes('publishPromptIntelligenceCandidate(candidate.id)'), true)
  assert.equal(pageSource.includes('dismissPromptIntelligenceCandidate(dismissTarget.id)'), true)
  assert.equal(pageSource.includes('result.staged'), true)
  assert.equal(pageSource.includes("candidate.kind === 'evidence' && (candidate.lifecycle_status === 'pending' || candidate.ai_analyzed)"), true)
  assert.equal(pageSource.includes('createPromptIntelligenceCandidateDraft(draftTarget.id'), true)
  assert.equal(pageSource.includes('saveDraft'), true)
})

test('legacy automatic rule admission is not exposed as a setting', () => {
  assert.equal(pageSource.includes("t('promptFilter.intelligence.autoAdd')"), false)
  assert.equal(pageSource.includes('config.intelligence.auto_add'), false)
  assert.equal(pageSource.includes("setBool('intelligence', 'auto_add'"), false)
  assert.equal(pageSource.includes("{ path: ['intelligence', 'auto_add'], remove: true }"), true, 'legacy data should be cleaned when advanced settings are next saved')
})

test('CY evidence AI analysis keeps rule publishing and identity updates behind separate controls', () => {
  for (const fragment of [
    '/prompt-filter/intelligence/ai-providers',
    '/prompt-filter/intelligence/candidates/${id}/analyze',
    '/prompt-filter/intelligence/candidates/${candidateId}/identity-updates/${evidenceId}/apply',
    '/prompt-filter/intelligence/candidates/${candidateId}/identity-updates/${evidenceId}/rollback',
  ]) {
    assert.equal(apiSource.includes(fragment), true, `missing controlled AI API: ${fragment}`)
  }
  assert.equal(pageSource.includes("useState<PromptIdentityUpdateMode>('suggest')"), true)
  assert.equal(pageSource.includes("persisted?.identity_update.mode === 'guarded_auto' ? 'guarded_auto' : 'suggest'"), true)
  assert.equal(pageSource.includes("value: 'guarded_auto'"), true)
  assert.equal(pageSource.includes('applyPromptIntelligenceIdentityUpdate'), true)
  assert.equal(pageSource.includes('rollbackPromptIntelligenceIdentityUpdate'), true)
  assert.equal(pageSource.includes('publishPromptIntelligenceCandidate(candidate.id)'), true, 'rule publication must remain a distinct action')
})

test('persisted AI learning results are restored and visibly marked without rerunning analysis', () => {
  for (const fragment of [
    'const persisted = candidate.latest_ai_analysis ?? null',
    'setAIResult(persisted)',
    'candidate.ai_analyzed',
    'candidate.ai_analysis_count',
    "t('promptFilter.intelligence.aiLearned')",
    "t('promptFilter.intelligence.aiViewResult')",
    "t('promptFilter.intelligence.aiRunAgain')",
  ]) {
    assert.equal(pageSource.includes(fragment), true, `missing persisted AI result UI: ${fragment}`)
  }
})

test('applied identity attribution is closed and remains reviewable', () => {
  for (const fragment of [
    "candidate.lifecycle_status === 'published' ? 'promptFilter.intelligence.attributedEvidence'",
    "candidateLifecycleLabel(candidate)",
    "candidate.kind === 'evidence' && (candidate.lifecycle_status === 'pending' || candidate.ai_analyzed)",
    "aiResult.identity_update.rolled_back",
    "Boolean(aiResult.identity_update.block_reason)",
    "aiTarget.lifecycle_status !== 'pending'",
  ]) {
    assert.equal(pageSource.includes(fragment), true, `missing closed-loop identity UI: ${fragment}`)
  }
})
