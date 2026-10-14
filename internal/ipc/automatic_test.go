package ipc

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"appholder/internal/manager"
	"appholder/internal/update"
)

func autoFixture(t *testing.T, block bool) (*Service, string, []byte, []byte, func()) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "apps")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	before := []byte("\x7fELF\x00\x00\x00\x00AI\x02old")
	after := append(append([]byte(nil), before...), make([]byte, 8192)...)
	hash := sha1.Sum(after)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	if !block {
		unblock()
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/meta" {
			fmt.Fprintf(w, "zsync: 0.6.2\nLength: %d\nURL: /app\nSHA-1: %s\n\n", len(after), hex.EncodeToString(hash[:]))
			return
		}
		w.Write(after[:4096])
		w.(http.Flusher).Flush()
		<-release
		w.Write(after[4096:])
	}))
	m := &manager.Manager{Config: manager.Config{Directories: []string{dir}}, StateDir: filepath.Join(root, "state"), DesktopDir: filepath.Join(root, "desktop")}
	file := filepath.Join(dir, "Demo.AppImage")
	if err := os.WriteFile(file, before, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Integrate(file); err != nil {
		t.Fatal(err)
	}
	m.Apps[0].UpdateInfo = "zsync|" + server.URL + "/meta"
	checker := update.New()
	checker.Client = server.Client()
	s := &Service{Manager: m, Checker: checker}
	t.Cleanup(func() { unblock(); s.Wait(); server.Close() })
	return s, file, before, after, unblock
}

func TestAutomaticUpdatesAfterChecksAndKeepsBackup(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+filepath.Join(t.TempDir(), "no-bus"))
	s, file, _, after, _ := autoFixture(t, false)
	s.Manager.Apps[0].AutoUpdate = true
	if !s.CheckUpdates(context.Background(), true) {
		t.Fatal("check not started")
	}
	s.Wait()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(after) {
		t.Fatal("automatic update not installed")
	}
	a := s.Manager.Apps[0]
	if !a.AutoUpdate || len(a.Versions) != 1 {
		t.Fatal("preference or backup missing")
	}
	data, err = os.ReadFile(filepath.Join(s.Manager.StateDir, "library.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved []manager.App
	if err = json.Unmarshal(data, &saved); err != nil || !saved[0].AutoUpdate {
		t.Fatal("preference not persisted")
	}
}

func TestDisablingAutomaticUpdatesCancelsDownload(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+filepath.Join(t.TempDir(), "no-bus"))
	s, file, before, _, unblock := autoFixture(t, true)
	app := s.Manager.Apps[0]
	s.Manager.Apps[0].AutoUpdate = true
	s.Manager.Apps[0].Update.Status = "available"
	if err := s.startAutomatic(context.Background(), app.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	downloading := false
	for time.Now().Before(deadline) {
		s.jobsMu.Lock()
		done := s.operations[app.ID].Downloaded
		s.jobsMu.Unlock()
		if done > 0 {
			downloading = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !downloading {
		t.Fatal("download did not start")
	}
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/apps/"+app.ID+"/auto-update", bytes.NewReader([]byte(`{"enabled":false}`))))
	if response.Code != 204 {
		t.Fatal(response.Body.String())
	}
	s.Wait()
	unblock()
	data, _ := os.ReadFile(file)
	if string(data) != string(before) || s.Manager.Apps[0].AutoUpdate {
		t.Fatal("cancelled update changed file or preference")
	}
	s.jobsMu.Lock()
	job := s.operations[app.ID]
	s.jobsMu.Unlock()
	if job.Status != "cancelled" {
		t.Fatalf("%+v", job)
	}
}

func TestPinnedOrDisabledCopyCannotAutoUpdate(t *testing.T) {
	s, _, _, _, _ := autoFixture(t, false)
	id := s.Manager.Apps[0].ID
	if err := s.startAutomatic(context.Background(), id); err == nil {
		t.Fatal("disabled copy queued")
	}
	s.Manager.Apps[0].Pinned = true
	s.Manager.Apps[0].Update.Status = "available"
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/apps/"+id+"/auto-update", bytes.NewReader([]byte(`{"enabled":true}`))))
	if response.Code != 409 {
		t.Fatal("enabled auto-update on pinned copy")
	}
	s.Manager.Apps[0].Pinned = false
	s.Manager.Apps[0].AutoUpdate = true
	response = httptest.NewRecorder()
	s.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/apps/"+id+"/pin", bytes.NewReader([]byte(`{"pinned":true}`))))
	if response.Code != 204 || s.Manager.Apps[0].AutoUpdate {
		t.Fatal("pin did not disable auto-update")
	}
}
