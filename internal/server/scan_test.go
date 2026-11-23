package server

import (
	"testing"

	"github.com/Songxwn/netbox-glean/internal/model"
)

func TestScanAddress(t *testing.T) {
	cases := map[string]string{
		"10.0.0.1":                      "10.0.0.1",
		" 10.0.0.2:161 ":                "10.0.0.2",
		"https://10.0.0.3/redfish":      "10.0.0.3",
		"https://bmc.example.com:8443/": "bmc.example.com",
	}
	for in, want := range cases {
		if got := scanAddress(in); got != want {
			t.Errorf("scanAddress(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScanTargetName(t *testing.T) {
	req := model.ScanRequest{Protocol: model.ProtocolSNMP, Port: 161, NamePrefix: "office"}
	data := &model.Collected{Identity: model.Identity{Name: "core-sw-1"}}
	if got := scanTargetName(req, "10.0.0.1", data); got != "office-core-sw-1" {
		t.Errorf("scanTargetName() = %q", got)
	}
	// The collected name may be the IP itself; fall back to a host based name.
	data.Identity.Name = "10.0.0.1"
	req.NamePrefix = ""
	if got := scanTargetName(req, "10.0.0.1", data); got != "10-0-0-1" {
		t.Errorf("scanTargetName() fallback = %q", got)
	}
}

func TestUniqueScanName(t *testing.T) {
	used := map[string]bool{"leaf-1": true, "leaf-1-2": true}
	if got := uniqueScanName("leaf-1", used); got != "leaf-1-3" {
		t.Errorf("uniqueScanName() = %q, want leaf-1-3", got)
	}
	if got := uniqueScanName("leaf-2", used); got != "leaf-2" {
		t.Errorf("uniqueScanName() = %q, want leaf-2", got)
	}
}