package netbox

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Songxwn/netbox-glean/internal/model"
)

func TestFindIPAddressByHost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ipam/ip-addresses/" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		switch r.URL.Query().Get("q") {
		case "10.0.0.5":
			_, _ = fmt.Fprint(w, `{"count":2,"next":null,"results":[
			{"id":7,"address":"10.0.0.5/24","assigned_object_type":null,"assigned_object_id":null},
			{"id":8,"address":"10.0.0.50/32","assigned_object_type":"dcim.interface","assigned_object_id":3}
		]}`)
		case "10.0.0.6":
			_, _ = fmt.Fprint(w, `{"count":0,"next":null,"results":[]}`)
		default:
			t.Errorf("unexpected q %q", r.URL.Query().Get("q"))
		}
	}))
	defer server.Close()

	client, err := New(Config{URL: server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, found, err := client.findIPAddressByHost(context.Background(), "10.0.0.5")
	if err != nil {
		t.Fatalf("findIPAddressByHost: %v", err)
	}
	if !found || got.ID != 7 {
		t.Fatalf("findIPAddressByHost() = %#v, found=%v; want id 7", got, found)
	}
	if _, found, err := client.findIPAddressByHost(context.Background(), "10.0.0.6"); err != nil || found {
		t.Fatalf("findIPAddressByHost(10.0.0.6) found=%v err=%v, want not found", found, err)
	}
}

func TestIPDescription(t *testing.T) {
	got := ipDescription("core-sw-1", model.IPAddress{
		MAC:       "AA:BB:CC:DD:EE:FF",
		Interface: "Gi0/1",
		Source:    "arp",
	})
	want := "netbox-glean via core-sw-1 Gi0/1 AA:BB:CC:DD:EE:FF arp"
	if got != want {
		t.Errorf("ipDescription() = %q, want %q", got, want)
	}
}

func TestIsDuplicateIPError(t *testing.T) {
	duplicate := &APIError{StatusCode: http.StatusBadRequest, Body: `{"address":["Duplicate IP address found."]}`}
	if !isDuplicateIPError(duplicate) {
		t.Error("isDuplicateIPError() = false for duplicate response")
	}
	other := &APIError{StatusCode: http.StatusBadRequest, Body: `{"address":["Invalid address."]}`}
	if isDuplicateIPError(other) {
		t.Error("isDuplicateIPError() = true for unrelated error")
	}
	if isDuplicateIPError(fmt.Errorf("boom")) {
		t.Error("isDuplicateIPError() = true for non API error")
	}
}

func TestUsableNetboxIP(t *testing.T) {
	for _, raw := range []string{"0.0.0.0", "127.0.0.1", "224.0.0.1", "169.254.1.1", "fe80::1"} {
		if usableNetboxIP(net.ParseIP(raw)) {
			t.Errorf("usableNetboxIP(%s) = true, want false", raw)
		}
	}
	if !usableNetboxIP(net.ParseIP("10.0.0.5")) {
		t.Error("usableNetboxIP(10.0.0.5) = false, want true")
	}
}