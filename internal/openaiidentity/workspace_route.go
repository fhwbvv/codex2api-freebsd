package openaiidentity

import (
	"fmt"
	"strings"
)

const ChatGPTAccountIDHeader = "Chatgpt-Account-Id"

// WorkspaceOverrideFromHeaders returns the workspace explicitly selected for
// upstream routing. Header names are matched case-insensitively because
// imported credentials may not have passed through net/http canonicalization.
func WorkspaceOverrideFromHeaders(headers map[string]string) string {
	override, err := WorkspaceOverrideFromHeadersChecked(headers)
	if err != nil {
		return ""
	}
	return override
}

// WorkspaceOverrideFromHeadersChecked returns the route override and rejects
// conflicting case variants of Chatgpt-Account-Id. Imported credential maps
// are not always normalized by net/http, so selecting the first map entry
// would otherwise make route identity depend on Go map iteration order.
func WorkspaceOverrideFromHeadersChecked(headers map[string]string) (string, error) {
	override := ""
	found := false
	for name, value := range headers {
		if strings.EqualFold(strings.TrimSpace(name), ChatGPTAccountIDHeader) {
			value = strings.TrimSpace(value)
			if found && value != override {
				return "", fmt.Errorf("conflicting %s header values", ChatGPTAccountIDHeader)
			}
			override = value
			found = true
		}
	}
	return override, nil
}

// EffectiveWorkspaceID is the workspace that requests and quota probes
// actually target. An explicit Chatgpt-Account-Id route takes precedence over
// the workspace embedded in the OAuth token.
func EffectiveWorkspaceID(tokenWorkspaceID string, headers map[string]string) string {
	if override := WorkspaceOverrideFromHeaders(headers); override != "" {
		return override
	}
	return NormalizeWorkspaceID(tokenWorkspaceID)
}
