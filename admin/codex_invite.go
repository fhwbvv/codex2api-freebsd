package admin

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/codex2api/proxy"
	"github.com/gin-gonic/gin"
)

const (
	inviteDefaultMaxEmails = 10
	inviteUpperMaxEmails   = 50
)

var inviteEmailPattern = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
var inviteEmailSplitter = regexp.MustCompile(`[,;\r\n\t ]+`)

type inviteRequest struct {
	Emails      []string `json:"emails"`
	EmailsText  string   `json:"emails_text"`
	ReferralKey string   `json:"referral_key"`
	ProxyURL    string   `json:"proxy_url"`
	MaxEmails   int      `json:"max_emails"`
}

// SendInvite 通过指定账号向 ChatGPT 推荐邀请端点发送邀请邮件。
// POST /api/accounts/:id/invite
func (h *Handler) SendInvite(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		writeError(c, http.StatusBadRequest, "无效的账号 ID")
		return
	}

	var req inviteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}

	maxEmails := inviteDefaultMaxEmails
	if req.MaxEmails > 0 && req.MaxEmails < maxEmails {
		maxEmails = req.MaxEmails
	}
	if maxEmails > inviteUpperMaxEmails {
		maxEmails = inviteUpperMaxEmails
	}

	emails, err := collectInviteEmails(req.Emails, req.EmailsText, maxEmails)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	account := h.findAccountByID(id)
	if account == nil {
		writeError(c, http.StatusNotFound, "账号不存在")
		return
	}
	if account.GetAccessToken() == "" {
		writeError(c, http.StatusBadRequest, "账号没有可用的 access token，请先刷新账号")
		return
	}

	proxyURL := strings.TrimSpace(req.ProxyURL)
	if proxyURL == "" {
		proxyURL = h.store.ResolveProxyForAccount(account)
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
	defer cancel()

	result, err := proxy.SendCodexInvite(ctx, account, proxyURL, req.ReferralKey, emails)
	if err != nil {
		writeError(c, http.StatusBadGateway, "邀请请求失败: "+err.Error())
		return
	}

	if !result.OK {
		// 常见：无 referral 资格的账号返回 403。透传上游响应供前端展示。
		c.JSON(http.StatusOK, gin.H{
			"ok":     false,
			"result": result,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"result": result,
	})
}

// collectInviteEmails 合并 emails[] 与文本来源，去重（忽略大小写）、校验格式、夹紧上限。
func collectInviteEmails(list []string, text string, maxEmails int) ([]string, error) {
	raw := make([]string, 0, len(list))
	raw = append(raw, list...)
	if strings.TrimSpace(text) != "" {
		raw = append(raw, inviteEmailSplitter.Split(text, -1)...)
	}

	seen := make(map[string]struct{})
	emails := make([]string, 0, len(raw))
	for _, e := range raw {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		key := strings.ToLower(e)
		if _, ok := seen[key]; ok {
			continue
		}
		if !inviteEmailPattern.MatchString(e) {
			return nil, fmt.Errorf("邮箱格式不正确: %s", e)
		}
		seen[key] = struct{}{}
		emails = append(emails, e)
	}

	if len(emails) == 0 {
		return nil, fmt.Errorf("请至少提供一个邮箱")
	}
	if len(emails) > maxEmails {
		return nil, fmt.Errorf("邮箱数量超过上限 %d", maxEmails)
	}
	return emails, nil
}
