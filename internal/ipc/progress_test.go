package ipc

import (
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

func TestProgressVisibleBeforeDownloadCompletes(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "apps")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	m := &manager.Manager{Config: manager.Config{Directories: []string{dir}}, StateDir: filepath.Join(root, "state"), DesktopDir: filepath.Join(root, "desktop")}
	before := []byte("\x7fELF\x00\x00\x00\x00AI\x02old")
	after := append(append([]byte(nil), before...), make([]byte, 64*1024)...)
	hash := sha1.Sum(after)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/meta.zsync" {
			fmt.Fprintf(w, "zsync: 0.6.2\nLength: %d\nURL: /app\nSHA-1: %s\n\n", len(after), hex.EncodeToString(hash[:]))
			return
		}
		w.Write(after[:4096])
		w.(http.Flusher).Flush()
		<-release
		w.Write(after[4096:])
	}))
	defer server.Close()
	live := filepath.Join(dir, "Demo.AppImage")
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
	service := &Service{Manager: m, Checker: checker}
	defer func() { unblock(); service.Wait() }()
	if err = service.startOperation(context.Background(), app.ID, false); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	observed := false
	for time.Now().Before(deadline) {
		response := httptest.NewRecorder()
		service.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/operations", nil))
		var jobs map[string]Operation
		if err = json.Unmarshal(response.Body.Bytes(), &jobs); err != nil {
			t.Fatal(err)
		}
		job := jobs[app.ID]
		if job.Downloaded > 0 && job.Downloaded < job.Total && job.Status == "running" {
			observed = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !observed {
		t.Fatal("no download progress exposed before completion")
	}
	// Operation polling must also work while the library is locked for activation.
	service.mu.Lock()
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/operations", nil))
	service.mu.Unlock()
	if response.Code != 200 {
		t.Fatal("progress unavailable during activation")
	}
	unblock()
	service.Wait()
	service.jobsMu.Lock()
	job := service.operations[app.ID]
	service.jobsMu.Unlock()
	if job.Status != "completed" || job.Downloaded != int64(len(after)) || job.Total != int64(len(after)) {
		t.Fatalf("incorrect final progress: %+v", job)
	}
}
