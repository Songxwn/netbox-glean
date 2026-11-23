package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/Songxwn/netbox-glean/internal/model"
)

func TestStartRecordsTriggerAndActive(t *testing.T) {
	m := New(Options{MaxConcurrent: 1})
	release := make(chan struct{})
	target := model.Target{ID: "t1", Name: "core-1", Protocol: model.ProtocolSNMP}
	id := m.Start(target, model.ModeSync, TriggerSchedule, func(context.Context, model.LogFunc) (*model.Collected, *model.SyncResult, error) {
		<-release
		return &model.Collected{}, nil, nil
	})

	if !m.ActiveFor("t1") {
		t.Error("ActiveFor() = false right after Start")
	}
	if m.ActiveFor("other") {
		t.Error("ActiveFor() = true for an unknown target")
	}
	job, ok := m.Get(id)
	if !ok {
		t.Fatalf("job %s not found", id)
	}
	if job.Trigger != TriggerSchedule {
		t.Errorf("Trigger = %q, want %q", job.Trigger, TriggerSchedule)
	}

	close(release)
	waitForStatus(t, m, id, StatusSuccess)
	if m.ActiveFor("t1") {
		t.Error("ActiveFor() = true after the job finished")
	}
}

func TestTriggerDefaultsToManual(t *testing.T) {
	m := New(Options{})
	id := m.Start(model.Target{ID: "t2", Name: "leaf-1"}, model.ModeCollect, "", func(context.Context, model.LogFunc) (*model.Collected, *model.SyncResult, error) {
		return &model.Collected{}, nil, nil
	})
	waitForStatus(t, m, id, StatusSuccess)
	job, ok := m.Get(id)
	if !ok {
		t.Fatalf("job %s not found", id)
	}
	if job.Trigger != TriggerManual {
		t.Errorf("Trigger = %q, want %q", job.Trigger, TriggerManual)
	}
}

func TestStartScanStoresScanResult(t *testing.T) {
	m := New(Options{})
	id := m.StartScan(Meta{ID: "scan-1", Name: "office", Protocol: model.ProtocolScan}, "", func(context.Context, model.LogFunc) (*model.ScanResult, error) {
		return &model.ScanResult{Scanned: 4, Alive: 2, Added: 1, Existing: 1, Devices: []model.ScannedDevice{{Address: "10.0.0.1", Status: "added"}}}, nil
	})
	waitForStatus(t, m, id, StatusSuccess)
	job, ok := m.Get(id)
	if !ok {
		t.Fatalf("job %s not found", id)
	}
	if job.Mode != model.ModeScan || job.Trigger != TriggerScan {
		t.Errorf("Mode/Trigger = %q/%q", job.Mode, job.Trigger)
	}
	if job.Scan == nil || job.Scan.Added != 1 || len(job.Scan.Devices) != 1 {
		t.Errorf("unexpected scan result: %#v", job.Scan)
	}
	summaries := m.List(10)
	if len(summaries) != 1 || summaries[0].Found != 2 || summaries[0].Added != 1 {
		t.Errorf("unexpected summary: %#v", summaries)
	}
}

func waitForStatus(t *testing.T, m *Manager, id, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if job, ok := m.Get(id); ok && job.Status == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s did not reach status %q", id, want)
}