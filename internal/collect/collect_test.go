package collect

import (
	"testing"

	"github.com/Songxwn/netbox-glean/internal/model"
)

func TestFinishNormalizes(t *testing.T) {
	data := &model.Collected{
		Identity: model.Identity{
			MemoryBytes: 8 * 1024 * 1024 * 1024,
		},
		Interfaces: []model.Interface{
			{Name: " eth0 "},
			{Name: "ETH0"},
			{Name: ""},
			{Name: "eth1"},
		},
		Components: []model.Component{
			{Name: "Fan", Class: "fan"},
			{Name: "Fan", Class: "fan"},
			{Name: "Fan", Class: "module"},
		},
	}
	finish(model.Target{Protocol: model.ProtocolSNMP, Name: " switch-1 "}, data)
	if data.Source != model.ProtocolSNMP {
		t.Errorf("Source = %q", data.Source)
	}
	if data.Identity.Name != "switch-1" {
		t.Errorf("Identity.Name = %q, want switch-1", data.Identity.Name)
	}
	if data.Identity.MemoryGB != 8 {
		t.Errorf("MemoryGB = %v, want 8", data.Identity.MemoryGB)
	}
	if len(data.Interfaces) != 2 {
		t.Fatalf("interfaces = %d, want 2", len(data.Interfaces))
	}
	if data.Interfaces[0].Name != "eth0" || data.Interfaces[1].Name != "eth1" {
		t.Errorf("unexpected interfaces: %#v", data.Interfaces)
	}
	if len(data.Components) != 3 {
		t.Fatalf("components = %d, want 3", len(data.Components))
	}
	if data.Components[1].Name != "Fan #2" {
		t.Errorf("duplicate component name = %q, want Fan #2", data.Components[1].Name)
	}
}

func TestFinishHandlesNil(t *testing.T) {
	finish(model.Target{}, nil)
}