package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"log"
	"strings"
	"sync"

	"github.com/tidwall/gjson"
)

const grokToolOrderMemoLimit = 256

var grokToolOrderMemo = struct {
	sync.Mutex
	order map[string][]string
}{order: make(map[string][]string)}

func resetGrokToolOrderMemo() {
	grokToolOrderMemo.Lock()
	grokToolOrderMemo.order = make(map[string][]string)
	grokToolOrderMemo.Unlock()
}

// grokToolOrderKey 用静态 system + 首条 user 做会话键，故意不把 tools
// 算进去，否则中途多一个 MCP 工具会换键，首次顺序记忆就断了。
func grokToolOrderKey(body []byte) string {
	firstUser := grokFirstUserSeed(body)
	if firstUser == "" {
		return ""
	}
	system := ""
	if raw := gjson.GetBytes(body, "system"); raw.Exists() && raw.Raw != "null" {
		system = stableAnthropicSystemSeed([]byte(raw.Raw))
	}
	sum := sha256.Sum256([]byte("codex2api:grok-tools:" + system + "\x00" + firstUser))
	return hex.EncodeToString(sum[:16])
}

func grokFirstUserSeed(body []byte) string {
	if msgs := gjson.GetBytes(body, "messages"); msgs.IsArray() {
		var seed string
		msgs.ForEach(func(_, item gjson.Result) bool {
			if item.Get("role").String() != "user" {
				return true
			}
			seed = strings.TrimSpace(item.Get("content").Raw)
			return false
		})
		return seed
	}
	if input := gjson.GetBytes(body, "input"); input.IsArray() {
		var seed string
		input.ForEach(func(_, item gjson.Result) bool {
			if item.Get("role").String() != "user" {
				return true
			}
			seed = strings.TrimSpace(item.Get("content").Raw)
			return false
		})
		return seed
	}
	return ""
}

func grokToolName(tool any) string {
	item, ok := tool.(map[string]any)
	if !ok {
		return ""
	}
	name, _ := item["name"].(string)
	return strings.TrimSpace(name)
}

// stabilizeGrokToolOrder 按会话记住工具首次出现的顺序。同一会话后续请求
// 即使 Claude Code 重排或在中间插入 MCP 工具，已见过的工具仍按首次顺序输出，
// 新工具只追加在表尾，避免 Grok 整表前缀被打穿。
func stabilizeGrokToolOrder(orderKey string, tools []any) []any {
	if orderKey == "" || len(tools) < 2 {
		return tools
	}
	names := make([]string, len(tools))
	byName := make(map[string]any, len(tools))
	for i, tool := range tools {
		name := grokToolName(tool)
		names[i] = name
		if name != "" {
			byName[name] = tool
		}
	}
	remembered := rememberGrokToolOrder(orderKey, names)
	out := make([]any, 0, len(tools))
	seen := make(map[string]bool, len(tools))
	for _, name := range remembered {
		tool, ok := byName[name]
		if !ok {
			continue
		}
		out = append(out, tool)
		seen[name] = true
	}
	for i, name := range names {
		if name != "" {
			if seen[name] {
				continue
			}
			out = append(out, byName[name])
			seen[name] = true
			continue
		}
		out = append(out, tools[i])
	}
	return out
}

func rememberGrokToolOrder(orderKey string, names []string) []string {
	grokToolOrderMemo.Lock()
	defer grokToolOrderMemo.Unlock()
	if grokToolOrderMemo.order == nil {
		grokToolOrderMemo.order = make(map[string][]string)
	}
	existing := grokToolOrderMemo.order[orderKey]
	have := make(map[string]bool, len(existing)+len(names))
	for _, name := range existing {
		have[name] = true
	}
	for _, name := range names {
		if name == "" || have[name] {
			continue
		}
		existing = append(existing, name)
		have[name] = true
	}
	if len(grokToolOrderMemo.order) >= grokToolOrderMemoLimit {
		if _, ok := grokToolOrderMemo.order[orderKey]; !ok {
			grokToolOrderMemo.order = map[string][]string{orderKey: existing}
			return existing
		}
	}
	grokToolOrderMemo.order[orderKey] = existing
	return existing
}

func grokShortHash(raw string) string {
	if raw == "" || raw == "null" {
		return "-"
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:8])
}

func logGrokPrefixFingerprint(body []byte, turnIdx int, model string) {
	if len(body) == 0 {
		return
	}
	nDev := 0
	var sys strings.Builder
	gjson.GetBytes(body, "input").ForEach(func(_, item gjson.Result) bool {
		if item.Get("role").String() != "developer" {
			return nDev == 0
		}
		nDev++
		if nDev > 1 {
			sys.WriteByte(0)
		}
		sys.WriteString(item.Get("content").Raw)
		return nDev < 8
	})
	log.Printf("Grok prefix fingerprint tools=%s sys=%s ntools=%d ndev=%d turn=%d model=%s",
		grokShortHash(gjson.GetBytes(body, "tools").Raw),
		grokShortHash(sys.String()),
		int(gjson.GetBytes(body, "tools.#").Int()),
		nDev,
		turnIdx,
		strings.TrimSpace(model),
	)
}
