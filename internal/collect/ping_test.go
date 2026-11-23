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

func TestExpandScanTargetsIPv6(t *testing.T) {
	v4, v6, warnings := expandScanTargets([]string{"10.0.0.0/30", "2001:db8::/126"})
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(v4) != 2 {
		t.Fatalf("IPv4 hosts = %v, want 2", v4)
	}
	if len(v6) != 4 {
		t.Fatalf("IPv6 hosts = %v, want 4", v6)
	}
	for i, want := range []string{"2001:db8::", "2001:db8::1", "2001:db8::2", "2001:db8::3"} {
		if got := v6[i].String(); got != want {
			t.Errorf("IPv6 host[%d] = %s, want %s", i, got, want)
		}
	}

	v4, v6, warnings = expandScanTargets([]string{"2001:db8::/128"})
	if len(warnings) != 0 || len(v4) != 0 || len(v6) != 1 || v6[0].String() != "2001:db8::" {
		t.Fatalf("expandScanTargets(/128) = %v/%v, warnings %v", v4, v6, warnings)
	}
}

func TestExpandScanTargetsRejectsWideIPv6(t *testing.T) {
	_, v6, warnings := expandScanTargets([]string{"2001:db8::/64"})
	if len(v6) != 0 {
		t.Fatalf("IPv6 hosts = %v, want none", v6)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want one oversized-prefix warning", warnings)
	}
}

func TestExpandScanTargetsCapsAcrossFamilies(t *testing.T) {
	v4, v6, warnings := expandScanTargets([]string{"10.0.0.0/21", "2001:db8::/120"})
	if len(v4)+len(v6) != pingMaxHosts {
		t.Fatalf("hosts = %d, want %d", len(v4)+len(v6), pingMaxHosts)
	}
	if len(v6) != 0 {
		t.Fatalf("IPv6 hosts = %d, want none after the IPv4 range filled the cap", len(v6))
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want a cap warning", warnings)
	}
}
