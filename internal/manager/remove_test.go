package manager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveExcludesFileAndReintegrationRestoresHistory(t *testing.T) {
	m, dir := fixture(t)
	path := filepath.Join(dir, "Demo.AppImage")
	fake(t, path)
	app, err := m.Integrate(path)
	if err != nil {
		t.Fatal(err)
	}
	m.Apps[0].Pinned = true
	m.Apps[0].Versions = []Version{{Path: "saved-version", SHA256: "checksum"}}
	if err = m.Remove(app.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("removed the retained file")
	}
	// Simulate loading the persisted exclusion on a later daemon session.
	data, err := os.ReadFile(filepath.Join(m.StateDir, "removed.json"))
	if err != nil {
		t.Fatal(err)
	}
	m.Removed = nil
	if err = json.Unmarshal(data, &m.Removed); err != nil {
		t.Fatal(err)
	}
	previous, err := m.Scan(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Scan(previous); err != nil {
		t.Fatal(err)
	}
	if len(m.Apps) != 0 {
		t.Fatal("watcher re-registered removed file")
	}
	restored, err := m.Integrate(path)
	if err != nil {
		t.Fatal(err)
	}
	if !restored.Pinned || len(restored.Versions) != 1 {
		t.Fatal("management history lost")
	}
	if _, ok := m.Removed[path]; ok {
		t.Fatal("exclusion remains after reintegration")
	}
}
func TestUninstallDeletesOnlySelectedFileAndAllowsDropReinstall(t *testing.T) {
	m, dir := fixture(t)
	path := filepath.Join(dir, "Demo.AppImage")
	other := filepath.Join(dir, "Other.AppImage")
	fake(t, path)
	fake(t, other)
	app, err := m.Integrate(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Remove(app.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file still exists")
	}
	if _, err = os.Stat(other); err != nil {
		t.Fatal("other file removed")
	}
	fake(t, path)
	previous, err := m.Scan(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Scan(previous); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range m.Apps {
		if a.Path == path {
			found = true
		}
	}
	if !found {
		t.Fatal("new drop did not reinstall app")
	}
}
func TestUninstallRejectsChangedFile(t *testing.T) {
	m, dir := fixture(t)
	path := filepath.Join(dir, "Demo.AppImage")
	fake(t, path)
	app, err := m.Integrate(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("changed content"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = m.Remove(app.ID, true); err == nil {
		t.Fatal("deleted changed file")
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("changed file lost")
	}
}
