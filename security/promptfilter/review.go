package promptfilter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultReviewBaseURL        = "https://api.openai.com"
	DefaultReviewModel          = "omni-moderation-latest"
	DefaultReviewTimeoutSeconds = 10
)

type ReviewClient struct {
	HTTPClient *http.Client
}

var DefaultReviewClient = ReviewClient{}

type reviewRequest struct {
	Model string `json:"model,omitempty"`
	Input string `json:"input"`
}

type reviewResponse struct {
	Model   string         `json:"model"`
	Results []reviewResult `json:"results"`
}

type reviewResult struct {
	Flagged bool `json:"flagged"`
}

func NormalizeReviewConfig(cfg ReviewConfig) ReviewConfig {
	defaults := DefaultReviewConfig()
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaults.BaseURL
	}
	cfg.Model = strings.TrimSpace(cfg.Model)
	if cfg.Model == "" {
		cfg.Model = defaults.Model
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = defaults.TimeoutSeconds
	}
	if cfg.TimeoutSeconds > 60 {
		cfg.TimeoutSeconds = 60
	}
	return cfg
}

func (cfg ReviewConfig) Ready() bool {
	cfg = NormalizeReviewConfig(cfg)
	return cfg.Enabled && cfg.APIKey != "" && cfg.BaseURL != ""
}

func ValidateReviewConfig(cfg ReviewConfig) error {
	cfg = NormalizeReviewConfig(cfg)
	if cfg.Enabled && cfg.APIKey == "" {
		return fmt.Errorf("review api key is required when prompt filter review is enabled")
	}
	if cfg.BaseURL == "" {
		return nil
	}
	_, err := reviewEndpoint(cfg.BaseURL)
	return err
}

func (c ReviewClient) ReviewText(ctx context.Context, text string, cfg ReviewConfig) (bool, string, error) {
	cfg = NormalizeReviewConfig(cfg)
	if !cfg.Ready() {
		return false, cfg.Model, nil
	}
	if strings.TrimSpace(text) == "" {
		return false, cfg.Model, nil
	}
	endpoint, err := reviewEndpoint(cfg.BaseURL)
	if err != nil {
		return false, cfg.Model, err
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutSeconds)*time.Second)
	defer cancel()

	payload, err := json.Marshal(reviewRequest{
		Model: cfg.Model,
		Input: text,
	})
	if err != nil {
		return false, cfg.Model, err
	}
	req, err := http.NewRequestWithContext(timeoutCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return false, cfg.Model, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, cfg.Model, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, cfg.Model, fmt.Errorf("review request failed with status %d", resp.StatusCode)
	}

	var decoded reviewResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return false, cfg.Model, err
	}
	if len(decoded.Results) == 0 {
		return false, cfg.Model, fmt.Errorf("review response missing results")
	}
	flagged := false
	for _, result := range decoded.Results {
		if result.Flagged {
			flagged = true
			break
		}
	}
	model := strings.TrimSpace(decoded.Model)
	if model == "" {
		model = cfg.Model
	}
	return flagged, model, nil
}

func ApplyReviewResult(verdict Verdict, flagged bool, model string, reviewErr error, cfg ReviewConfig) Verdict {
	cfg = NormalizeReviewConfig(cfg)
	verdict.Reviewed = true
	verdict.ReviewFlagged = flagged
	verdict.ReviewModel = strings.TrimSpace(model)
	if verdict.ReviewModel == "" {
		verdict.ReviewModel = cfg.Model
	}
	if reviewErr != nil {
		verdict.ReviewError = reviewErr.Error()
		if cfg.FailClosed {
			verdict.Action = ActionBlock
			verdict.Reason = "prompt review failed: " + reviewErr.Error()
		} else {
			verdict.Action = ActionAllow
			verdict.Reason = "prompt review failed; allowed by policy: " + reviewErr.Error()
		}
		return verdict
	}
	if !flagged {
		verdict.Action = ActionAllow
		verdict.Reason = "prompt review cleared local filter match"
		return verdict
	}
	verdict.Reason = "prompt review confirmed local filter match"
	return verdict
}

func reviewEndpoint(baseURL string) (string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultReviewBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("review base_url must start with http:// or https://")
	}
	if strings.HasSuffix(parsed.Path, "/moderations") {
		return parsed.String(), nil
	}
	path := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(path, "/v1") {
		parsed.Path = path + "/moderations"
	} else {
		parsed.Path = path + "/v1/moderations"
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}
