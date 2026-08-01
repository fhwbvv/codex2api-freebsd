package admin

import (
	"testing"
	"time"
)

// TestGrokBatchImportTimeoutScalesWithFiles 批量导入的整体超时必须跟文件数一起涨：
// 写死常量时每个文件分到的预算会被批量摊薄，5000 个文件只剩 12ms/个，
// 数据库稍慢就会中途超时、剩下的文件全部报 context deadline exceeded。
func TestGrokBatchImportTimeoutScalesWithFiles(t *testing.T) {
	cases := []struct {
		name  string
		files int
		want  time.Duration
	}{
		{"空批次只有基础预算", 0, grokBatchImportBaseTimeout},
		{"负数按 0 处理", -1, grokBatchImportBaseTimeout},
		{"500 个文件", 500, grokBatchImportBaseTimeout + 50*time.Second},
		{"5000 个文件", 5000, grokBatchImportBaseTimeout + 500*time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := grokBatchImportTimeout(tc.files); got != tc.want {
				t.Fatalf("grokBatchImportTimeout(%d) = %v, want %v", tc.files, got, tc.want)
			}
		})
	}
}

// TestGrokBatchImportTimeoutStaysBounded 上限存在的意义：一个超大请求不能无限期占住连接。
func TestGrokBatchImportTimeoutStaysBounded(t *testing.T) {
	if got := grokBatchImportTimeout(grokBatchImportMaxFiles * 10); got != grokBatchImportMaxTimeout {
		t.Fatalf("超大批次超时 = %v, want 封顶 %v", got, grokBatchImportMaxTimeout)
	}
}

// TestGrokBatchImportTimeoutCoversMaxFiles 满额导入不能一进来就撞上封顶，
// 否则每个文件的预算又会被摊薄回去。
func TestGrokBatchImportTimeoutCoversMaxFiles(t *testing.T) {
	got := grokBatchImportTimeout(grokBatchImportMaxFiles)
	if got >= grokBatchImportMaxTimeout {
		t.Fatalf("满额 %d 个文件的超时 %v 已经撞上封顶 %v，每个文件的预算会被压缩",
			grokBatchImportMaxFiles, got, grokBatchImportMaxTimeout)
	}
	if perFile := got / time.Duration(grokBatchImportMaxFiles); perFile < grokBatchImportPerFileTimeout {
		t.Fatalf("满额时每个文件只剩 %v，低于 %v", perFile, grokBatchImportPerFileTimeout)
	}
}
