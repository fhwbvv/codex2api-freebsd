//go:build race

package database

// raceDetectorEnabled 标记本次测试构建启用了 -race。竞争检测会把 CPU/内存
// 开销放大 5-20 倍,性能界限类断言需要按此放宽时限,而普通构建保持严格。
const raceDetectorEnabled = true
