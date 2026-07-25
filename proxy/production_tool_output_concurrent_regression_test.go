package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/codex2api/security/promptfilter"
	"github.com/gin-gonic/gin"
)

const productionChildTaskToolOutputProxy = `Read prd.md and implement.md and keep each child's acceptance criteria testable. create new children with task.py create "<title>" --slug <name> --parent <parent-dir>. link existing tasks.`

// This is the exact production false-positive family that previously matched
// the old create+children proximity rule. The auxiliary layer is deliberately
// set to enforce: a clean result must come from correct intent detection, not
// from the default profile leaving tool output in shadow/off mode.
func TestProductionChildTaskToolOutputConcurrentV1Matrix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name      string
		endpoint  string
		model     string
		transport promptfilter.Transport
		body      []byte
	}{
		{
			name:      "responses_http",
			endpoint:  "/v1/responses",
			model:     "gpt-5.5",
			transport: promptfilter.TransportHTTP,
			body:      []byte(fmt.Sprintf(`{"model":"gpt-5.5","input":[{"type":"function_call_output","call_id":"call_1","output":%q}]}`, productionChildTaskToolOutputProxy)),
		},
		{
			name:      "responses_websocket",
			endpoint:  "/v1/responses",
			model:     "gpt-5.5",
			transport: promptfilter.TransportWebSocket,
			body:      []byte(fmt.Sprintf(`{"type":"response.create","model":"gpt-5.5","input":[{"type":"function_call_output","call_id":"call_1","output":%q}]}`, productionChildTaskToolOutputProxy)),
		},
		{
			name:      "chat_completions",
			endpoint:  "/v1/chat/completions",
			model:     "gpt-5.5",
			transport: promptfilter.TransportHTTP,
			body:      []byte(fmt.Sprintf(`{"model":"gpt-5.5","messages":[{"role":"tool","tool_call_id":"call_1","content":%q}]}`, productionChildTaskToolOutputProxy)),
		},
		{
			name:      "messages",
			endpoint:  "/v1/messages",
			model:     "claude-sonnet-4",
			transport: promptfilter.TransportHTTP,
			body:      []byte(fmt.Sprintf(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool_1","content":%q}]}]}`, productionChildTaskToolOutputProxy)),
		},
	}
	profiles := []string{
		promptfilter.GuardProfileBalanced,
		promptfilter.GuardProfileStrict,
		promptfilter.GuardProfileResearch,
	}
	const (
		repetitions = 300
		concurrency = 50
	)

	for _, profile := range profiles {
		cfg := promptGuardTestConfig()
		cfg.Advanced.Guard.Mode = promptfilter.GuardModeEnforce
		cfg.Advanced.Guard.DefaultProfile = profile
		cfg.Advanced.Guard.Layers.ToolOutput.Mode = promptfilter.GuardModeEnforce
		handler := newPromptGuardTestHandler(promptfilter.NormalizeConfig(cfg))

		for _, tc := range tests {
			t.Run(profile+"/"+tc.name, func(t *testing.T) {
				jobs := make(chan int, concurrency)
				errs := make(chan error, repetitions)
				var workers sync.WaitGroup
				for range concurrency {
					workers.Add(1)
					go func() {
						defer workers.Done()
						for iteration := range jobs {
							c, _ := gin.CreateTestContext(httptest.NewRecorder())
							c.Request = httptest.NewRequest(http.MethodPost, tc.endpoint, nil)
							decision := handler.evaluatePromptGuard(c, tc.body, tc.body, tc.endpoint, tc.model, tc.transport).Decision
							if decision.Action != promptfilter.ActionAllow || decision.Score != 0 || decision.AuditScore != 0 || decision.StrikeEligible || len(decision.Signals) != 0 {
								errs <- fmt.Errorf("iteration=%d decision=%+v", iteration, decision)
							}
						}
					}()
				}
				for iteration := range repetitions {
					jobs <- iteration
				}
				close(jobs)
				workers.Wait()
				close(errs)
				for err := range errs {
					t.Fatal(err)
				}
			})
		}
	}
}
