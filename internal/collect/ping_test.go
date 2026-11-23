package collect

import "testing"

func TestExpandScanCIDRs(t *testing.T) {
	ips, warnings := expandScanCIDRs([]string{"192.168.1.0/30"})
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(ips) != 2 || ips[0].String() != "192.168.1.1" || ips[1].String() != "192.168.1.2" {
		t.Fatalf("expandScanCIDRs(/30) = %v", ips)
	}

	ips, _ = expandScanCIDRs([]string{"192.168.1.0/31"})
	if len(ips) != 2 || ips[0].String() != "192.168.1.0" || ips[1].String() != "192.168.1.1" {
		t.Fatalf("expandScanCIDRs(/31) = %v", ips)
	}

	ips, _ = expandScanCIDRs([]string{"192.168.1.7/32"})
	if len(ips) != 1 || ips[0].String() != "192.168.1.7" {
		t.Fatalf("expandScanCIDRs(/32) = %v", ips)
	}
}

func TestExpandScanCIDRsSkipsInvalid(t *testing.T) {
	ips, warnings := expandScanCIDRs([]string{"not-a-cidr", "2001:db8::/64", "10.0.0.0/32"})
	if len(ips) != 1 || ips[0].String() != "10.0.0.0" {
		t.Fatalf("expandScanCIDRs() = %v", ips)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v, want 2 entries", warnings)
	}
}

func TestExpandScanCIDRsCapsHosts(t *testing.T) {
	ips, warnings := expandScanCIDRs([]string{"10.0.0.0/8"})
	if len(ips) != pingMaxHosts {
		t.Fatalf("expandScanCIDRs(/8) returned %d hosts, want %d", len(ips), pingMaxHosts)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want a cap warning", warnings)
	}
	if ips[0].String() != "10.0.0.1" {
		t.Errorf("first host = %s, want 10.0.0.1", ips[0])
	}
}