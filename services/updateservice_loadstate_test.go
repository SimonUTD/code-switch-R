package services

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateService_LoadState_ClampsLatestKnownVersionToCurrent(t *testing.T) {
	t.Helper()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "update-state.json")
	state := UpdateState{
		LatestKnownVersion: "v1.3.4",
		UpdateReady:        false,
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.WriteFile(statePath, data, 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}

	us := &UpdateService{
		currentVersion: "v2.8.6",
		stateFile:      statePath,
	}
	if err := us.LoadState(); err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if us.latestVersion != "v2.8.6" {
		t.Fatalf("expected latestVersion to be clamped, got %q", us.latestVersion)
	}

	updated, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read updated state: %v", err)
	}
	var updatedState UpdateState
	if err := json.Unmarshal(updated, &updatedState); err != nil {
		t.Fatalf("unmarshal updated state: %v", err)
	}
	if updatedState.LatestKnownVersion != "v2.8.6" {
		t.Fatalf("expected state.LatestKnownVersion to be updated, got %q", updatedState.LatestKnownVersion)
	}
}

func TestUpdateService_LoadState_DoesNotClampWhenStateIsNewer(t *testing.T) {
	t.Helper()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "update-state.json")
	state := UpdateState{
		LatestKnownVersion: "v2.9.0",
		UpdateReady:        false,
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.WriteFile(statePath, data, 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}

	us := &UpdateService{
		currentVersion: "v2.8.6",
		stateFile:      statePath,
	}
	if err := us.LoadState(); err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if us.latestVersion != "v2.9.0" {
		t.Fatalf("expected latestVersion to remain, got %q", us.latestVersion)
	}
}

