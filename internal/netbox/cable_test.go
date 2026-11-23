package netbox

import "testing"

func TestNormalizeMAC(t *testing.T) {
	cases := map[string]string{
		"AA:BB:CC:DD:EE:FF": "aa:bb:cc:dd:ee:ff",
		"aa-bb-cc-dd-ee-ff": "aa:bb:cc:dd:ee:ff",
		"aabb.ccdd.eeff":    "aa:bb:cc:dd:ee:ff",
		"aabbccddeeff":      "aa:bb:cc:dd:ee:ff",
		"not-a-mac":         "",
		"":                  "",
		"aa:bb:cc:dd:ee":    "",
	}
	for in, want := range cases {
		if got := normalizeMAC(in); got != want {
			t.Errorf("normalizeMAC(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMatchRemoteInterface(t *testing.T) {
	ifaces := []nbInterface{
		{ID: 1, Name: "GigabitEthernet0/1", MACAddress: "AA:BB:CC:DD:EE:01"},
		{ID: 2, Name: "GigabitEthernet0/2", MACAddress: "AA:BB:CC:DD:EE:02"},
	}
	cases := []struct {
		name     string
		port     string
		portDesc string
		wantID   int
	}{
		{name: "by name case insensitive", port: "gigabitethernet0/1", wantID: 1},
		{name: "by description", portDesc: "GigabitEthernet0/2", wantID: 2},
		{name: "by mac", port: "aa-bb-cc-dd-ee-02", wantID: 2},
		{name: "unknown", port: "TenGigabitEthernet1/1", wantID: 0},
		{name: "empty", wantID: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := matchRemoteInterface(ifaces, tc.port, tc.portDesc)
			gotID := 0
			if got != nil {
				gotID = got.ID
			}
			if gotID != tc.wantID {
				t.Fatalf("matched interface %d, want %d", gotID, tc.wantID)
			}
		})
	}
}

func TestInterfaceConnected(t *testing.T) {
	cases := map[string]bool{
		`null`:          false,
		``:              false,
		`{"id": 7}`:     true,
		`5`:             true,
	}
	for raw, want := range cases {
		iface := nbInterface{Cable: []byte(raw)}
		if got := iface.connected(); got != want {
			t.Errorf("connected(%s) = %v, want %v", raw, got, want)
		}
	}
}