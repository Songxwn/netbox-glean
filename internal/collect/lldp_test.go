package collect

import "testing"

func TestLLDPLocalPort(t *testing.T) {
	cases := []struct {
		index string
		want  string
		ok    bool
	}{
		{index: "0.12.1", want: "12", ok: true},
		{index: "0.1.1", want: "1", ok: true},
		{index: "5.300.2", want: "300", ok: true},
		{index: "0", ok: false},
		{index: "", ok: false},
		{index: "0.x.1", ok: false},
		{index: "0..1", ok: false},
	}
	for _, tc := range cases {
		got, ok := lldpLocalPort(tc.index)
		if ok != tc.ok || got != tc.want {
			t.Errorf("lldpLocalPort(%q) = (%q, %v), want (%q, %v)", tc.index, got, ok, tc.want, tc.ok)
		}
	}
}

func TestBuildNeighbors(t *testing.T) {
	table := map[string]map[string]string{
		"0.12.1": {
			"port":          "GigabitEthernet0/1",
			"port_desc":     "to core-1",
			"sysname":       "core-1",
			"chassis":       "aa:bb:cc:dd:ee:ff",
			"sysdesc":       "Cisco IOS",
			"port_subtype":  "5",
		},
		// Same local port and remote port behind a different index: deduplicated.
		"0.12.2": {
			"port":     "GigabitEthernet0/1",
			"sysname":  "core-1",
			"chassis":  "aa:bb:cc:dd:ee:ff",
		},
		"0.13.1": {
			"port":    "Gi0/2",
			"sysname": "core-2",
		},
		"0.99.1": {
			"port":    "eth9",
			"sysname": "unknown-local",
		},
		"0.14.1": {
			// No usable remote data at all.
		},
	}
	ifaces := map[string]map[string]string{
		"12": {"name": "eth0", "descr": "uplink to core"},
		"13": {"descr": "eth1"},
	}

	got := buildNeighbors(table, ifaces)
	if len(got) != 3 {
		t.Fatalf("neighbors = %d, want 3: %#v", len(got), got)
	}
	byLocal := map[string]int{}
	for i, n := range got {
		byLocal[n.LocalInterface] = i
	}
	first, ok := byLocal["eth0"]
	if !ok {
		t.Fatalf("missing neighbor for eth0: %#v", got)
	}
	if got[first].RemoteSystem != "core-1" || got[first].RemotePort != "GigabitEthernet0/1" {
		t.Errorf("unexpected eth0 neighbor: %#v", got[first])
	}
	if got[first].RemotePortDesc != "to core-1" {
		t.Errorf("RemotePortDesc = %q", got[first].RemotePortDesc)
	}
	if _, ok := byLocal["eth1"]; !ok {
		t.Errorf("expected ifDescr fallback for ifIndex 13: %#v", got)
	}
	if _, ok := byLocal["ifIndex 99"]; !ok {
		t.Errorf("expected ifIndex fallback for unknown local port: %#v", got)
	}
}

func TestBuildNeighborsEmpty(t *testing.T) {
	if got := buildNeighbors(nil, nil); got != nil {
		t.Fatalf("expected nil, got %#v", got)
	}
}