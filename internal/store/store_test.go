package store

import (
	"os"
	"path/filepath"
	"strings"
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
	if !s.Settings().SyncIPAddresses {
		t.Error("missing sync_ip_addresses should default to true")
	}
}

func TestSettingsSyncIPAddressesExplicitFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"sync_ip_addresses":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if s.Settings().SyncIPAddresses {
		t.Error("explicit sync_ip_addresses=false must be preserved")
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
	if !settings.SyncInterfaces || !settings.SyncCables || !settings.SyncIPAddresses {
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
func TestIPTargetPersistence(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	saved, err := s.SaveIPTarget(model.IPTarget{
		Name:    "office sweep",
		Enabled: true,
		Subnets: "10.0.0.0/24",
	})
	if err != nil {
		t.Fatalf("SaveIPTarget: %v", err)
	}
	at := time.Date(2026, 11, 23, 6, 0, 0, 0, time.UTC)
	if err := s.MarkIPTargetRun(saved.ID, at); err != nil {
		t.Fatalf("MarkIPTargetRun: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	target, ok := reopened.IPTarget(saved.ID)
	if !ok {
		t.Fatalf("IP target %s not found after reopen", saved.ID)
	}
	if target.Subnets != "10.0.0.0/24" || target.LastRunAt == nil || !target.LastRunAt.Equal(at) {
		t.Fatalf("unexpected IP target: %#v", target)
	}
	if found, err := reopened.DeleteIPTarget(saved.ID); err != nil || !found {
		t.Fatalf("DeleteIPTarget = %v, %v", found, err)
	}
}

func TestCloudTargetPersistence(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	saved, err := s.SaveCloudTarget(model.CloudTarget{
		Name:             "prod aliyun",
		Provider:         model.CloudAliyun,
		Region:           "cn-hangzhou",
		AccessKey:        "ak",
		SecretKey:        "sk",
		CollectPrefixes:  true,
		CollectInstances: true,
	})
	if err != nil {
		t.Fatalf("SaveCloudTarget: %v", err)
	}
	at := time.Date(2026, 11, 23, 7, 0, 0, 0, time.UTC)
	if err := s.MarkCloudTargetRun(saved.ID, at); err != nil {
		t.Fatalf("MarkCloudTargetRun: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	target, ok := reopened.CloudTarget(saved.ID)
	if !ok {
		t.Fatalf("cloud target %s not found after reopen", saved.ID)
	}
	if target.Provider != model.CloudAliyun || target.Region != "cn-hangzhou" {
		t.Fatalf("unexpected cloud target: %#v", target)
	}
	if target.LastRunAt == nil || !target.LastRunAt.Equal(at) {
		t.Fatalf("LastRunAt = %v, want %v", target.LastRunAt, at)
	}
	if len(reopened.CloudTargets()) != 1 {
		t.Errorf("CloudTargets = %#v", reopened.CloudTargets())
	}
	if found, err := reopened.DeleteCloudTarget(saved.ID); err != nil || !found {
		t.Fatalf("DeleteCloudTarget = %v, %v", found, err)
	}
}

func TestSettingsSyncCloudAssetsDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"netbox_url":"https://netbox.example.com"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !s.Settings().SyncCloudAssets {
		t.Error("missing sync_cloud_assets should default to true")
	}

	path = filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"sync_cloud_assets":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if s.Settings().SyncCloudAssets {
		t.Error("explicit sync_cloud_assets=false must be preserved")
	}
}

func TestLegacyIPDiscoveryMigration(t *testing.T) {
	dir := t.TempDir()
	legacy := `[{
		"id":"t1","name":"core-1","protocol":"snmp","address":"10.0.0.1","port":161,
		"enabled":true,"discover_ips":true,"scan_subnets":"10.0.0.0/24",
		"snmp":{"version":"v2c","community":"public"},
		"schedule_enabled":true,"schedule_interval_minutes":30
	}]`
	if err := os.WriteFile(filepath.Join(dir, "targets.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	target, ok := s.IPTarget("ip-t1")
	if !ok {
		t.Fatal("legacy discover_ips target was not migrated")
	}
	if target.Address != "10.0.0.1" || target.Subnets != "10.0.0.0/24" || !target.ScheduleEnabled {
		t.Fatalf("unexpected migrated target: %#v", target)
	}
	data, err := os.ReadFile(filepath.Join(dir, "targets.json"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, key := range []string{"discover_ips", "scan_subnets"} {
		if strings.Contains(content, key) {
			t.Fatalf("targets.json still contains %s: %s", key, content)
		}
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := len(reopened.IPTargets()); got != 1 {
		t.Fatalf("migration duplicated IP targets: %d", got)
	}
}
