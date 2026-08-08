package auth

import (
	"strings"
	"time"

	"github.com/codex2api/database"
)

func (s *Store) ReplacePromptRiskTrustPolicies(items []*database.PromptRiskTrustPolicy) {
	if s == nil {
		return
	}
	next := make(map[string]database.PromptRiskTrustPolicy, len(items))
	for _, item := range items {
		if item == nil || strings.TrimSpace(item.SubjectKey) == "" {
			continue
		}
		next[item.SubjectKey] = *item
	}
	s.promptRiskTrustMu.Lock()
	s.promptRiskTrustPolicies = next
	s.promptRiskTrustMu.Unlock()
}

func (s *Store) GetPromptRiskTrustPolicy(subjectKey string, now time.Time) (database.PromptRiskTrustPolicy, bool) {
	if s == nil || strings.TrimSpace(subjectKey) == "" {
		return database.PromptRiskTrustPolicy{}, false
	}
	s.promptRiskTrustMu.RLock()
	item, ok := s.promptRiskTrustPolicies[subjectKey]
	s.promptRiskTrustMu.RUnlock()
	if !ok || item.Status != database.PromptRiskTrustStatusActive || !item.ValidUntil.After(now.UTC()) || item.LastRiskScore >= item.RiskThreshold {
		return database.PromptRiskTrustPolicy{}, false
	}
	return item, true
}

func (s *Store) RemovePromptRiskTrustPolicy(subjectKey string) {
	if s == nil || strings.TrimSpace(subjectKey) == "" {
		return
	}
	s.promptRiskTrustMu.Lock()
	delete(s.promptRiskTrustPolicies, subjectKey)
	s.promptRiskTrustMu.Unlock()
}

func (s *Store) RecordPromptRiskTrustModelReview(subjectKey string, reviewedAt time.Time) {
	if s == nil || strings.TrimSpace(subjectKey) == "" {
		return
	}
	s.promptRiskTrustMu.Lock()
	item, ok := s.promptRiskTrustPolicies[subjectKey]
	if ok {
		reviewedAt = reviewedAt.UTC()
		item.LastModelReviewAt = &reviewedAt
		item.ModelReviewCount++
		s.promptRiskTrustPolicies[subjectKey] = item
	}
	s.promptRiskTrustMu.Unlock()
}
