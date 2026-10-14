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

func TestSettingsAndRemovalBlockActiveJobs(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "apps")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	m := &manager.Manager{Config: manager.Config{Directories: []string{dir}}, ConfigPath: filepath.Join(root, "config.json"), StateDir: filepath.Join(root, "state"), DesktopDir: filepath.Join(root, "desktop")}
	file := filepath.Join(dir, "Demo.AppImage")
	if err := os.WriteFile(file, []byte("\x7fELF\x00\x00\x00\x00AI\x02payload"), 0700); err != nil {
		t.Fatal(err)
	}
	app, err := m.Integrate(file)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{Manager: m, operations: map[string]Operation{app.ID: {Status: "running"}}}
	handler := service.Handler()
	body, _ := json.Marshal(map[string]any{"directories": []string{filepath.Join(root, "other")}})
	for _, endpoint := range []string{"/settings", "/apps/" + app.ID + "/uninstall"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body)))
		if response.Code != 409 {
			t.Fatalf("active task did not block %s: %d", endpoint, response.Code)
		}
	}
	delete(service.operations, app.ID)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/settings", bytes.NewReader(body)))
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/apps/"+app.ID+"/remove", nil))
	if response.Code != 204 {
		t.Fatal(response.Body.String())
	}
	if _, err = os.Stat(file); err != nil {
		t.Fatal("remove endpoint deleted file")
	}
}
