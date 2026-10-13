package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLauncherOnlyReusesEchoVaultService(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
		expected   bool
	}{
		{"echo-vault", `{"app":"echo-vault"}`, 200, true},
		{"other app", `{"app":"other"}`, 200, false},
		{"html", `<html>another local service</html>`, 200, false},
		{"unavailable", `{"app":"echo-vault"}`, 503, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/health" {
					t.Errorf("unexpected probe path %s", r.URL.Path)
				}
				w.WriteHeader(test.status)
				w.Write([]byte(test.body))
			}))
			defer server.Close()
			if echoVaultRunning(server.URL) != test.expected {
				t.Fatal("incorrect service detection")
			}
		})
	}
}
