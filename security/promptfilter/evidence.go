package promptfilter

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// StableEvidenceFingerprint returns a namespaced SHA-256 fingerprint without
// retaining the original evidence. Callers must provide a canonical value when
// exact byte distinctions are meaningful (for example, a case-sensitive RE2
// pattern).
func StableEvidenceFingerprint(namespace, value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(namespace) + "\x00" + strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
}

// PromptEvidenceFingerprint canonicalizes user-authored text before hashing so
// the same semantic sample observed through different protocols or platforms
// converges on one global evidence candidate. The canonical text is never
// returned or persisted by this helper.
func PromptEvidenceFingerprint(text string) string {
	canonical := norm.NFKC.String(text)
	canonical = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, canonical)
	canonical = strings.Join(strings.Fields(canonical), " ")
	if canonical == "" {
		return ""
	}
	return StableEvidenceFingerprint("prompt", canonical)
}
