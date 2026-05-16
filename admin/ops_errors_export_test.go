package admin

import (
	"testing"
	"time"

	"github.com/codex2api/database"
)

func TestBuildOpsErrorExportFileDedupesAndExcludesStatuses(t *testing.T) {
	start := time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	filter := database.UsageLogFilter{
		Start: start,
		End:   end,
		Model: "gpt-5.4",
	}
	logs := []*database.UsageLog{
		{
			ID:                1,
			CreatedAt:         start.Add(10 * time.Minute),
			StatusCode:        500,
			UpstreamErrorKind: "upstream_400",
			ErrorMessage:      "Response input messages must contain json",
			InboundEndpoint:   "/v1/responses",
			UpstreamEndpoint:  "https://api.openai.com/v1/responses",
			Model:             "gpt-5.4",
			EffectiveModel:    "gpt-5.4",
			AccountID:         11,
			APIKeyID:          101,
			IsRetryAttempt:    true,
			AttemptIndex:      1,
		},
		{
			ID:                2,
			CreatedAt:         start.Add(20 * time.Minute),
			StatusCode:        500,
			UpstreamErrorKind: "upstream_400",
			ErrorMessage:      "Response input messages   must contain json",
			InboundEndpoint:   "/v1/responses",
			UpstreamEndpoint:  "https://api.openai.com/v1/responses",
			Model:             "gpt-5.4",
			EffectiveModel:    "gpt-5.4",
			AccountID:         12,
			APIKeyID:          102,
		},
		{
			ID:                3,
			CreatedAt:         start.Add(30 * time.Minute),
			StatusCode:        429,
			UpstreamErrorKind: "rate_limit",
			ErrorMessage:      "too many requests",
			InboundEndpoint:   "/v1/responses",
			Model:             "gpt-5.4",
		},
		{
			ID:                4,
			CreatedAt:         start.Add(40 * time.Minute),
			StatusCode:        401,
			UpstreamErrorKind: "unauthorized",
			ErrorMessage:      "invalid key",
			InboundEndpoint:   "/v1/responses",
			Model:             "gpt-5.4",
		},
	}
	excludedCodes, excludedSet, ok := parseExcludedStatusCodes("429,401")
	if !ok {
		t.Fatal("exclude status parse failed")
	}

	exportFile := buildOpsErrorExportFile(logs, filter, true, excludedCodes, excludedSet)

	if exportFile.TotalMatched != 4 {
		t.Fatalf("TotalMatched = %d, want 4", exportFile.TotalMatched)
	}
	if exportFile.ExcludedCount != 2 {
		t.Fatalf("ExcludedCount = %d, want 2", exportFile.ExcludedCount)
	}
	if exportFile.ExportedCount != 1 {
		t.Fatalf("ExportedCount = %d, want 1", exportFile.ExportedCount)
	}
	if exportFile.DuplicatesRemoved != 1 {
		t.Fatalf("DuplicatesRemoved = %d, want 1", exportFile.DuplicatesRemoved)
	}
	if len(exportFile.Errors) != 1 {
		t.Fatalf("len(Errors) = %d, want 1", len(exportFile.Errors))
	}
	entry := exportFile.Errors[0]
	if entry.Occurrences != 2 {
		t.Fatalf("Occurrences = %d, want 2", entry.Occurrences)
	}
	if got, want := entry.SampleIDs, []int64{1, 2}; !sameInt64Slice(got, want) {
		t.Fatalf("SampleIDs = %v, want %v", got, want)
	}
	if got, want := entry.AffectedAccountIDs, []int64{11, 12}; !sameInt64Slice(got, want) {
		t.Fatalf("AffectedAccountIDs = %v, want %v", got, want)
	}
	if got, want := entry.AffectedAPIKeyIDs, []int64{101, 102}; !sameInt64Slice(got, want) {
		t.Fatalf("AffectedAPIKeyIDs = %v, want %v", got, want)
	}
	if !entry.FirstSeen.Equal(logs[0].CreatedAt) || !entry.LastSeen.Equal(logs[1].CreatedAt) {
		t.Fatalf("time range = %s..%s, want %s..%s", entry.FirstSeen, entry.LastSeen, logs[0].CreatedAt, logs[1].CreatedAt)
	}
}

func TestBuildOpsErrorExportFileRawKeepsDuplicates(t *testing.T) {
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	logs := []*database.UsageLog{
		{ID: 1, CreatedAt: now, StatusCode: 500, ErrorMessage: "same", InboundEndpoint: "/v1/responses"},
		{ID: 2, CreatedAt: now.Add(time.Minute), StatusCode: 500, ErrorMessage: "same", InboundEndpoint: "/v1/responses"},
	}

	exportFile := buildOpsErrorExportFile(logs, database.UsageLogFilter{}, false, nil, map[int]bool{})

	if exportFile.ExportedCount != 2 {
		t.Fatalf("ExportedCount = %d, want 2", exportFile.ExportedCount)
	}
	if exportFile.DuplicatesRemoved != 0 {
		t.Fatalf("DuplicatesRemoved = %d, want 0", exportFile.DuplicatesRemoved)
	}
	if len(exportFile.Errors) != 2 {
		t.Fatalf("len(Errors) = %d, want 2", len(exportFile.Errors))
	}
}

func sameInt64Slice(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
