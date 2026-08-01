package admin

import (
	"bufio"
	"context"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/codex2api/proxy"
	"github.com/gin-gonic/gin"
)

type memStatsReader func(*runtime.MemStats)

type cpuSampler struct {
	mu        sync.Mutex
	lastTotal uint64
	lastIdle  uint64
	hasLast   bool
}

func newCPUSampler() *cpuSampler {
	return &cpuSampler{}
}

func (s *cpuSampler) Sample() float64 {
	if runtime.GOOS != "linux" {
		return 0
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	total, idle, err := readLinuxCPUTicks()
	if err != nil {
		return 0
	}

	if !s.hasLast {
		s.lastTotal = total
		s.lastIdle = idle
		s.hasLast = true

		time.Sleep(120 * time.Millisecond)
		total, idle, err = readLinuxCPUTicks()
		if err != nil {
			return 0
		}
	}

	totalDelta := total - s.lastTotal
	idleDelta := idle - s.lastIdle
	s.lastTotal = total
	s.lastIdle = idle

	if totalDelta == 0 {
		return 0
	}

	busy := float64(totalDelta-idleDelta) / float64(totalDelta) * 100
	if busy < 0 {
		return 0
	}
	if busy > 100 {
		return 100
	}
	return busy
}

func readLinuxCPUTicks() (uint64, uint64, error) {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return 0, 0, scanner.Err()
	}

	fields := strings.Fields(scanner.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, nil
	}

	var total uint64
	for _, field := range fields[1:] {
		v, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return 0, 0, err
		}
		total += v
	}

	idle, err := strconv.ParseUint(fields[4], 10, 64)
	if err != nil {
		return 0, 0, err
	}

	return total, idle, nil
}

func readSystemMemory() (usedBytes uint64, totalBytes uint64, percent float64) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	return readSystemMemoryWithFallback(&mem)
}

func readSystemMemoryWithFallback(mem *runtime.MemStats) (usedBytes uint64, totalBytes uint64, percent float64) {
	if runtime.GOOS == "linux" {
		file, err := os.Open("/proc/meminfo")
		if err == nil {
			defer file.Close()

			var totalKB uint64
			var availableKB uint64
			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				line := scanner.Text()
				if strings.HasPrefix(line, "MemTotal:") {
					totalKB = parseMeminfoKB(line)
				}
				if strings.HasPrefix(line, "MemAvailable:") {
					availableKB = parseMeminfoKB(line)
				}
			}
			if totalKB > 0 {
				totalBytes = totalKB * 1024
				usedBytes = (totalKB - availableKB) * 1024
				percent = float64(usedBytes) / float64(totalBytes) * 100
				return usedBytes, totalBytes, percent
			}
		}
	}

	if mem == nil {
		return 0, 0, 0
	}
	usedBytes = mem.Alloc
	totalBytes = mem.Sys
	if totalBytes > 0 {
		percent = float64(usedBytes) / float64(totalBytes) * 100
	}

	return usedBytes, totalBytes, percent
}

func parseMeminfoKB(line string) uint64 {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0
	}

	v, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0
	}

	return v
}

func parseLinuxProcessRSS(r io.Reader) (uint64, bool) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		if rssKB := parseMeminfoKB(line); rssKB > 0 {
			return rssKB * 1024, true
		}
		return 0, false
	}
	return 0, false
}

// readProcessMemory 返回当前进程实际驻留在物理内存中的字节数。
//
// runtime.MemStats.Sys 是 Go 运行时从 OS 申请/保留过的虚拟内存总量，包含已空闲
// 甚至已归还的堆页，流量峰值过后仍会保持高水位，不能当成当前进程内存展示。
// Linux/Docker 使用 /proc/self/status 的 VmRSS；非 Linux 才回退到 Sys。
func readProcessMemory() uint64 {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	return readProcessMemoryWithFallback(&mem)
}

func readProcessMemoryWithFallback(mem *runtime.MemStats) uint64 {
	if runtime.GOOS == "linux" {
		file, err := os.Open("/proc/self/status")
		if err == nil {
			defer file.Close()
			if rss, ok := parseLinuxProcessRSS(file); ok {
				return rss
			}
		}
	}

	if mem == nil {
		return 0
	}
	return mem.Sys
}

func collectOpsMemory(readMemStats memStatsReader) opsMemoryResponse {
	if readMemStats == nil {
		readMemStats = runtime.ReadMemStats
	}
	var mem runtime.MemStats
	readMemStats(&mem)
	usedBytes, totalBytes, percent := readSystemMemoryWithFallback(&mem)
	return opsMemoryResponse{
		Percent:           percent,
		UsedBytes:         usedBytes,
		TotalBytes:        totalBytes,
		ProcessBytes:      readProcessMemoryWithFallback(&mem),
		HeapAllocBytes:    mem.HeapAlloc,
		HeapInuseBytes:    mem.HeapInuse,
		HeapReleasedBytes: mem.HeapReleased,
		NumGC:             mem.NumGC,
	}
}

func responseCacheConfigOpsResponse(config proxy.ResponseCacheAppliedConfig) opsResponseCacheConfig {
	return opsResponseCacheConfig{
		Generation:          config.Generation,
		LocalMaxBytes:       config.LocalMaxBytes,
		LocalMaxEntryBytes:  config.LocalMaxEntryBytes,
		ReconstructMaxBytes: config.ReconstructMaxBytes,
	}
}

