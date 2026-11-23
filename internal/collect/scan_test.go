package collect

import (
	"context"
	"testing"

	"github.com/Songxwn/netbox-glean/internal/model"
)

func TestProbeUnsupportedProtocol(t *testing.T) {
	if err := Probe(context.Background(), model.Target{Protocol: "nope", Address: "10.0.0.1"}); err == nil {
		t.Fatal("Probe() expected an error for an unsupported protocol")
	}
}

func TestProbeRedfishEmptyHost(t *testing.T) {
	if err := Probe(context.Background(), model.Target{Protocol: model.ProtocolRedfish}); err == nil {
		t.Fatal("Probe() expected an error for an empty host")
	}
}

func TestTargetHost(t *testing.T) {
	cases := map[string]string{
		"10.0.0.1":                 "10.0.0.1",
		"10.0.0.2:443":             "10.0.0.2",
		"https://10.0.0.3/x":       "10.0.0.3",
		"https://bmc.example.com/": "bmc.example.com",
	}
	for in, want := range cases {
		if got := targetHost(in); got != want {
			t.Errorf("targetHost(%q) = %q, want %q", in, got, want)
		}
	}
}