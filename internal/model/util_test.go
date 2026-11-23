package model

import "testing"

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