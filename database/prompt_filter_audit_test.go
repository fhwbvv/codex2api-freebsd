package database

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

func TestPromptFilterAuditQueueReservesHighPriorityCapacity(t *testing.T) {
	queue := newPromptFilterAuditQueue(&DB{})
	for index := 0; index < promptFilterAuditLowCapacity; index++ {
		if !queue.enqueue(PromptFilterLogInput{Source: "low"}, PromptFilterLogPriorityLow) {
			t.Fatalf("low priority enqueue %d failed before its dedicated capacity", index)
		}
	}
	if queue.enqueue(PromptFilterLogInput{Source: "overflow"}, PromptFilterLogPriorityLow) {
		t.Fatal("low priority queue accepted an item beyond its bounded capacity")
	}
	if !queue.enqueue(PromptFilterLogInput{Source: "block", Action: "block"}, PromptFilterLogPriorityHigh) {
		t.Fatal("low priority saturation consumed reserved high priority capacity")
	}
	if got := queue.droppedLow.Load(); got != 1 {
		t.Fatalf("dropped low = %d, want 1", got)
	}
}

func TestEnqueuePromptFilterLogPersistsAsynchronously(t *testing.T) {
	db, err := New("sqlite", filepath.Join(t.TempDir(), "codex2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	input := &PromptFilterLogInput{Source: "local_filter", Endpoint: "/v1/responses", Action: "block", TextPreview: "bounded prompt"}
	if !db.EnqueuePromptFilterLog(input, PromptFilterLogPriorityHigh) {
		t.Fatal("audit enqueue failed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !db.WaitPromptFilterAuditIdle(ctx) {
		t.Fatal("audit queue did not become idle")
	}
	logs, err := db.ListPromptFilterLogs(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].Endpoint != "/v1/responses" || logs[0].TextPreview != "bounded prompt" {
		t.Fatalf("persisted audit logs = %+v", logs)
	}
}

func TestPromptFilterAuditQueueDropsOversizedEvidence(t *testing.T) {
	queue := newPromptFilterAuditQueue(&DB{})
	input := PromptFilterLogInput{FullText: strings.Repeat("x", promptFilterAuditMaxJobBytes+1)}
	if queue.enqueue(input, PromptFilterLogPriorityHigh) {
		t.Fatal("oversized audit evidence entered the queue")
	}
	if got := queue.droppedHigh.Load(); got != 1 {
		t.Fatalf("dropped high = %d, want 1", got)
	}
}

func TestPromptFilterAuditQueueReservesHighPriorityBytes(t *testing.T) {
	queue := newPromptFilterAuditQueue(&DB{})
	input := PromptFilterLogInput{FullText: strings.Repeat("x", 128*1024)}
	for queue.enqueue(input, PromptFilterLogPriorityLow) {
	}
	if got := queue.retainedLow.Load(); got > promptFilterAuditMaxLowBytes {
		t.Fatalf("retained low-priority bytes = %d, max = %d", got, promptFilterAuditMaxLowBytes)
	}
	if !queue.enqueue(PromptFilterLogInput{Source: "block", FullText: strings.Repeat("y", 128*1024)}, PromptFilterLogPriorityHigh) {
		t.Fatal("low-priority byte saturation consumed the high-priority byte reserve")
	}
}

func TestPromptFilterAuditQueueOwnsQueuedStrings(t *testing.T) {
	queue := newPromptFilterAuditQueue(&DB{})
	backing := strings.Repeat("x", 4*1024*1024)
	preview := backing[:32]
	if !queue.enqueue(PromptFilterLogInput{TextPreview: preview}, PromptFilterLogPriorityLow) {
		t.Fatal("enqueue failed")
	}
	job := <-queue.low
	if unsafe.StringData(job.input.TextPreview) == unsafe.StringData(preview) {
		t.Fatal("queued preview retained the caller's backing allocation")
	}
}

func TestPromptFilterAuditQueueCloseRejectsConcurrentEnqueue(t *testing.T) {
	db, err := New("sqlite", filepath.Join(t.TempDir(), "codex2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	queue := db.promptFilterAudit

	var stop atomic.Bool
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for !stop.Load() {
				_ = queue.enqueue(PromptFilterLogInput{Source: "racing"}, PromptFilterLogPriorityLow)
			}
		}()
	}
	time.Sleep(10 * time.Millisecond)
	queue.close(2 * time.Second)
	stop.Store(true)
	workers.Wait()
	if queue.enqueue(PromptFilterLogInput{Source: "closed"}, PromptFilterLogPriorityHigh) {
		t.Fatal("closed queue accepted an audit record")
	}
	if queue.pending.Load() != 0 || queue.retainedHigh.Load() != 0 || queue.retainedLow.Load() != 0 {
		t.Fatalf("closed queue retained work: pending=%d high_bytes=%d low_bytes=%d", queue.pending.Load(), queue.retainedHigh.Load(), queue.retainedLow.Load())
	}
	if queue.droppedHigh.Load() == 0 {
		t.Fatal("closed-queue rejection was not counted as a high-priority drop")
	}
}
