package promptfilter

import "testing"

func TestEngineCacheIgnoresOperationalGuardPerformanceChanges(t *testing.T) {
	base := DefaultConfig()
	first, err := engineForConfig(base)
	if err != nil {
		t.Fatal(err)
	}

	operational := base
	operational.LogMatches = !base.LogMatches
	operational.Advanced.Guard.Performance.AsyncShadowAuxiliaryEnabled = true
	operational.Advanced.Guard.Performance.ShadowWorkers = 16
	operational.Advanced.Guard.Performance.ShadowQueueSize = 4096
	operational.Advanced.Guard.Performance.ShadowOverflowMode = GuardShadowOverflowSync
	operational.Advanced.Guard.Performance.MaxSegments = 32
	operational.Advanced.Guard.Performance.MaxCurrentUserBytes = 262144
	operational.Advanced.Guard.Performance.MaxAuxiliaryBytes = 16384
	operational.Advanced.Guard.Performance.ScanChunkBytes = 4096
	operational.Advanced.Guard.Performance.ScanOverlapBytes = 256
	operational.Advanced.Guard.Rollout.Enabled = true
	operational.Advanced.Guard.Rollout.Percent = 25
	second, err := engineForConfig(operational)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("operational guard tuning rebuilt the regex engine")
	}
}

func TestEngineCacheChangesWhenDetectionSemanticsChange(t *testing.T) {
	base := DefaultConfig()
	first, err := engineForConfig(base)
	if err != nil {
		t.Fatal(err)
	}

	detection := base
	detection.Advanced.Normalization.DecodeBase64 = !base.Advanced.Normalization.DecodeBase64
	second, err := engineForConfig(detection)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("normalization change reused a stale regex engine")
	}
}

func TestEngineCacheDoesNotRetainOperationalSecrets(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Review.APIKey = "sk-review-sensitive"
	cfg.Advanced.NewAPI.Secret = "newapi-sensitive"
	engine, err := engineForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if engine.cfg.Review.APIKey != "" || engine.cfg.Advanced.NewAPI.Secret != "" {
		t.Fatalf("engine retained operational secrets: review=%q newapi=%q", engine.cfg.Review.APIKey, engine.cfg.Advanced.NewAPI.Secret)
	}
}
