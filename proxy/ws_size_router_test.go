package proxy

import (
	"testing"
	"time"
)

func TestWebsocketSizeRouterLearnsAndRoutes(t *testing.T) {
	var r websocketSizeRouter

	if r.PreferHTTP(10 * 1024 * 1024) {
		t.Fatal("未学习任何样本时不应改路 HTTP")
	}

	r.RecordMessageTooBig(400 * 1024)
	if !r.PreferHTTP(400 * 1024) {
		t.Fatal("达到已知失败体积的请求应改路 HTTP")
	}
	if !r.PreferHTTP(390 * 1024) {
		t.Fatal("余量内(95%)的请求应改路 HTTP")
	}
	if r.PreferHTTP(300 * 1024) {
		t.Fatal("明显小于阈值的请求应继续走 WS")
	}

	// 更小的失败样本收紧阈值;更大的不放宽
	r.RecordMessageTooBig(350 * 1024)
	if !r.PreferHTTP(340 * 1024) {
		t.Fatal("更小的失败样本应收紧阈值")
	}
	r.RecordMessageTooBig(900 * 1024)
	if r.minTooBig != 350*1024 {
		t.Fatalf("更大的失败样本不应放宽阈值, minTooBig=%d", r.minTooBig)
	}
}

func TestWebsocketSizeRouterIgnoresTinySamples(t *testing.T) {
	var r websocketSizeRouter
	r.RecordMessageTooBig(wsSizeRouterMinSample - 1)
	if r.PreferHTTP(10 * 1024 * 1024) {
		t.Fatal("低于样本下限的 1009 不应参与学习")
	}
}

func TestWebsocketSizeRouterExpiresLearnedThreshold(t *testing.T) {
	var r websocketSizeRouter
	r.RecordMessageTooBig(400 * 1024)
	r.learnedAt = time.Now().Add(-wsSizeRouterTTL - time.Minute)
	if r.PreferHTTP(500 * 1024) {
		t.Fatal("过期的学习结果不应继续生效")
	}
	if r.minTooBig != 0 {
		t.Fatal("过期后应清空学习状态")
	}
}

func TestWebsocketSizeRouterEnvEscapeHatch(t *testing.T) {
	t.Setenv("CODEX_WS_SIZE_ROUTER", "off")
	var r websocketSizeRouter
	r.RecordMessageTooBig(400 * 1024)
	if r.PreferHTTP(500 * 1024) {
		t.Fatal("CODEX_WS_SIZE_ROUTER=off 时应保持旧行为")
	}
}
