package netbox

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Songxwn/netbox-glean/internal/model"
)

func TestCloudPrefixDescription(t *testing.T) {
	target := model.CloudTarget{Name: "prod", Provider: model.CloudAWS}
	prefix := model.CloudPrefix{
		CIDR:   "10.0.0.0/16",
		Name:   "prod-vpc",
		VPC:    "vpc-1",
		Region: "us-east-1",
		Kind:   model.CloudPrefixVPC,
	}
	got := cloudPrefixDescription(target, prefix)
	for _, want := range []string{"netbox-glean", "cloud", "AWS", "prod", "vpc", "prod-vpc", "vpc=vpc-1", "us-east-1"} {
		if !strings.Contains(got, want) {
			t.Errorf("description %q missing %q", got, want)
		}
	}
}

func TestEnsurePrefix(t *testing.T) {
	var posted []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			q := r.URL.Query().Get("q")
			switch q {
			case "10.0.0.0/16":
				// NetBox may return a differently-shaped prefix from a search.
				fmt.Fprint(w, `{"count":1,"next":null,"results":[{"id":1,"prefix":"10.0.0.0/16"}]}`)
			case "10.1.0.0/16":
				fmt.Fprint(w, `{"count":1,"next":null,"results":[{"id":2,"prefix":"10.0.0.0/8"}]}`)
			default:
				fmt.Fprint(w, `{"count":0,"next":null,"results":[]}`)
			}
		case http.MethodPost:
			posted = append(posted, r.URL.Query().Get("q"))
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"id":3}`)
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	client, err := New(Config{URL: server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	created, err := client.ensurePrefix(context.Background(), "10.0.0.0/16", "existing", 0)
	if err != nil || created {
		t.Fatalf("ensure existing prefix = %v, %v", created, err)
	}

	created, err = client.ensurePrefix(context.Background(), "10.1.0.0/16", "new", 0)
	if err != nil || !created {
		t.Fatalf("ensure new prefix = %v, %v", created, err)
	}
	if len(posted) != 1 {
		t.Fatalf("posted = %#v, want one POST", posted)
	}

	if _, err := client.ensurePrefix(context.Background(), "", "empty", 0); err == nil {
		t.Fatal("expected error for empty CIDR")
	}
}

func TestEnsurePrefixDuplicateIsNotAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"count":0,"next":null,"results":[]}`)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"prefix":["Duplicate prefix found."]}`)
	}))
	defer server.Close()

	client, err := New(Config{URL: server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	created, err := client.ensurePrefix(context.Background(), "10.2.0.0/16", "duplicate", 0)
	if err != nil {
		t.Fatalf("ensurePrefix: %v", err)
	}
	if created {
		t.Fatal("duplicate prefix should not be reported as created")
	}
}
