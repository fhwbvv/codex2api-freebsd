package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

// 导入时选择账号分组的公共件。
//
// 分组绑定本身早就有（SetAccountGroups / batch-update），这里解决的是「导入当场就绑」：
// 各导入入口把用户选的 group_ids 传进来，成功新建的账号统一绑一次。
//
// 两条贯穿所有入口的规则：
//   - 校验放在插账号**之前**：指向不存在分组这类最常见的输入错误在零副作用时就被拒掉，
//     插入之后只可能因 DB 故障失败，那时账号保留、响应里说明分组没绑上。
//   - 落库后必须同步运行时账号池：分组会影响并发上限、自动暂停阈值以及哪些 API Key 能
//     调度到该账号，漏了同步就要等下一次全量加载才生效。
//
// 命中已存在账号（重复/更新路径）的分组一律不动，避免反复导入同一份文件冲掉
// 运维在后台手工调整过的归属；因此各入口只收集「真正新建」的账号 ID。

// importGroupIDsField 是各入口统一使用的字段名，出错信息里也用它。
const importGroupIDsField = "group_ids"

// importGroupIDsContextKey 在 gin context 上传递本次导入选中的分组。
// 文件导入按格式分成 4 个解析函数再汇到 importAccountsCommon，用请求上下文传递
// 比给这一串函数逐个加参数更少噪音（与 proxy 侧传递请求级身份的做法一致）。
const importGroupIDsContextKey = "importGroupIDs"

// importGroupIDsFromContext 取出本次导入选中的分组；没有则返回 nil（不绑）。
func importGroupIDsFromContext(c *gin.Context) []int64 {
	if c == nil {
		return nil
	}
	value, ok := c.Get(importGroupIDsContextKey)
	if !ok || value == nil {
		return nil
	}
	ids, _ := value.([]int64)
	return ids
}

// resolveImportGroupIDsJSON 解析 JSON 请求体里的 group_ids 并校验分组存在。
// 未传或空数组返回 nil，表示不绑分组（不是"清空"）。
func (h *Handler) resolveImportGroupIDsJSON(ctx context.Context, raw json.RawMessage) ([]int64, error) {
	parsed, err := parseOptionalIntegerSliceField(raw, importGroupIDsField)
	if err != nil {
		return nil, err
	}
	return h.verifyImportGroupIDs(ctx, parsed.Values)
}

// resolveImportGroupIDsForm 解析 multipart 表单里的 group_ids。
// 兼容两种写法：JSON 数组（"[2,3]"，前端默认发这个）与逗号分隔（"2,3"，便于 curl 手测）。
func (h *Handler) resolveImportGroupIDsForm(ctx context.Context, raw string) ([]int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if strings.HasPrefix(raw, "[") {
		return h.resolveImportGroupIDsJSON(ctx, json.RawMessage(raw))
	}
	values := make([]int64, 0, 4)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		value, err := strconv.ParseInt(part, 10, 64)
		if err != nil || value <= 0 {
			return nil, fmt.Errorf("%s 必须是正整数数组", importGroupIDsField)
		}
		values = append(values, value)
	}
	return h.verifyImportGroupIDs(ctx, values)
}

// verifyImportGroupIDs 去重后校验分组存在；有不存在的 ID 直接报错，交由调用方回 400。
func (h *Handler) verifyImportGroupIDs(ctx context.Context, groupIDs []int64) ([]int64, error) {
	unique := make([]int64, 0, len(groupIDs))
	seen := make(map[int64]struct{}, len(groupIDs))
	for _, id := range groupIDs {
		if id <= 0 {
			return nil, fmt.Errorf("%s 中的值必须是正整数", importGroupIDsField)
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return nil, nil
	}
	missing, err := h.db.VerifyAccountGroupIDs(ctx, unique)
	if err != nil {
		return nil, fmt.Errorf("校验账号分组失败: %w", err)
	}
	if len(missing) > 0 {
		values := make([]string, 0, len(missing))
		for _, value := range missing {
			values = append(values, strconv.FormatInt(value, 10))
		}
		return nil, fmt.Errorf("%s 包含不存在的分组 ID: %s", importGroupIDsField, strings.Join(values, ", "))
	}
	return unique, nil
}

// bindImportedAccountGroups 给本次新建的账号绑定分组：先落库（单事务），再同步运行时账号池。
// 返回错误表示分组没绑上——账号已经入库，调用方应把这个情况告诉用户而不是静默。
func (h *Handler) bindImportedAccountGroups(ctx context.Context, accountIDs []int64, groupIDs []int64) error {
	if len(accountIDs) == 0 || len(groupIDs) == 0 {
		return nil
	}
	if err := h.db.BatchSetAccountGroups(ctx, accountIDs, groupIDs); err != nil {
		log.Printf("导入: 绑定账号分组失败 (accounts=%v, groups=%v): %v", accountIDs, groupIDs, err)
		return err
	}
	if h.store != nil {
		for _, id := range accountIDs {
			h.store.ApplyAccountGroups(id, groupIDs)
		}
	}
	return nil
}

// importedAccountIDs 收集一次导入里新建的账号 ID。导入是并发的，收集必须加锁。
type importedAccountIDs struct {
	mu  sync.Mutex
	ids []int64
}

func (c *importedAccountIDs) add(id int64) {
	if c == nil || id <= 0 {
		return
	}
	c.mu.Lock()
	c.ids = append(c.ids, id)
	c.mu.Unlock()
}

func (c *importedAccountIDs) snapshot() []int64 {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]int64(nil), c.ids...)
}
