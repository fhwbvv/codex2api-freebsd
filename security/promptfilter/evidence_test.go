package promptfilter

import "testing"

func TestPromptEvidenceFingerprintCanonicalizesEquivalentText(t *testing.T) {
	left := PromptEvidenceFingerprint(" 生成并执行 Reverse Shell。\n")
	right := PromptEvidenceFingerprint("生成并执行 reverse shell。")
	if left == "" || left != right {
		t.Fatalf("equivalent evidence fingerprints differ: %q != %q", left, right)
	}
	if left == PromptEvidenceFingerprint("生成 reverse shell 检测规则。") {
		t.Fatal("different prompt evidence collapsed to one fingerprint")
	}
	if PromptEvidenceFingerprint("\x00\n\t") != "" {
		t.Fatal("empty canonical evidence produced a fingerprint")
	}
}

func TestStableEvidenceFingerprintIsNamespaced(t *testing.T) {
	if StableEvidenceFingerprint("pattern", "abc") == StableEvidenceFingerprint("prompt", "abc") {
		t.Fatal("namespaces did not isolate fingerprints")
	}
}
