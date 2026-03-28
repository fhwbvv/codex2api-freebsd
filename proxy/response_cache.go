package proxy

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ==================== 响应上下文缓存 ====================
// 用于解决 previous_response_id 场景下 tool calling 上下文丢失的问题。
// 代理层设置 store=false 并删除 previous_response_id，导致上游无法恢复历史 function_call。
// 本模块在本地缓存每次响应的累积对话上下文，当下一个请求带 previous_response_id 时，
// 自动将历史 items 注入回 input，使上游无需依赖服务端存储即可匹配 call_id。

const (
	responseCacheTTL      = 10 * time.Minute
	responseCacheMaxItems = 5000 // 最大缓存条目数，防止内存无限增长
)

type responseCacheEntry struct {
	items     []json.RawMessage
	createdAt time.Time
}

var respCache struct {
	mu    sync.RWMutex
	store map[string]*responseCacheEntry
}

func init() {
	respCache.store = make(map[string]*responseCacheEntry)
	go respCacheCleanupLoop()
}

// setResponseCache 存储响应上下文
func setResponseCache(responseID string, items []json.RawMessage) {
	respCache.mu.Lock()
	// 超过上限时跳过写入，等待清理腾出空间
	if len(respCache.store) >= responseCacheMaxItems {
		respCache.mu.Unlock()
		return
	}
	respCache.store[responseID] = &responseCacheEntry{
		items:     items,
		createdAt: time.Now(),
	}
	respCache.mu.Unlock()
}

// getResponseCache 查找缓存的响应上下文
func getResponseCache(responseID string) []json.RawMessage {
	respCache.mu.RLock()
	entry, ok := respCache.store[responseID]
	respCache.mu.RUnlock()
	if !ok || time.Since(entry.createdAt) > responseCacheTTL {
		return nil
	}
	return entry.items
}

// respCacheCleanupLoop 后台清理过期条目
func respCacheCleanupLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		respCache.mu.Lock()
		for k, v := range respCache.store {
			if now.Sub(v.createdAt) > responseCacheTTL {
				delete(respCache.store, k)
			}
		}
		respCache.mu.Unlock()
	}
}

// expandPreviousResponse 检查请求中是否有 previous_response_id，
// 如果有且缓存命中，则将历史对话 items 注入到 input 头部。
// 返回处理后的 body 和提取到的 previous_response_id（用于后续缓存链路）。
func expandPreviousResponse(codexBody []byte) ([]byte, string) {
	prevID := gjson.GetBytes(codexBody, "previous_response_id").String()
	if prevID == "" {
		return codexBody, ""
	}

	cached := getResponseCache(prevID)
	if cached == nil {
		// 缓存未命中（首次请求 / 过期 / 其他实例），无法展开，按原样继续
		return codexBody, prevID
	}

	// 构建新 input: 缓存的历史 items + 当前 input items
	currentInput := gjson.GetBytes(codexBody, "input")
	var merged []json.RawMessage
	merged = append(merged, cached...)
	if currentInput.IsArray() {
		currentInput.ForEach(func(_, v gjson.Result) bool {
			merged = append(merged, json.RawMessage(v.Raw))
			return true
		})
	}

	mergedJSON, err := json.Marshal(merged)
	if err != nil {
		log.Printf("展开 previous_response_id 失败: %v", err)
		return codexBody, prevID
	}

	codexBody, _ = sjson.SetRawBytes(codexBody, "input", mergedJSON)
	log.Printf("已展开 previous_response_id=%s，注入 %d 条历史 items", prevID, len(cached))
	return codexBody, prevID
}

// cacheCompletedResponse 从 response.completed 事件中提取 response.id 和 response.output，
// 与当前请求的 expanded input 合并后存入缓存。
// 仅在响应包含 function_call 时才缓存，避免为普通对话浪费内存。
func cacheCompletedResponse(expandedInputRaw []byte, completedData []byte) {
	respID := gjson.GetBytes(completedData, "response.id").String()
	if respID == "" {
		return
	}

	// 仅在响应包含 function_call 时才缓存（普通对话无需 previous_response_id 展开）
	output := gjson.GetBytes(completedData, "response.output")
	if !output.IsArray() {
		return
	}
	hasFunctionCall := false
	output.ForEach(func(_, item gjson.Result) bool {
		if item.Get("type").String() == "function_call" {
			hasFunctionCall = true
			return false
		}
		return true
	})
	if !hasFunctionCall {
		return
	}

	var items []json.RawMessage

	// 添加展开后的请求 input items
	inputItems := gjson.ParseBytes(expandedInputRaw)
	if inputItems.IsArray() {
		inputItems.ForEach(func(_, v gjson.Result) bool {
			items = append(items, json.RawMessage(v.Raw))
			return true
		})
	}

	// 添加响应 output items
	output.ForEach(func(_, v gjson.Result) bool {
		items = append(items, json.RawMessage(v.Raw))
		return true
	})

	if len(items) > 0 {
		setResponseCache(respID, items)
	}
}