func responseCacheOpsResponseFromSnapshot(snapshot proxy.ResponseCacheOpsSnapshot) opsResponseCache {
	lastSyncAt := ""
	if !snapshot.LastConfigSyncAt.IsZero() {
		lastSyncAt = snapshot.LastConfigSyncAt.Format(time.RFC3339Nano)
	}
	return opsResponseCache{
		EffectiveConfig:        responseCacheConfigOpsResponse(snapshot.EffectiveConfig),
		AppliedConfig:          responseCacheConfigOpsResponse(snapshot.AppliedConfig),
		Entries:                snapshot.Stats.Entries,
		MaxEntries:             snapshot.EffectiveConfig.MaxEntries,
		CurrentBytes:           snapshot.Stats.Bytes,
		MaxBytes:               snapshot.EffectiveConfig.LocalMaxBytes,
		HighWaterBytes:         snapshot.Stats.HighWaterBytes,
		LargestEntryBytes:      snapshot.Stats.LargestSeenEntryBytes,
		LocalHits:              snapshot.Stats.LocalHits,
		LocalMisses:            snapshot.Stats.LocalMisses,
		RemoteHits:             snapshot.Stats.RemoteHits,
		RemoteMisses:           snapshot.Stats.RemoteMisses,
		Expirations:            snapshot.Stats.Expirations,
		CountEvictions:         snapshot.Stats.CountEvictions,
		ByteEvictions:          snapshot.Stats.ByteEvictions,
		OversizeBypasses:       snapshot.Stats.OversizeBypasses,
		OversizeRejections:     snapshot.Stats.OversizeRejections,
		KnownUnavailableErrors: snapshot.Stats.KnownUnavailableErrors,
		LastConfigSyncAt:       lastSyncAt,
		LastConfigSyncError:    snapshot.LastConfigSyncError,
	}
}

// GetOpsOverview 获取系统运维概览
func (h *Handler) GetOpsOverview(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	usageStats, err := h.getUsageStatsCached(ctx, time.Time{}, time.Time{}, "")
	if err != nil {
		writeInternalError(c, err)
		return
	}

	trafficSnapshot, err := h.db.GetTrafficSnapshot(ctx)
	if err != nil {
		writeInternalError(c, err)
		return
	}

	dbHealthy := h.db.Ping(ctx) == nil
	dbStats := h.db.Stats()
	dbUsage := 0.0
	if dbStats.MaxOpenConnections > 0 {
		dbUsage = float64(dbStats.OpenConnections) / float64(dbStats.MaxOpenConnections) * 100
	}

	redisHealthy := h.cache != nil && h.cache.Ping(ctx) == nil
	var redisTotal uint32
	var redisIdle uint32
	var redisStale uint32
	var redisPoolSize int
	var redisUsage float64
	if h.cache != nil {
		poolStats := h.cache.Stats()
		redisTotal = poolStats.TotalConns
		redisIdle = poolStats.IdleConns
		redisStale = poolStats.StaleConns
		redisPoolSize = h.cache.PoolSize()

		activeRedis := int(redisTotal) - int(redisIdle) - int(redisStale)
		if activeRedis < 0 {
			activeRedis = 0
		}
		if redisPoolSize > 0 {
			redisUsage = float64(activeRedis) / float64(redisPoolSize) * 100
		}
	}

	memorySnapshot := collectOpsMemory(h.memReader)
	cpuPercent := h.cpuSampler.Sample()
	responseCacheSnapshot := proxy.GetResponseCacheOpsSnapshot()

	var activeRequests int64
	var totalRuntimeRequests int64
	for _, acc := range h.store.Accounts() {
		activeRequests += acc.GetActiveRequests()
		totalRuntimeRequests += acc.GetTotalRequests()
	}

	c.JSON(200, opsOverviewResponse{
		UpdatedAt:      time.Now().Format(time.RFC3339),
		UptimeSeconds:  int64(time.Since(h.startedAt).Seconds()),
		DatabaseDriver: h.databaseDriver,
		DatabaseLabel:  h.databaseLabel,
		CacheDriver:    h.cacheDriver,
		CacheLabel:     h.cacheLabel,
		CPU: opsCPUResponse{
			Percent: cpuPercent,
			Cores:   runtime.NumCPU(),
		},
		Memory: memorySnapshot,
		Runtime: opsRuntimeResponse{
			Goroutines:        runtime.NumGoroutine(),
			AvailableAccounts: h.store.AvailableCount(),
			TotalAccounts:     h.store.AccountCount(),
		},
		Requests: opsRequestsResponse{
			Active: activeRequests,
			Total:  totalRuntimeRequests,
		},
		Postgres: opsDatabaseResponse{
			Healthy:      dbHealthy,
			Open:         dbStats.OpenConnections,
			InUse:        dbStats.InUse,
			Idle:         dbStats.Idle,
			MaxOpen:      dbStats.MaxOpenConnections,
			WaitCount:    dbStats.WaitCount,
			UsagePercent: dbUsage,
		},
		Redis: opsRedisResponse{
			Healthy:      redisHealthy,
			TotalConns:   redisTotal,
			IdleConns:    redisIdle,
			StaleConns:   redisStale,
			PoolSize:     redisPoolSize,
			UsagePercent: redisUsage,
		},
		Traffic: opsTrafficResponse{
			QPS:           trafficSnapshot.QPS,
			QPSPeak:       trafficSnapshot.QPSPeak,
			TPS:           trafficSnapshot.TPS,
			TPSPeak:       trafficSnapshot.TPSPeak,
			RPM:           usageStats.RPM,
			TPM:           usageStats.TPM,
			ErrorRate:     usageStats.ErrorRate,
			TodayRequests: usageStats.TodayRequests,
			TodayTokens:   usageStats.TodayTokens,
			RPMLimit:      h.rateLimiter.GetRPM(),
			AvgDurationMs: usageStats.AvgDurationMs,
		},
		ResponseCache: responseCacheOpsResponseFromSnapshot(responseCacheSnapshot),
	})
}
