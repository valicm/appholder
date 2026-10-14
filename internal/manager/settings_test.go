package manager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFoldersPersistAndRemovingWatchKeepsFilesAndHistory(t *testing.T) {
	m, dir := fixture(t)
	m.ConfigPath = filepath.Join(t.TempDir(), "config.json")
	path := filepath.Join(dir, "Demo.AppImage")
	fake(t, path)
	app, err := m.Integrate(path)
	if err != nil {
		t.Fatal(err)
	}
	m.Apps[0].Pinned = true
	m.Apps[0].Sandboxed = true
	m.Apps[0].SandboxNetwork = true
	replacement := filepath.Join(t.TempDir(), "new-apps")
	if err = m.SetDirectories([]string{replacement, replacement}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(m.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	var config Config
	if err = json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Directories) != 1 {
		t.Fatal("folders not deduplicated")
	}
	if _, err = m.Scan(nil); err != nil {
		t.Fatal(err)
	}
	if len(m.Apps) != 0 {
		t.Fatal("removed watch remains registered")
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("watch removal deleted file")
	}
	if err = m.SetDirectories([]string{dir}); err != nil {
		t.Fatal(err)
	}
	previous, err := m.Scan(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Scan(previous); err != nil {
		t.Fatal(err)
	}
	if len(m.Apps) != 1 || m.Apps[0].ID != app.ID || !m.Apps[0].Pinned || !m.Apps[0].Sandboxed || !m.Apps[0].SandboxNetwork {
		t.Fatal("watch restore lost management state")
	}
	for _, invalid := range [][]string{nil, {"relative"}} {
		if err = m.SetDirectories(invalid); err == nil {
			t.Fatal("accepted invalid folders")
		}
	}
	if m.Config.Directories[0] != dir {
		t.Fatal("invalid config replaced active folders")
	}
}
