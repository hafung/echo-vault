package main

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func mustJSON(value M) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func TestLocalOpenConfirmationAndRequestGuards(t *testing.T) {
	target := filepath.Join(t.TempDir(), "notes.md")
	check(t, os.WriteFile(target, []byte("notes"), 0600))
	var opened []string
	app := &App{openLocal: func(_ context.Context, path string) error {
		opened = append(opened, path)
		return nil
	}}
	for _, body := range []M{
		{"path": target, "action": "open"},
		{"path": target, "action": "open", "confirmed": "true"},
		{"path": target, "action": "invalid", "confirmed": true},
	} {
		res := backupRequest(t, app, "POST", "/api/local/open", mustJSON(body))
		if res.Code != http.StatusBadRequest {
			t.Fatalf("invalid open accepted: %d %s", res.Code, res.Body)
		}
	}
	body := mustJSON(M{"path": target, "action": "open", "confirmed": true})
	for _, origin := range []string{"", "https://evil.example"} {
		req := httptest.NewRequest("POST", "http://localhost/api/local/open", strings.NewReader(body))
		if origin != "" {
			req.Header.Set("Origin", origin)
			req.Header.Set("X-Echo-Request", "1")
		}
		res := httptest.NewRecorder()
		app.ServeHTTP(res, req)
		if res.Code != http.StatusForbidden {
			t.Fatalf("untrusted request accepted: %d", res.Code)
		}
	}
	if len(opened) != 0 {
		t.Fatal("opened before confirmation")
	}
	for _, action := range []string{"open", "reveal"} {
		res := backupRequest(t, app, "POST", "/api/local/open", mustJSON(M{"path": target, "action": action, "confirmed": true}))
		if res.Code != http.StatusOK {
			t.Fatalf("confirmed open failed: %d %s", res.Code, res.Body)
		}
	}
	resolved, err := filepath.EvalSymlinks(target)
	check(t, err)
	if len(opened) != 2 || opened[0] != resolved || opened[1] != filepath.Dir(resolved) {
		t.Fatalf("wrong destinations: %v", opened)
	}
}

func TestLocalOpenTargets(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "中文 file '$`&;.md")
	check(t, os.WriteFile(file, []byte("document"), 0600))
	if _, err := localOpenTarget(file, "open"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "relative/file.md", "https://example.com/file.md", "file:///tmp/a", "//server/share/file", `\\server\share\file`, dir + "/missing", file + "\x00", file + "\n"} {
		if _, err := localOpenTarget(value, "open"); err == nil {
			t.Fatalf("invalid destination accepted: %q", value)
		}
	}
	for _, ext := range []string{".exe", ".cmd", ".desktop", ".lnk", ".ps1", ".sh"} {
		target := filepath.Join(dir, "program"+ext)
		check(t, os.WriteFile(target, []byte("program"), 0600))
		if _, err := localOpenTarget(target, "open"); err == nil {
			t.Fatalf("executable accepted: %s", target)
		}
		if _, err := localOpenTarget(target, "reveal"); err != nil {
			t.Fatal(err)
		}
	}
	resolved, err := filepath.EvalSymlinks(dir)
	check(t, err)
	for _, action := range []string{"open", "reveal"} {
		got, err := localOpenTarget(dir, action)
		if err != nil || got != resolved {
			t.Fatalf("folder failed: %s %v", got, err)
		}
	}
	link := filepath.Join(dir, "innocent.md")
	if err := os.Symlink(filepath.Join(dir, "program.exe"), link); err == nil {
		if _, err := localOpenTarget(link, "open"); err == nil {
			t.Fatal("executable symlink accepted")
		}
	}
}

func TestWindowsOpenPathIsData(t *testing.T) {
	target := `D:\中文\a '$(calc)` + "`" + `.md`
	script := windowsOpenScript(target)
	if strings.Contains(script, target) || !strings.Contains(script, base64.StdEncoding.EncodeToString([]byte(target))) {
		t.Fatal("path was interpolated as code")
	}
	data, err := base64.StdEncoding.DecodeString(encodedPowerShell(script))
	check(t, err)
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(data[i*2:])
	}
	if string(utf16.Decode(units)) != script {
		t.Fatal("PowerShell encoding mismatch")
	}
}
