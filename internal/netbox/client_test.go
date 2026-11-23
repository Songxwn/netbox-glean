package netbox

import "testing"

func TestResolveToken(t *testing.T) {
	cases := []struct {
		name        string
		token       string
		version     string
		wantMode    string
		wantHeader  string
		wantErr     bool
	}{
		{name: "auto v1", token: "abc", version: "auto", wantMode: "1", wantHeader: "Token abc"},
		{name: "auto v1 pasted scheme", token: "Token abc", version: "auto", wantMode: "1", wantHeader: "Token abc"},
		{name: "auto v2", token: "nbt_key.secret", version: "auto", wantMode: "2", wantHeader: "Bearer nbt_key.secret"},
		{name: "auto v2 pasted scheme", token: "Bearer nbt_key.secret", version: "auto", wantMode: "2", wantHeader: "Bearer nbt_key.secret"},
		{name: "auto empty", token: "", version: "", wantMode: "1", wantHeader: ""},
		{name: "force v1", token: "nbt_key.secret", version: "1", wantMode: "1", wantHeader: "Token nbt_key.secret"},
		{name: "force v2", token: "abc", version: "2", wantMode: "2", wantHeader: "Bearer abc"},
		{name: "invalid", token: "abc", version: "3", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, header, err := resolveToken(tc.token, tc.version)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if mode != tc.wantMode || header != tc.wantHeader {
				t.Fatalf("got mode=%q header=%q, want mode=%q header=%q", mode, header, tc.wantMode, tc.wantHeader)
			}
		})
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Cisco Systems, Inc.": "cisco-systems-inc",
		" Dell Inc ":          "dell-inc",
		"a/b c_d":             "a-b-c-d",
		"":                    "item-",
	}
	for input, want := range cases {
		got := slugify(input)
		if want == "item-" {
			if len(got) != len("item-")+8 || got[:5] != "item-" {
				t.Errorf("slugify(%q) = %q, want hashed fallback", input, got)
			}
			continue
		}
		if got != want {
			t.Errorf("slugify(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestManagementIP(t *testing.T) {
	cases := map[string]string{
		"10.0.0.1":                      "10.0.0.1",
		"10.0.0.1:623":                  "10.0.0.1",
		"[2001:db8::1]:443":             "2001:db8::1",
		"https://10.0.0.5/redfish/v1":   "10.0.0.5",
		"https://[2001:db8::5]/redfish": "2001:db8::5",
		"hostname.example.com":          "",
	}
	for input, want := range cases {
		if got := managementIP(input); got != want {
			t.Errorf("managementIP(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestShortHashAndTitleize(t *testing.T) {
	if got := shortHash(""); len(got) != 8 {
		t.Errorf("shortHash() length = %d, want 8", len(got))
	}
	if got := titleize("dc-1"); got != "Dc 1" {
		t.Errorf("titleize() = %q, want %q", got, "Dc 1")
	}
}