package auth

import (
	"context"

	"github.com/codex2api/database"
)

// GetPromptFilterNewAPIBinding returns an immutable value copy of the binding
// for a Codex2API API key. It is safe on the request hot path.
func (s *Store) GetPromptFilterNewAPIBinding(apiKeyID int64) (database.PromptFilterNewAPIBinding, bool) {
	if s == nil || apiKeyID <= 0 {
		return database.PromptFilterNewAPIBinding{}, false
	}
	s.promptFilterNewAPIBindingsMu.RLock()
	binding, ok := s.promptFilterNewAPIBindings[apiKeyID]
	s.promptFilterNewAPIBindingsMu.RUnlock()
	if !ok {
		return database.PromptFilterNewAPIBinding{}, false
	}
	return clonePromptFilterNewAPIBinding(binding), true
}

// HasPromptFilterNewAPIBindings reports whether the runtime is operating in
// per-key platform-binding mode. Once any binding exists, unbound API keys
// must not fall back to the legacy process-wide NewAPI secret.
func (s *Store) HasPromptFilterNewAPIBindings() bool {
	if s == nil {
		return false
	}
	s.promptFilterNewAPIBindingsMu.RLock()
	hasBindings := len(s.promptFilterNewAPIBindings) > 0
	s.promptFilterNewAPIBindingsMu.RUnlock()
	return hasBindings
}

// ReplacePromptFilterNewAPIBindings atomically replaces the complete runtime
// snapshot after a DB read or an admin mutation.
func (s *Store) ReplacePromptFilterNewAPIBindings(bindings []*database.PromptFilterNewAPIBinding) {
	if s == nil {
		return
	}
	next := make(map[int64]database.PromptFilterNewAPIBinding, len(bindings))
	for _, binding := range bindings {
		if binding == nil || binding.APIKeyID <= 0 {
			continue
		}
		next[binding.APIKeyID] = clonePromptFilterNewAPIBinding(*binding)
	}
	s.promptFilterNewAPIBindingsMu.Lock()
	s.promptFilterNewAPIBindings = next
	s.promptFilterNewAPIBindingsMu.Unlock()
}

// UpsertPromptFilterNewAPIBinding publishes one binding after its database
// mutation commits. Admin writes use this non-failing in-memory step instead
// of making a second full-table database read part of the request outcome.
func (s *Store) UpsertPromptFilterNewAPIBinding(binding database.PromptFilterNewAPIBinding) {
	if s == nil || binding.APIKeyID <= 0 {
		return
	}
	binding = clonePromptFilterNewAPIBinding(binding)
	s.promptFilterNewAPIBindingsMu.Lock()
	if s.promptFilterNewAPIBindings == nil {
		s.promptFilterNewAPIBindings = make(map[int64]database.PromptFilterNewAPIBinding)
	}
	s.promptFilterNewAPIBindings[binding.APIKeyID] = binding
	s.promptFilterNewAPIBindingsMu.Unlock()
}

// RemovePromptFilterNewAPIBinding removes one committed binding from the
// runtime snapshot without a fallible post-commit database round trip.
func (s *Store) RemovePromptFilterNewAPIBinding(apiKeyID int64) {
	if s == nil || apiKeyID <= 0 {
		return
	}
	s.promptFilterNewAPIBindingsMu.Lock()
	delete(s.promptFilterNewAPIBindings, apiKeyID)
	s.promptFilterNewAPIBindingsMu.Unlock()
}

func (s *Store) LoadPromptFilterNewAPIBindings(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	bindings, err := s.db.ListPromptFilterNewAPIBindings(ctx)
	if err != nil {
		return err
	}
	s.ReplacePromptFilterNewAPIBindings(bindings)
	return nil
}

func clonePromptFilterNewAPIBinding(binding database.PromptFilterNewAPIBinding) database.PromptFilterNewAPIBinding {
	if binding.PreviousSecretExpiresAt != nil {
		expiresAt := *binding.PreviousSecretExpiresAt
		binding.PreviousSecretExpiresAt = &expiresAt
	}
	return binding
}
