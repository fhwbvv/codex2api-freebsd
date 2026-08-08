package proxy

import (
	"context"

	"github.com/codex2api/database"
	"github.com/codex2api/security"
)

// ExecuteInternalResponseForAPIKey performs an administrative model request
// through the account pool while attributing routing and usage to a selected
// gateway API Key. The raw credential is never copied into the child request.
func (h *Handler) ExecuteInternalResponseForAPIKey(ctx context.Context, body []byte, row *database.APIKeyRow, reason string) (int, []byte) {
	if row == nil {
		return h.ExecuteInternalResponse(ctx, body)
	}
	return h.executeInternalResponseWithAttribution(ctx, body, &internalResponseAttribution{
		APIKeyID:     row.ID,
		APIKeyName:   row.Name,
		APIKeyMasked: security.MaskAPIKey(row.Key),
		APIKeyRow:    row,
		Reason:       reason,
	})
}
