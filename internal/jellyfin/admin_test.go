package jellyfin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEnsureAdminCreatesAndUpdates(t *testing.T) {
	var have bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/Users" && r.Method == http.MethodGet:
			if !have {
				_ = json.NewEncoder(w).Encode([]any{})
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"Id": "1", "Name": "seerr", "Policy": map[string]any{"IsAdministrator": true},
			}})
		case r.URL.Path == "/Users/New":
			have = true
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["Name"] != "seerr" || body["Password"] != "pw" {
				t.Errorf("create %#v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"Id": "1", "Policy": map[string]any{"IsAdministrator": false}})
		case r.URL.Path == "/Users/1/Password":
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/Users/1/Policy":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["IsAdministrator"] != true {
				t.Errorf("policy %#v", body)
			}
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "key")
	if err := c.EnsureAdmin(context.Background(), "seerr", "pw"); err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureAdmin(context.Background(), "seerr", "pw"); err != nil {
		t.Fatal(err)
	}
}

func TestPublishAddressKeepsOtherSettingsAndUserChoice(t *testing.T) {
	stored := `{"InternalHttpPort":8096,"PublishedServerUriBySubnet":["192.168.0.0/16=http://x:1"],"EnableUPnP":false}`
	var posted map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/System/Configuration/network" {
			t.Errorf("unexpected %s", r.URL.Path)
		}
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(stored))
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&posted)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := New(srv.URL, "k")
	changed, err := c.PublishAddress(context.Background(), "http://192.168.1.5:8097")
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	list, _ := posted["PublishedServerUriBySubnet"].([]any)
	if len(list) != 2 || list[1] != "all=http://192.168.1.5:8097" || posted["InternalHttpPort"] != float64(8096) {
		t.Errorf("posted %#v", posted)
	}
	posted = nil
	stored = `{"PublishedServerUriBySubnet":["all=http://mine:9"]}`
	if changed, err = c.PublishAddress(context.Background(), "http://192.168.1.5:8097"); err != nil || changed || posted != nil {
		t.Errorf("a user's own entry was overwritten: changed=%v err=%v", changed, err)
	}
}
