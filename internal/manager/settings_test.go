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

func TestNewAppsUseSavedDefaultsAndCleanTechnicalName(t *testing.T) {
	m, appsDir := fixture(t)
	m.ConfigPath = filepath.Join(t.TempDir(), "config.json")
	if err := m.SetAppDefaults(true, true, false); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "opencode-desktop-linux-x86_64_e113afd2f43e5928128e21e1de0d0112.AppImage")
	fake(t, source)
	app, err := m.Integrate(source)
	if err != nil {
		t.Fatal(err)
	}
	if app.Name != "OpenCode" {
		t.Fatalf("display name = %q", app.Name)
	}
	if !app.AutoUpdate || !app.Sandboxed || app.SandboxNetwork {
		t.Fatalf("defaults not applied: %+v", app)
	}
	if _, err = os.Stat(filepath.Join(appsDir, filepath.Base(source))); err != nil {
		t.Fatal(err)
	}
}
