package promptfilter

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite"
)

type corpusReplayMetrics struct {
	Rows             int
	FullTextRows     int
	PreviewRows      int
	MatchedRows      int
	AllowedRows      int
	BlockedRows      int
	WarnedRows       int
	TerminalRows     int
	ScoreSum         int64
	MaxScore         int
	ScoreBuckets     [6]int
	UnstableRows     int
	TerminalOnlyHits int
	TerminalOnlyIDs  []int64
}

func (m *corpusReplayMetrics) add(id int64, fullText bool, verdict Verdict, repeat *Verdict, normalAction string) {
	m.Rows++
	if fullText {
		m.FullTextRows++
	} else {
		m.PreviewRows++
	}
	if len(verdict.Matched) > 0 {
		m.MatchedRows++
	}
	switch verdict.Action {
	case ActionBlock:
		m.BlockedRows++
	case ActionWarn:
		m.WarnedRows++
	default:
		m.AllowedRows++
	}
	if verdict.TerminalStrictHit {
		m.TerminalRows++
	}
	if normalAction != ActionBlock && verdict.Action == ActionBlock {
		m.TerminalOnlyHits++
		if len(m.TerminalOnlyIDs) < 20 {
			m.TerminalOnlyIDs = append(m.TerminalOnlyIDs, id)
		}
	}
	m.ScoreSum += int64(verdict.Score)
	if verdict.Score > m.MaxScore {
		m.MaxScore = verdict.Score
	}
	switch {
	case verdict.Score == 0:
		m.ScoreBuckets[0]++
	case verdict.Score < 25:
		m.ScoreBuckets[1]++
	case verdict.Score < 50:
		m.ScoreBuckets[2]++
	case verdict.Score < 90:
		m.ScoreBuckets[3]++
	case verdict.Score < 150:
		m.ScoreBuckets[4]++
	default:
		m.ScoreBuckets[5]++
	}
	if repeat != nil && (repeat.Score != verdict.Score || repeat.Action != verdict.Action || repeat.RawScore != verdict.RawScore || repeat.TerminalStrictHit != verdict.TerminalStrictHit) {
		m.UnstableRows++
	}
}

func (m *corpusReplayMetrics) merge(other corpusReplayMetrics) {
	m.Rows += other.Rows
	m.FullTextRows += other.FullTextRows
	m.PreviewRows += other.PreviewRows
	m.MatchedRows += other.MatchedRows
	m.AllowedRows += other.AllowedRows
	m.BlockedRows += other.BlockedRows
	m.WarnedRows += other.WarnedRows
	m.TerminalRows += other.TerminalRows
	m.ScoreSum += other.ScoreSum
	m.UnstableRows += other.UnstableRows
	m.TerminalOnlyHits += other.TerminalOnlyHits
	if other.MaxScore > m.MaxScore {
		m.MaxScore = other.MaxScore
	}
	for i := range m.ScoreBuckets {
		m.ScoreBuckets[i] += other.ScoreBuckets[i]
	}
	for _, id := range other.TerminalOnlyIDs {
		if len(m.TerminalOnlyIDs) >= 20 {
			break
		}
		m.TerminalOnlyIDs = append(m.TerminalOnlyIDs, id)
	}
}

func (m corpusReplayMetrics) String() string {
	avg := 0.0
	if m.Rows > 0 {
		avg = float64(m.ScoreSum) / float64(m.Rows)
	}
	sort.Slice(m.TerminalOnlyIDs, func(i, j int) bool { return m.TerminalOnlyIDs[i] < m.TerminalOnlyIDs[j] })
	return fmt.Sprintf("rows=%d full_text=%d preview=%d matched=%d (%.2f%%) allow=%d block=%d (%.2f%%) warn=%d terminal=%d terminal_only_blocks=%d avg_score=%.2f max_score=%d buckets=[0:%d 1-24:%d 25-49:%d 50-89:%d 90-149:%d 150+:%d] repeat_mismatches=%d terminal_only_sample_ids=%v",
		m.Rows, m.FullTextRows, m.PreviewRows, m.MatchedRows, percent(m.MatchedRows, m.Rows), m.AllowedRows, m.BlockedRows, percent(m.BlockedRows, m.Rows), m.WarnedRows, m.TerminalRows, m.TerminalOnlyHits, avg, m.MaxScore,
		m.ScoreBuckets[0], m.ScoreBuckets[1], m.ScoreBuckets[2], m.ScoreBuckets[3], m.ScoreBuckets[4], m.ScoreBuckets[5], m.UnstableRows, m.TerminalOnlyIDs)
}

