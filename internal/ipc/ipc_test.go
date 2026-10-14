package ipc

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"appholder/internal/manager"
)

func TestIntegrateAndList(t *testing.T) {
	root := t.TempDir()
	apps := filepath.Join(root, "apps")
	if err := os.MkdirAll(apps, 0755); err != nil {
		t.Fatal(err)
	}
	s := &Service{Manager: &manager.Manager{Config: manager.Config{Directories: []string{apps}}, StateDir: filepath.Join(root, "state"), DesktopDir: filepath.Join(root, "desktop")}}
	source := filepath.Join(root, "Demo.AppImage")
	if err := os.WriteFile(source, []byte("\x7fELF\x00\x00\x00\x00AI\x02payload"), 0600); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"path": source})
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/integrate", bytes.NewReader(body)))
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	response = httptest.NewRecorder()
	s.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/apps", nil))
	var list []manager.App
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Path != filepath.Join(apps, "Demo.AppImage") {
		t.Fatalf("unexpected library: %+v", list)
	}
	response = httptest.NewRecorder()
	s.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/integrate", bytes.NewReader([]byte(`{"path":"relative"}`))))
	if response.Code != 400 {
		t.Fatal("accepted relative path")
	}
}
