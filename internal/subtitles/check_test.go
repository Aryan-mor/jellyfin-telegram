package subtitles

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckSubDLAcceptsAnAccountReplyAndHidesTheKey(t *testing.T) {
	var saw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw = r.URL.Query().Get("api_key")
		if saw == "bad" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"status":true}`))
	}))
	defer srv.Close()
	if err := checkSubDL(context.Background(), "good", srv.URL, srv.Client()); err != nil {
		t.Fatal(err)
	}
	if saw != "good" {
		t.Fatalf("sent %q", saw)
	}
	err := checkSubDL(context.Background(), "bad", srv.URL, srv.Client())
	if err == nil || strings.Contains(err.Error(), "bad") {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckGeminiRejectsABadKeyWithoutEchoingIt(t *testing.T) {
	const secret = "AIzaSyREJECTEDKEYVALUE123456"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"API key not valid"}}`))
	}))
	defer srv.Close()
	err := checkGemini(context.Background(), secret, srv.URL)
	if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("err = %v", err)
	}
}
