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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"appholder/internal/manager"
	"appholder/internal/update"
)

func TestUpdateAllQueuesLimitsDownloadsAndSkipsPinned(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "apps")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	m := &manager.Manager{Config: manager.Config{Directories: []string{dir}}, StateDir: filepath.Join(root, "state"), DesktopDir: filepath.Join(root, "desktop")}
	old := []byte("\x7fELF\x00\x00\x00\x00AI\x02old")
	next := append(append([]byte(nil), old...), []byte("new")...)
	digest := sha1.Sum(next)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var active, maximum atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/meta" {
			fmt.Fprintf(w, "zsync: 0.6.2\nLength: %d\nURL: /app\nSHA-1: %s\n\n", len(next), hex.EncodeToString(digest[:]))
			return
		}
		count := active.Add(1)
		defer active.Add(-1)
		for count > maximum.Load() {
			previous := maximum.Load()
			if maximum.CompareAndSwap(previous, count) {
				break
			}
		}
		<-release
		w.Write(next)
	}))
	defer server.Close()
	for i := 0; i < 5; i++ {
		file := filepath.Join(dir, fmt.Sprintf("App%d.AppImage", i))
		if err := os.WriteFile(file, old, 0700); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Integrate(file); err != nil {
			t.Fatal(err)
		}
	}
	for i := range m.Apps {
		m.Apps[i].UpdateInfo = "zsync|" + server.URL + "/meta"
		m.Apps[i].Update.Status = "available"
	}
	m.Apps[4].Pinned = true
	checker := update.New()
	checker.Client = server.Client()
	service := &Service{Manager: m, Checker: checker}
	defer func() { unblock(); service.Wait() }()
	result := service.UpdateAll(context.Background())
	if len(result.Started) != 4 || len(result.Skipped) != 1 {
		t.Fatalf("%+v", result)
	}
	again := service.UpdateAll(context.Background())
	if len(again.Started) != 0 || len(again.Skipped) != 5 {
		t.Fatalf("duplicate batch accepted: %+v", again)
	}
	deadline := time.Now().Add(3 * time.Second)
	for active.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if active.Load() != 2 {
		t.Fatal("downloads not started")
	}
	unblock()
	service.Wait()
	if maximum.Load() > 2 {
		t.Fatal("download concurrency exceeded limit")
	}
	for i, a := range m.Apps {
		bytes, _ := os.ReadFile(a.Path)
		if i == 4 {
			if string(bytes) != string(old) {
				t.Fatal("pinned version changed")
			}
		} else if string(bytes) != string(next) {
			t.Fatal("batch update failed")
		}
	}
}
