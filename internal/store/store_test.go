package store

import (
	"os"
	"path/filepath"
	"testing"
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