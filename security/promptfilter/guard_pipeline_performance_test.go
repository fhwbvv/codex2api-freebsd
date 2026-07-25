package promptfilter

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func benchmarkGuardEnvelope(toolSegments int, segmentBytes int) RequestEnvelope {
	segments := make([]Segment, 0, toolSegments+1)
	segments = append(segments, Segment{
		Origin:   OriginCurrentUser,
		Role:     "user",
		Text:     "请总结刚才的工具执行结果，并继续完成正常的软件开发任务。",
		Sequence: 0,
		Trust:    SegmentTrustClientSupplied,
	})
	line := `{"level":"info","component":"builder","message":"compiled package successfully","files":128,"tests":256}` + "\n"
	for index := 0; index < toolSegments; index++ {
		text := strings.Repeat(line, segmentBytes/len(line)+1)
		text = fmt.Sprintf("tool_call=%d\n%s", index, text[:segmentBytes])
		segments = append(segments, Segment{
			Origin:   OriginToolOutput,
			Role:     "tool",
			Text:     text,
			Sequence: index + 1,
			Trust:    SegmentTrustClientSupplied,
		})
	}
	return RequestEnvelope{
		Endpoint:       "/v1/responses",
		Protocol:       ProtocolResponses,
		Transport:      TransportHTTP,
		RequestedModel: "gpt-5.5",
		EffectiveModel: "gpt-5.5",
		ModelFamily:    ModelFamilyOpenAI,
		Segments:       segments,
	}
}

func benchmarkGuardConfig() Config {
	cfg := RecommendedConfig()
	cfg.Enabled = true
	cfg.Mode = ModeWarn
	cfg.Advanced.Guard.Mode = GuardModeWarn
	cfg.Advanced.Guard.Layers.ToolOutput.Mode = GuardModeShadow
	return NormalizeConfig(cfg)
}

func BenchmarkGuardPipelineRepeatedAuxiliaryContext(b *testing.B) {
	const (
		toolSegments = 8
		segmentBytes = 64 * 1024
	)
	pipeline := NewGuardPipeline()
	request := GuardRequest{
		Envelope: benchmarkGuardEnvelope(toolSegments, segmentBytes),
		Config:   benchmarkGuardConfig(),
	}
	b.ReportAllocs()
	b.SetBytes(toolSegments * segmentBytes)
	b.ResetTimer()
	for range b.N {
		decision := pipeline.Evaluate(context.Background(), request)
		if decision.Action != ActionAllow || decision.Score != 0 {
			b.Fatalf("unexpected decision: %+v", decision)
		}
	}
}

func BenchmarkGuardPipelineCachedSynchronousAuxiliaryContext(b *testing.B) {
	const (
		toolSegments = 8
		segmentBytes = 64 * 1024
	)
	pipeline := NewGuardPipeline()
	cfg := benchmarkGuardConfig()
	cfg.Advanced.Guard.Performance.AsyncShadowAuxiliaryEnabled = false
	request := GuardRequest{
		Envelope: benchmarkGuardEnvelope(toolSegments, segmentBytes),
		Config:   cfg,
	}
	// Warm the exact cache once; the measured path represents subsequent Codex
	// turns that resend the same accumulated tool history.
	decision := pipeline.Evaluate(context.Background(), request)
	if decision.Action != ActionAllow || decision.Score != 0 {
		b.Fatalf("unexpected warmup decision: %+v", decision)
	}
	b.ReportAllocs()
	b.SetBytes(toolSegments * segmentBytes)
	b.ResetTimer()
	for range b.N {
		decision = pipeline.Evaluate(context.Background(), request)
		if decision.Action != ActionAllow || decision.Score != 0 {
			b.Fatalf("unexpected decision: %+v", decision)
		}
	}
}

func BenchmarkGuardPipelineUncachedSynchronousAuxiliaryContext(b *testing.B) {
	const (
		toolSegments = 8
		segmentBytes = 64 * 1024
	)
	pipeline := NewGuardPipeline()
	cfg := benchmarkGuardConfig()
	cfg.Advanced.Guard.Performance.AsyncShadowAuxiliaryEnabled = false
	cfg.Advanced.Guard.Performance.ExactSegmentCacheEnabled = false
	request := GuardRequest{
		Envelope: benchmarkGuardEnvelope(toolSegments, segmentBytes),
		Config:   cfg,
	}
	b.ReportAllocs()
	b.SetBytes(toolSegments * segmentBytes)
	b.ResetTimer()
	for range b.N {
		decision := pipeline.Evaluate(context.Background(), request)
		if decision.Action != ActionAllow || decision.Score != 0 {
			b.Fatalf("unexpected decision: %+v", decision)
		}
	}
}

func BenchmarkGuardPipelineCachedSynchronousAuxiliaryContextParallel(b *testing.B) {
	const (
		toolSegments = 8
		segmentBytes = 64 * 1024
	)
	pipeline := NewGuardPipeline()
	cfg := benchmarkGuardConfig()
	cfg.Advanced.Guard.Performance.AsyncShadowAuxiliaryEnabled = false
	request := GuardRequest{
		Envelope: benchmarkGuardEnvelope(toolSegments, segmentBytes),
		Config:   cfg,
	}
	if decision := pipeline.Evaluate(context.Background(), request); decision.Action != ActionAllow || decision.Score != 0 {
		b.Fatalf("unexpected warmup decision: %+v", decision)
	}
	b.ReportAllocs()
	b.SetBytes(toolSegments * segmentBytes)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			decision := pipeline.Evaluate(context.Background(), request)
			if decision.Action != ActionAllow || decision.Score != 0 {
				b.Fatalf("unexpected decision: %+v", decision)
			}
		}
	})
}
