package proxy

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/codex2api/security"
)

const (
	portableCompactionEnvelopePrefix = "sub2api-emulated-compaction-v1:"
	portableCompactionSummaryOpen    = "<summary>"
	portableCompactionSummaryClose   = "</summary>"
	responsesCompactionSummaryPrefix = "[Conversation summary from earlier turns]\n"
)

func isResponsesCompactionItemType(itemType string) bool {
	switch strings.ToLower(strings.TrimSpace(itemType)) {
	case "compaction", "context_compaction", "compaction_summary":
		return true
	default:
		return false
	}
}

func decodePortableCompactionSummary(encryptedContent string) (string, bool) {
	encoded, ok := strings.CutPrefix(encryptedContent, portableCompactionEnvelopePrefix)
	if !ok || encoded == "" || len(encoded) > base64.StdEncoding.EncodedLen(security.MaxRequestBodySize) {
		return "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(decoded) == 0 || len(decoded) > security.MaxRequestBodySize || !utf8.Valid(decoded) {
		return "", false
	}

	wrapped := strings.TrimSpace(string(decoded))
	if !strings.HasPrefix(wrapped, portableCompactionSummaryOpen) || !strings.HasSuffix(wrapped, portableCompactionSummaryClose) {
		return "", false
	}
	summary := strings.TrimSpace(wrapped[len(portableCompactionSummaryOpen) : len(wrapped)-len(portableCompactionSummaryClose)])
	if summary == "" {
		return "", false
	}
	return summary, true
}

func responsesCompactionDeveloperMessage(summary string) map[string]any {
	return map[string]any{
		"type": "message",
		"role": "developer",
		"content": []any{
			map[string]any{
				"type": "input_text",
				"text": responsesCompactionSummaryPrefix + summary,
			},
		},
	}
}

func normalizePortableResponsesCompactionItems(body map[string]any) bool {
	input := body["input"]
	if inputItem, ok := input.(map[string]any); ok {
		if !isResponsesCompactionItemType(firstNonEmptyAnyString(inputItem["type"])) {
			return false
		}
		encryptedContent, _ := inputItem["encrypted_content"].(string)
		summary, portable := decodePortableCompactionSummary(encryptedContent)
		if !portable {
			return false
		}
		body["input"] = responsesCompactionDeveloperMessage(summary)
		return true
	}

	inputItems, ok := input.([]any)
	if !ok {
		return false
	}

	modified := false
	out := make([]any, 0, len(inputItems))
	for _, raw := range inputItems {
		item, ok := raw.(map[string]any)
		if !ok || !isResponsesCompactionItemType(firstNonEmptyAnyString(item["type"])) {
			out = append(out, raw)
			continue
		}
		encryptedContent, _ := item["encrypted_content"].(string)
		summary, portable := decodePortableCompactionSummary(encryptedContent)
		if !portable {
			out = append(out, raw)
			continue
		}
		out = append(out, responsesCompactionDeveloperMessage(summary))
		modified = true
	}

	if modified {
		body["input"] = out
	}
	return modified
}

// normalizePortableResponsesCompactionHistory rewrites known reversible
// emulated compaction envelopes before account affinity is resolved. Opaque
// encrypted compaction state remains untouched and therefore source-affine.
func normalizePortableResponsesCompactionHistory(rawBody []byte) ([]byte, bool) {
	if len(rawBody) == 0 || !bytes.Contains(rawBody, []byte(portableCompactionEnvelopePrefix)) {
		return rawBody, false
	}

	var body map[string]any
	if err := json.Unmarshal(rawBody, &body); err != nil || !normalizePortableResponsesCompactionItems(body) {
		return rawBody, false
	}
	normalized, err := json.Marshal(body)
	if err != nil {
		return rawBody, false
	}
	return normalized, true
}