func percent(value, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(value) * 100 / float64(total)
}

type corpusReplayJob struct {
	id       int64
	endpoint string
	text     string
	fullText bool
}

func TestProductionCorpusReplay(t *testing.T) {
	dbPath := strings.TrimSpace(os.Getenv("PROMPT_FILTER_CORPUS_DB"))
	if dbPath == "" {
		t.Skip("set PROMPT_FILTER_CORPUS_DB to run the production corpus replay")
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	rows, err := db.Query(`select id, endpoint, full_text, text_preview from prompt_filter_logs order by id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	normalCfg := testConfig(ModeBlock)
	normalCfg.StrictTerminalEnabled = false
	terminalCfg := normalCfg
	terminalCfg.StrictTerminalEnabled = true

	workers := runtime.GOMAXPROCS(0)
	if workers > 12 {
		workers = 12
	}
	jobs := make(chan corpusReplayJob, workers*2)
	results := make(chan [2]corpusReplayMetrics, workers)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var normalMetrics, terminalMetrics corpusReplayMetrics
			for job := range jobs {
				normal := inspectCorpusPrompt(job.endpoint, job.text, normalCfg)
				terminal := inspectCorpusPrompt(job.endpoint, job.text, terminalCfg)
				var repeat *Verdict
				if job.id%500 == 0 {
					repeated := inspectCorpusPrompt(job.endpoint, job.text, terminalCfg)
					repeat = &repeated
				}
				normalMetrics.add(job.id, job.fullText, normal, nil, normal.Action)
				terminalMetrics.add(job.id, job.fullText, terminal, repeat, normal.Action)
			}
			results <- [2]corpusReplayMetrics{normalMetrics, terminalMetrics}
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	for rows.Next() {
		var id int64
		var endpoint, fullText, preview string
		if err := rows.Scan(&id, &endpoint, &fullText, &preview); err != nil {
			t.Fatal(err)
		}
		text := strings.TrimSpace(fullText)
		isFullText := text != ""
		if text == "" {
			text = strings.TrimSpace(preview)
		}
		if text == "" {
			continue
		}
		jobs <- corpusReplayJob{id: id, endpoint: endpoint, text: text, fullText: isFullText}
	}
	close(jobs)
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	var normalTotal, terminalTotal corpusReplayMetrics
	for result := range results {
		normalTotal.merge(result[0])
		terminalTotal.merge(result[1])
	}
	t.Logf("NORMAL:   %s", normalTotal.String())
	t.Logf("TERMINAL: %s", terminalTotal.String())
	if terminalTotal.UnstableRows != 0 {
		t.Fatalf("terminal replay produced %d repeat mismatches", terminalTotal.UnstableRows)
	}
}

func inspectCorpusPrompt(endpoint, text string, cfg Config) Verdict {
	endpoint = strings.ToLower(strings.TrimSpace(endpoint))
	var body any
	switch endpoint {
	case "/v1/chat/completions":
		body = map[string]any{"messages": []any{map[string]any{"role": "user", "content": text}}}
	case "/v1/messages":
		body = map[string]any{"messages": []any{map[string]any{"role": "user", "content": text}}}
	case "/v1/images/generations", "/v1/images/edits":
		body = map[string]any{"prompt": text}
	default:
		endpoint = "/v1/responses"
		body = map[string]any{"input": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": text}}}}}
	}
	data, _ := json.Marshal(body)
	return Inspect(data, endpoint, cfg)
}
