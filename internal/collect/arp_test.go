package collect

import (
	"net"
	"testing"

	"github.com/Songxwn/netbox-glean/internal/model"
)

func TestParseNeighborIndex(t *testing.T) {
	cases := []struct {
		index   string
		wantIf  int
		wantIP  string
		wantOK  bool
	}{
		{index: "2.10.0.0.5", wantIf: 2, wantIP: "10.0.0.5", wantOK: true},
		{index: "7.4.192.168.1.10", wantIf: 7, wantIP: "192.168.1.10", wantOK: true},
		{index: "2.1.4.10.0.0.5", wantIf: 2, wantIP: "10.0.0.5", wantOK: true},
		{index: "2.1.4.4.10.0.0", wantIf: 2, wantIP: "4.10.0.0", wantOK: true},
		{index: "3.16.32.1.13.184.0.0.0.0.0.0.0.0.0.0.0.1", wantIf: 3, wantIP: "2001:db8::1", wantOK: true},
		{index: "0.10.0.0.1", wantOK: false},
		{index: "2.10.0.0.999", wantOK: false},
		{index: "2.10.0", wantOK: false},
		{index: "", wantOK: false},
	}
	for _, tc := range cases {
		ifIndex, ip, ok := parseNeighborIndex(tc.index)
		if ok != tc.wantOK {
			t.Errorf("parseNeighborIndex(%q) ok = %v, want %v", tc.index, ok, tc.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if ifIndex != tc.wantIf {
			t.Errorf("parseNeighborIndex(%q) ifIndex = %d, want %d", tc.index, ifIndex, tc.wantIf)
		}
		if ip.String() != tc.wantIP {
			t.Errorf("parseNeighborIndex(%q) ip = %s, want %s", tc.index, ip, tc.wantIP)
		}
	}
}

func TestBuildIPAddresses(t *testing.T) {
	mac := string([]byte{0, 17, 34, 51, 68, 85})
	table := map[string]map[string]string{
		"1.10.0.0.5":   {"mac": mac, "net": "10.0.0.5", "type": "3"},
		"1.10.0.0.6":   {"mac": string([]byte{6, 0, 17, 34, 51, 68, 86}), "net": "10.0.0.6", "type": "3"},
		"2.10.0.0.7":   {"mac": mac, "net": "10.0.0.7", "type": "2"},
		"3.10.0.0.8":   {"mac": mac, "net": "224.0.0.1", "type": "3"},
		"4.10.0.0.9":   {"net": "10.0.0.9", "type": "3"},
	}
	ifaces := map[string]map[string]string{
		"1": {"name": "GigabitEthernet0/1"},
		"4": {"descr": "port-4"},
	}

	entries := buildIPAddresses(table, ifaces, false)
	if len(entries) != 3 {
		t.Fatalf("buildIPAddresses() returned %d entries, want 3: %#v", len(entries), entries)
	}
	byIP := map[string]model.IPAddress{}
	for _, entry := range entries {
		byIP[entry.Address] = entry
	}
	if got := byIP["10.0.0.5"]; got.MAC != "00:11:22:33:44:55" || got.Interface != "GigabitEthernet0/1" || got.Source != "arp" {
		t.Errorf("10.0.0.5 entry = %#v", got)
	}
	if got := byIP["10.0.0.6"]; got.MAC != "00:11:22:33:44:56" {
		t.Errorf("10.0.0.6 MAC = %q, want length-prefixed MAC stripped", got.MAC)
	}
	if got := byIP["10.0.0.9"]; got.Interface != "port-4" || got.MAC != "" {
		t.Errorf("10.0.0.9 entry = %#v", got)
	}

	ndp := buildIPAddresses(map[string]map[string]string{
		"1.10.0.0.5": {"mac": mac, "type": "3", "state": "1"},
	}, ifaces, true)
	if len(ndp) != 1 || ndp[0].Source != "ndp" || ndp[0].State != "1" {
		t.Fatalf("buildIPAddresses(ndp) = %#v", ndp)
	}
}

func TestNormalizeIPAddresses(t *testing.T) {
	entries := normalizeIPAddresses([]model.IPAddress{
		{Address: "10.0.0.10", Source: "arp", MAC: "AA:BB:CC:DD:EE:FF", Interface: "Gi0/1"},
		{Address: "10.0.0.10", Source: "icmp"},
		{Address: "10.0.0.9", Source: "icmp"},
		{Address: "127.0.0.1"},
		{Address: "239.1.1.1"},
		{Address: "not-an-ip"},
	})
	if len(entries) != 2 {
		t.Fatalf("normalizeIPAddresses() returned %d entries, want 2: %#v", len(entries), entries)
	}
	if entries[0].Address != "10.0.0.9" || entries[1].Address != "10.0.0.10" {
		t.Fatalf("entries not sorted by address: %#v", entries)
	}
	if entries[1].MAC != "aa:bb:cc:dd:ee:ff" || entries[1].Interface != "Gi0/1" {
		t.Errorf("ARP metadata lost during merge: %#v", entries[1])
	}
}

func TestNeighborMAC(t *testing.T) {
	cases := map[string]string{
		string([]byte{0, 17, 34, 51, 68, 85}):    "00:11:22:33:44:55",
		string([]byte{6, 0, 17, 34, 51, 68, 85}): "00:11:22:33:44:55",
		"AA-BB-CC-DD-EE-FF":                      "aa:bb:cc:dd:ee:ff",
		"":                                       "",
		"nope":                                   "",
	}
	for input, want := range cases {
		if got := neighborMAC(input); got != want {
			t.Errorf("neighborMAC(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestUsableIP(t *testing.T) {
	rejected := []string{"0.0.0.0", "127.0.0.1", "224.0.0.1", "169.254.1.1", "255.255.255.255", "fe80::1", "::1"}
	for _, raw := range rejected {
		if usableIP(net.ParseIP(raw)) {
			t.Errorf("usableIP(%s) = true, want false", raw)
		}
	}
	for _, raw := range []string{"10.0.0.1", "2001:db8::1"} {
		if !usableIP(net.ParseIP(raw)) {
			t.Errorf("usableIP(%s) = false, want true", raw)
		}
	}
}