package proxy

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/tidwall/gjson"
)

// grokTurnIndex 估算会话内轮次序号（对齐官方 CLI 的 x-grok-turn-idx）。
// 无状态：按 input[] 里的 user 消息数计（每个 user 回合 +1），至少为 1。
// 与账号无关、对重试稳定（剥离密文不影响 user 消息计数）。
func grokTurnIndex(body []byte) int {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return 1
	}
	count := 0
	for _, item := range input.Array() {
		if item.Get("type").String() == "message" && strings.EqualFold(item.Get("role").String(), "user") {
			count++
		}
	}
	if count < 1 {
		return 1
	}
	return count
}

// grokBodyHasBlobs 判断请求体是否带有可能触发上游解码失败的外来密文
// （reasoning encrypted_content 或 compaction 项）。用于决定是否值得在 400 后重试。
func grokBodyHasBlobs(body []byte) bool {
	return bytes.Contains(body, []byte("encrypted_content")) || bytes.Contains(body, []byte(`"compaction"`))
}

// grokIsBlobDecodeFailure 判断上游 400 是否为密文解码失败（可通过剥离密文重试恢复）。
func grokIsBlobDecodeFailure(errBody []byte) bool {
	lower := bytes.ToLower(errBody)
	for _, marker := range [][]byte{
		[]byte("compaction blob"),
		[]byte("could not decrypt"),
		[]byte("could not decode the compaction"),
		[]byte("encrypted_content"),
	} {
		if bytes.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// grokMaxCompressedBody / grokMaxDecodedBody 是非流式响应体在压缩态与解压态的缓冲上限。
// 这条路径只承载错误体、billing 与非流式补全（实抓分别是数百字节与数十至数百 KB），
// 32MB / 128MB 留足余量。设上限是为了兜住畸形或恶意的高压缩比响应——
// 压缩态 1MB 的 gzip 可以膨胀到数 GB，无上限的整体缓冲会被直接放大成 OOM。
const (
	grokMaxCompressedBody = 32 << 20
	grokMaxDecodedBody    = 128 << 20
)

// decodeGrokResponseEncoding 在手动声明 Accept-Encoding 后接管响应解压。
// SSE 流上游不压缩（无 Content-Encoding），此处只处理非流式的压缩响应（错误/billing/非流式补全）：
// 整体缓冲后按 Content-Encoding 逆序解码。event-stream 一律跳过，避免缓冲流式响应。
// 超出上限或解码失败时原样返回压缩体，与既有"解码失败不放大成空响应"的语义一致
// （下游按 Content-Encoding 自行处理）。
func decodeGrokResponseEncoding(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	encoding := strings.TrimSpace(resp.Header.Get("Content-Encoding"))
	if encoding == "" {
		return
	}
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "event-stream") {
		return
	}
	// 多读 1 字节用于判断是否已超限：读满即说明压缩体比上限还大。
	data, err := io.ReadAll(io.LimitReader(resp.Body, grokMaxCompressedBody+1))
	_ = resp.Body.Close()
	if err != nil || len(data) > grokMaxCompressedBody {
		if len(data) > grokMaxCompressedBody {
			log.Printf("Grok 非流式响应压缩体超过 %dMB，跳过解压原样透传", grokMaxCompressedBody>>20)
		}
		resp.Body = io.NopCloser(bytes.NewReader(data))
		return
	}
	decoded, derr := decodeContentEncoding(data, encoding)
	if derr != nil {
		// 解码失败时原样返回，避免把问题放大成空响应。
		resp.Body = io.NopCloser(bytes.NewReader(data))
		return
	}
	resp.Body = io.NopCloser(bytes.NewReader(decoded))
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("Content-Length")
	resp.ContentLength = int64(len(decoded))
	resp.Uncompressed = true
}

// decodeContentEncoding 按 Content-Encoding（可能是逗号分隔的多重编码）逆序解压 gzip/br/deflate。
// 每一层的解压产物都受 grokMaxDecodedBody 约束，避免高压缩比内容（或多层嵌套压缩）
// 把有限的压缩体放大成不受控的内存占用。
func decodeContentEncoding(data []byte, encoding string) ([]byte, error) {
	encs := strings.Split(encoding, ",")
	for i := len(encs) - 1; i >= 0; i-- {
		enc := strings.ToLower(strings.TrimSpace(encs[i]))
		var reader io.ReadCloser
		switch enc {
		case "", "identity":
			continue
		case "gzip", "x-gzip":
			gr, err := gzip.NewReader(bytes.NewReader(data))
			if err != nil {
				return nil, err
			}
			reader = gr
		case "br":
			reader = io.NopCloser(brotli.NewReader(bytes.NewReader(data)))
		case "deflate":
			// HTTP "deflate" 规范上是 zlib 包装，但不少服务端发裸 deflate；先 zlib 后回退。
			if zr, err := zlib.NewReader(bytes.NewReader(data)); err == nil {
				reader = zr
			} else {
				reader = flate.NewReader(bytes.NewReader(data))
			}
		default:
			return nil, fmt.Errorf("unsupported content-encoding %q", enc)
		}
		out, err := readAllLimited(reader, grokMaxDecodedBody)
		_ = reader.Close()
		if err != nil {
			return nil, err
		}
		data = out
	}
	return data, nil
}

// readAllLimited 读满 limit 字节即报错，避免解压产物无上限膨胀。
func readAllLimited(reader io.Reader, limit int64) ([]byte, error) {
	// 多读 1 字节：读到 limit+1 说明真实内容已超限，而非恰好等于上限。
	out, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(out)) > limit {
		return nil, fmt.Errorf("decoded body exceeds %d bytes", limit)
	}
	return out, nil
}
