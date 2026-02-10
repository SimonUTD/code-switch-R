package services

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHealthCheckServiceSaveResult_SkipsWhenLoggingDisabled(t *testing.T) {
	loggingEnabled.Store(false)

	GlobalDBQueue = nil
	hcs := &HealthCheckService{}
	if err := hcs.saveResult(&HealthCheckResult{
		ProviderID:   1,
		ProviderName: "p1",
		Platform:     "claude",
		Status:       HealthStatusOperational,
		CheckedAt:    time.Now(),
	}); err != nil {
		t.Fatalf("expected nil error when logging disabled, got %v", err)
	}
}

func TestSpeedTestServiceUpdateEndpointTestResult_SkipsWhenLoggingDisabled(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	loggingEnabled.Store(false)

	svc := NewSpeedTestService()
	latency := uint64(123)
	if err := svc.UpdateEndpointTestResult("https://api.anthropic.com", &latency); err != nil {
		t.Fatalf("expected nil error when logging disabled, got %v", err)
	}

	if _, err := os.Stat(filepath.Join(tmpHome, ".code-switch", endpointsFileName)); err == nil {
		t.Fatalf("expected speedtest endpoints file to not be created when logging disabled")
	} else if !os.IsNotExist(err) {
		t.Fatalf("unexpected stat error: %v", err)
	}
}

