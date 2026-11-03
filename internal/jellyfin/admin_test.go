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
