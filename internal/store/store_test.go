package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Songxwn/netbox-glean/internal/model"
)

func TestSettingsSyncCablesDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	// Older installations have no sync_cables key; it must default to true.
	if err := os.WriteFile(path, []byte(`{"netbox_url":"https://netbox.example.com"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !s.Settings().SyncCables {
		t.Error("missing sync_cables should default to true")
	}
}

func TestSettingsSyncCablesExplicitFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"sync_cables":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if s.Settings().SyncCables {
		t.Error("explicit sync_cables=false must be preserved")
	}
}

func TestSettingsEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	settings := s.Settings()
	if !settings.SyncInterfaces || !settings.SyncCables {
		t.Errorf("defaults not applied: %#v", settings)
	}
}

func TestMarkTargetRunPersists(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	saved, err := s.SaveTarget(model.Target{
		Name:            "core-1",
		Protocol:        model.ProtocolSNMP,
		Address:         "10.0.0.1",
		Enabled:         true,
		ScheduleEnabled: true,
	})
	if err != nil {
		t.Fatalf("SaveTarget: %v", err)
	}
	at := time.Date(2026, 11, 23, 5, 30, 0, 0, time.UTC)
	if err := s.MarkTargetRun(saved.ID, at); err != nil {
		t.Fatalf("MarkTargetRun: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	target, ok := reopened.Target(saved.ID)
	if !ok {
		t.Fatalf("target %s not found after reopen", saved.ID)
	}
	if target.LastRunAt == nil || !target.LastRunAt.Equal(at) {
		t.Fatalf("LastRunAt = %v, want %v", target.LastRunAt, at)
	}
	if err := reopened.MarkTargetRun("missing", at); err != nil {
		t.Fatalf("MarkTargetRun for missing target: %v", err)
	}
}