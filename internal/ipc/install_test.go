package ipc

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"appholder/internal/manager"
	"appholder/internal/update"
)

func TestBackgroundInstallAndRollback(t *testing.T) {
	root := t.TempDir()
	apps := filepath.Join(root, "apps")
	if err := os.MkdirAll(apps, 0755); err != nil {
		t.Fatal(err)
	}
	m := &manager.Manager{Config: manager.Config{Directories: []string{apps}}, StateDir: filepath.Join(root, "state"), DesktopDir: filepath.Join(root, "desktop")}
	before := []byte("\x7fELF\x00\x00\x00\x00AI\x02old")
	after := append(append([]byte(nil), before...), []byte("new")...)
	hash := sha1.Sum(after)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/meta.zsync" {
			fmt.Fprintf(w, "zsync: 0.6.2\nLength: %d\nURL: /app\nSHA-1: %s\n\n", len(after), hex.EncodeToString(hash[:]))
			return
		}
		w.Write(after)
	}))
	defer server.Close()
	live := filepath.Join(apps, "Demo.AppImage")
	if err := os.WriteFile(live, before, 0700); err != nil {
		t.Fatal(err)
	}
	app, err := m.Integrate(live)
	if err != nil {
		t.Fatal(err)
	}
	m.Apps[0].UpdateInfo = "zsync|" + server.URL + "/meta.zsync"
	m.Apps[0].Update.Status = "available"
	checker := update.New()
	checker.Client = server.Client()
	s := &Service{Manager: m, Checker: checker, Context: context.Background()}
	request := httptest.NewRequest(http.MethodPost, "/apps/"+app.ID+"/install", nil)
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, request)
	if response.Code != 202 {
		t.Fatal(response.Body.String())
	}
	s.Wait()
	s.jobsMu.Lock()
	operation := s.operations[app.ID]
	s.jobsMu.Unlock()
	s.mu.Lock()
	installed := s.Manager.Apps[0]
	s.mu.Unlock()
	if operation.Status != "completed" || len(installed.Versions) != 1 {
		t.Fatalf("%+v %+v", operation, installed)
	}
	bytes, _ := os.ReadFile(live)
	if string(bytes) != string(after) {
		t.Fatal("update did not replace live image")
	}
	if err = s.startOperation(context.Background(), app.ID, true); err != nil {
		t.Fatal(err)
	}
	s.Wait()
	bytes, _ = os.ReadFile(live)
	if string(bytes) != string(before) {
		t.Fatal("rollback failed")
	}
	response = httptest.NewRecorder()
	s.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/operations", nil))
	if response.Code != 200 {
		t.Fatal(response.Code)
	}
}
