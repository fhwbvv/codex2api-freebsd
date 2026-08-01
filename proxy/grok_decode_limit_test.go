package proxy

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"testing"
)

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write(data); err != nil {
		t.Fatalf("gzip 写入失败: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip 收尾失败: %v", err)
	}
	return buf.Bytes()
}

func responseWithBody(body []byte, encoding, contentType string) *http.Response {
	header := http.Header{}
	if encoding != "" {
		header.Set("Content-Encoding", encoding)
	}
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	return &http.Response{
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

func TestDecodeGrokResponseEncodingNormalBody(t *testing.T) {
	plain := []byte(`{"config":{"creditUsagePercent":22.0}}`)
	resp := responseWithBody(gzipBytes(t, plain), "gzip", "application/json")

	decodeGrokResponseEncoding(resp)

	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if string(got) != string(plain) {
		t.Fatalf("解压结果 = %s, want %s", got, plain)
	}
	if resp.Header.Get("Content-Encoding") != "" {
		t.Fatalf("Content-Encoding 应被清除")
	}
	if !resp.Uncompressed {
		t.Fatalf("Uncompressed 应置位")
	}
	if resp.ContentLength != int64(len(plain)) {
		t.Fatalf("ContentLength = %d, want %d", resp.ContentLength, len(plain))
	}
}

func TestDecodeGrokResponseEncodingSkipsEventStream(t *testing.T) {
	compressed := gzipBytes(t, []byte("event: response.created\n"))
	resp := responseWithBody(compressed, "gzip", "text/event-stream")

	decodeGrokResponseEncoding(resp)

	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("流式响应不应被解压")
	}
	got, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(got, compressed) {
		t.Fatalf("流式响应体应原样保留")
	}
}

// TestDecodeGrokResponseEncodingHighRatioBomb 用高压缩比内容验证解压上限：
// 压缩态很小、解压态远超上限时必须放弃解压并原样透传，而不是把内存吃穿。
func TestDecodeGrokResponseEncodingHighRatioBomb(t *testing.T) {
	// 全零内容压缩比极高：解压后 grokMaxDecodedBody + 1MB，压缩态只有几百 KB。
	bomb := gzipBytes(t, make([]byte, grokMaxDecodedBody+(1<<20)))
	if len(bomb) > grokMaxCompressedBody {
		t.Fatalf("压缩体 %d 超过压缩上限，测不到解压上限", len(bomb))
	}
	resp := responseWithBody(bomb, "gzip", "application/json")

	decodeGrokResponseEncoding(resp)

	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("超限时不应声明已解压")
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if !bytes.Equal(got, bomb) {
		t.Fatalf("超限时应原样透传压缩体")
	}
}

func TestDecodeContentEncodingRejectsOversizedDecode(t *testing.T) {
	bomb := gzipBytes(t, make([]byte, grokMaxDecodedBody+1))
	if _, err := decodeContentEncoding(bomb, "gzip"); err == nil {
		t.Fatalf("解压产物超限应报错")
	}
}

// TestDecodeContentEncodingAcceptsAtLimit 恰好等于上限的内容必须能正常解压，
// 确认上限判定用的是"超过"而不是"达到"。
func TestDecodeContentEncodingAcceptsAtLimit(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 1<<20)
	out, err := decodeContentEncoding(gzipBytes(t, payload), "gzip")
	if err != nil {
		t.Fatalf("正常体积解压失败: %v", err)
	}
	if len(out) != len(payload) {
		t.Fatalf("解压长度 = %d, want %d", len(out), len(payload))
	}
}

func TestDecodeContentEncodingUnsupported(t *testing.T) {
	if _, err := decodeContentEncoding([]byte("x"), "exotic"); err == nil {
		t.Fatalf("未知编码应报错")
	}
	if !strings.Contains(func() string {
		_, err := decodeContentEncoding([]byte("x"), "exotic")
		return err.Error()
	}(), "exotic") {
		t.Fatalf("错误信息应带上编码名")
	}
}
