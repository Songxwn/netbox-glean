package model

import (
	"testing"
	"time"
)

func TestFirst(t *testing.T) {
	if got := First("", "  ", " value "); got != "value" {
		t.Fatalf("First() = %q, want %q", got, "value")
	}
	if got := First("", ""); got != "" {
		t.Fatalf("First() = %q, want empty", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("  hello  ", 3); got != "hel" {
		t.Fatalf("Truncate() = %q, want %q", got, "hel")
	}
	if got := Truncate("你好世界", 2); got != "你好" {
		t.Fatalf("Truncate() = %q, want %q", got, "你好")
	}
	if got := Truncate("abc", 0); got != "abc" {
		t.Fatalf("Truncate() = %q, want %q", got, "abc")
	}
}

func TestDefaultPort(t *testing.T) {
	cases := map[string]int{
		ProtocolSNMP:    161,
		ProtocolIPMI:    623,
		ProtocolRedfish: 443,
		"SNMP":          161,
		"unknown":       0,
	}
	for protocol, want := range cases {
		if got := DefaultPort(protocol); got != want {
			t.Errorf("DefaultPort(%q) = %d, want %d", protocol, got, want)
		}
	}
}

func TestNormalizeTarget(t *testing.T) {
	target := Target{
		Name:     "  core-sw-1 ",
		Protocol: " SNMP ",
		Address:  " 10.0.0.1 ",
	}
	NormalizeTarget(&target)
	if target.Name != "core-sw-1" {
		t.Errorf("Name = %q", target.Name)
	}
	if target.Protocol != ProtocolSNMP {
		t.Errorf("Protocol = %q", target.Protocol)
	}
	if target.Port != 161 {
		t.Errorf("Port = %d, want 161", target.Port)
	}
	if target.SNMP.Version != "v2c" {
		t.Errorf("SNMP.Version = %q, want v2c", target.SNMP.Version)
	}
	if target.IPMI.Interface != "lanplus" {
		t.Errorf("IPMI.Interface = %q, want lanplus", target.IPMI.Interface)
	}
}

func TestNormalizeIPTarget(t *testing.T) {
	target := IPTarget{
		Subnets: "10.0.0.0/24, 10.0.1.0/24 10.0.0.0/24",
		Address: " 10.0.0.2 ",
	}
	NormalizeIPTarget(&target)
	if target.Subnets != "10.0.0.0/24, 10.0.1.0/24" {
		t.Errorf("Subnets = %q", target.Subnets)
	}
	if target.Address != "10.0.0.2" || target.Port != 161 {
		t.Errorf("Address/Port = %q/%d", target.Address, target.Port)
	}
	if target.Name != "10.0.0.2" {
		t.Errorf("Name = %q", target.Name)
	}
	if got := target.ScanCIDRs(); len(got) != 2 {
		t.Errorf("ScanCIDRs() = %v, want 2 entries", got)
	}
}

func TestValidateTarget(t *testing.T) {
	if err := ValidateTarget(Target{Protocol: ProtocolSNMP, Address: "10.0.0.1", Port: 161}); err != nil {
		t.Fatalf("ValidateTarget() unexpected error: %v", err)
	}
	if err := ValidateTarget(Target{Protocol: "nope", Address: "10.0.0.1"}); err == nil {
		t.Fatal("ValidateTarget() expected error for unsupported protocol")
	}
	if err := ValidateTarget(Target{Protocol: ProtocolRedfish}); err == nil {
		t.Fatal("ValidateTarget() expected error for missing address")
	}
	if err := ValidateTarget(Target{Protocol: ProtocolIPMI, Address: "10.0.0.1", Port: 70000}); err == nil {
		t.Fatal("ValidateTarget() expected error for invalid port")
	}
}

func TestValidateIPTarget(t *testing.T) {
	if err := ValidateIPTarget(IPTarget{Subnets: "10.0.0.0/24"}); err != nil {
		t.Fatalf("ValidateIPTarget() unexpected error: %v", err)
	}
	if err := ValidateIPTarget(IPTarget{Address: "10.0.0.2", Port: 161}); err != nil {
		t.Fatalf("ValidateIPTarget() unexpected error for SNMP source: %v", err)
	}
	if err := ValidateIPTarget(IPTarget{}); err == nil {
		t.Fatal("ValidateIPTarget() expected error without subnets or address")
	}
	if err := ValidateIPTarget(IPTarget{Subnets: "10.0.0.0/33"}); err == nil {
		t.Fatal("ValidateIPTarget() expected error for invalid subnet")
	}
	if err := ValidateIPTarget(IPTarget{Subnets: "2001:db8::/33"}); err == nil {
		t.Fatal("ValidateIPTarget() expected error for an invalid IPv6 subnet")
	}
	if err := ValidateIPTarget(IPTarget{Subnets: "2001:db8::/120"}); err != nil {
		t.Fatalf("ValidateIPTarget() unexpected error for IPv6 subnet: %v", err)
	}
	if err := ValidateIPTarget(IPTarget{Subnets: "2001:db8::/64"}); err == nil {
		t.Fatal("ValidateIPTarget() expected error for an oversized IPv6 subnet")
	}
}

func TestValidateScanRequest(t *testing.T) {
	enabled := true
	req := ScanRequest{Subnets: "10.0.0.0/30", Protocol: ProtocolSNMP, Enabled: &enabled}
	NormalizeScanRequest(&req)
	if err := ValidateScanRequest(req); err != nil {
		t.Fatalf("ValidateScanRequest() unexpected error: %v", err)
	}
	if req.Port != 161 || !req.ScanEnabled() {
		t.Errorf("Port/ScanEnabled = %d/%v", req.Port, req.ScanEnabled())
	}
	if err := ValidateScanRequest(ScanRequest{Subnets: "10.0.0.0/24", Protocol: "nope"}); err == nil {
		t.Fatal("ValidateScanRequest() expected error for unsupported protocol")
	}
	if err := ValidateScanRequest(ScanRequest{Protocol: ProtocolSNMP}); err == nil {
		t.Fatal("ValidateScanRequest() expected error for missing subnets")
	}
	if err := ValidateScanRequest(ScanRequest{Subnets: "10.0.0.0/16", Protocol: ProtocolSNMP}); err == nil {
		t.Fatal("ValidateScanRequest() expected error for an oversized scan")
	}
	if got := ScanHostCount([]string{"10.0.0.0/30"}); got != 2 {
		t.Errorf("ScanHostCount(/30) = %d, want 2", got)
	}
}

func TestNormalizeScheduleInterval(t *testing.T) {
	cases := map[int]int{
		0:     DefaultScheduleMinutes,
		-5:    DefaultScheduleMinutes,
		1:     MinScheduleMinutes,
		4:     MinScheduleMinutes,
		5:     5,
		60:    60,
		20000: MaxScheduleMinutes,
	}
	for in, want := range cases {
		target := Target{Protocol: ProtocolSNMP, Address: "10.0.0.1", ScheduleIntervalMinutes: in}
		NormalizeTarget(&target)
		if target.ScheduleIntervalMinutes != want {
			t.Errorf("interval %d normalized to %d, want %d", in, target.ScheduleIntervalMinutes, want)
		}
	}
}

func TestScheduleDue(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-30 * time.Minute)
	old := now.Add(-90 * time.Minute)
	zero := time.Time{}

	cases := []struct {
		name   string
		target Target
		want   bool
	}{
		{name: "enabled target without schedule", target: Target{Enabled: true}, want: false},
		{name: "disabled target with schedule", target: Target{Enabled: false, ScheduleEnabled: true}, want: false},
		{name: "never ran", target: Target{Enabled: true, ScheduleEnabled: true, ScheduleIntervalMinutes: 60}, want: true},
		{name: "zero last run", target: Target{Enabled: true, ScheduleEnabled: true, ScheduleIntervalMinutes: 60, LastRunAt: &zero}, want: true},
		{name: "not due", target: Target{Enabled: true, ScheduleEnabled: true, ScheduleIntervalMinutes: 60, LastRunAt: &recent}, want: false},
		{name: "due", target: Target{Enabled: true, ScheduleEnabled: true, ScheduleIntervalMinutes: 60, LastRunAt: &old}, want: true},
		{name: "short interval due", target: Target{Enabled: true, ScheduleEnabled: true, ScheduleIntervalMinutes: 5, LastRunAt: &recent}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.target.ScheduleDue(now); got != tc.want {
				t.Fatalf("ScheduleDue() = %v, want %v", got, tc.want)
			}
		})
	}
}