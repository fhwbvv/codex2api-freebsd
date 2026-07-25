package promptfilter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type externalSemanticCorpusRow struct {
	Set      string `json:"set"`
	Category string `json:"category"`
	ID       string `json:"id"`
	Text     string `json:"text"`
}

// TestExternalSemanticCorpusReplay replays QA corpora captured outside the
// repository. It is opt-in so ordinary contributors do not need local report
// artifacts, while release QA can still use the exact production-like corpus.
func TestExternalSemanticCorpusReplay(t *testing.T) {
	rawPaths := strings.TrimSpace(os.Getenv("PROMPT_FILTER_QA_CORPUS"))
	if rawPaths == "" {
		t.Skip("set PROMPT_FILTER_QA_CORPUS to replay external semantic corpora")
	}

	cfg := recommendedEnabledConfig()
	total := 0
	falseBlocks := 0
	misses := 0
	falseBlockCategories := map[string]int{}
	missCategories := map[string]int{}
	failures := make([]string, 0, 24)
	for _, path := range filepath.SplitList(rawPaths) {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read corpus %s: %v", path, err)
		}
		var rows []externalSemanticCorpusRow
		if err := json.Unmarshal(data, &rows); err != nil {
			t.Fatalf("decode corpus %s: %v", path, err)
		}
		defaultSet := ""
		if strings.Contains(strings.ToLower(filepath.Base(path)), "benign") {
			defaultSet = "benign"
		}
		for _, row := range rows {
			set := strings.ToLower(strings.TrimSpace(row.Set))
			if set == "" {
				set = defaultSet
			}
			if set != "benign" && set != "malicious" {
				continue
			}
			total++
			verdict := InspectText(row.Text, cfg)
			wantBlock := set == "malicious"
			gotBlock := verdict.Action == ActionBlock
			if gotBlock == wantBlock {
				continue
			}
			if wantBlock {
				misses++
				missCategories[row.Category]++
			} else {
				falseBlocks++
				falseBlockCategories[row.Category]++
			}
			if len(failures) < cap(failures) {
				failures = append(failures, fmt.Sprintf("%s/%s action=%s score=%d matches=%v text=%q", row.Category, row.ID, verdict.Action, verdict.Score, verdict.Matched, row.Text))
			}
		}
	}

	t.Logf("external semantic corpus: total=%d false_blocks=%d false_block_categories=%v misses=%d miss_categories=%v", total, falseBlocks, falseBlockCategories, misses, missCategories)
	if falseBlocks != 0 || misses != 0 {
		t.Fatalf("semantic corpus regression: false_blocks=%d misses=%d samples:\n%s", falseBlocks, misses, strings.Join(failures, "\n"))
	}
}
