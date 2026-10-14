package manager

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"appholder/internal/update"
)

func TestActivateAndRollback(t *testing.T) {
	m, dir := fixture(t)
	live := filepath.Join(dir, "Example.AppImage")
	fake(t, live)
	app, err := m.Integrate(live)
	if err != nil {
		t.Fatal(err)
	}
	m.Apps[0].AutoUpdate = true
	old, err := os.ReadFile(live)
	if err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(dir, ".download")
	next := append(append([]byte(nil), old...), []byte("new version")...)
	if err = os.WriteFile(staged, next, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := m.Activate(app.ID, app.Fingerprint, staged, update.Result{Status: "available"})
	if err != nil {
		t.Fatal(err)
	}
	if !installed.AutoUpdate {
		t.Fatal("update lost automatic preference")
	}
	if len(installed.Versions) != 1 {
		t.Fatal("old version not retained")
	}
	if filepath.Dir(installed.Versions[0].Path) != filepath.Join(m.StateDir, "versions", app.ID) {
		t.Fatal("backup was not stored in the AppHolder state directory")
	}
	if _, err := os.Stat(filepath.Join(dir, ".appholder")); !os.IsNotExist(err) {
		t.Fatal("created a backup directory beside the application")
	}
	actual, _ := os.ReadFile(live)
	if string(actual) != string(next) {
		t.Fatal("update not activated")
	}
	rollback, err := m.Rollback(app.ID)
	if err != nil {
		t.Fatal(err)
	}
	actual, _ = os.ReadFile(live)
	if string(actual) != string(old) {
		t.Fatal("rollback did not restore previous bytes")
	}
	if len(rollback.Versions) != 2 || rollback.Update.Status != "" || rollback.AutoUpdate {
		t.Fatal("rollback state incorrect")
	}
}
func TestActivationRejectsChangedSourceAndInvalidImage(t *testing.T) {
	m, dir := fixture(t)
	live := filepath.Join(dir, "Example.AppImage")
	fake(t, live)
	app, err := m.Integrate(live)
	if err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(dir, ".download")
	if err = os.WriteFile(staged, []byte("not an AppImage"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Activate(app.ID, app.Fingerprint, staged, update.Result{}); err == nil {
		t.Fatal("accepted invalid image")
	}
	fake(t, staged)
	if err = os.WriteFile(live, []byte("changed"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Activate(app.ID, app.Fingerprint, staged, update.Result{}); err == nil {
		t.Fatal("replaced changed source")
	}
}
func TestRollbackRejectsTamperedArchive(t *testing.T) {
	m, dir := fixture(t)
	live := filepath.Join(dir, "Example.AppImage")
	fake(t, live)
	app, err := m.Integrate(live)
	if err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(dir, ".download")
	fake(t, staged)
	installed, err := m.Activate(app.ID, app.Fingerprint, staged, update.Result{})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(installed.Versions[0].Path, []byte("tampered"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Rollback(app.ID); err == nil {
		t.Fatal("restored corrupt archive")
	}
}

func TestRestoreSelectedVersionAndRetention(t *testing.T) {
	m, dir := fixture(t)
	m.Config.KeepVersions = 3
	live := filepath.Join(dir, "Example.AppImage")
	fake(t, live)
	app, err := m.Integrate(live)
	if err != nil {
		t.Fatal(err)
	}
	contents := [][]byte{}
	for i := 0; i < 4; i++ {
		current, err := os.ReadFile(live)
		if err != nil {
			t.Fatal(err)
		}
		contents = append(contents, append([]byte(nil), current...))
		staged := filepath.Join(dir, ".download")
		next := append(append([]byte(nil), current...), byte('a'+i))
		if err = os.WriteFile(staged, next, 0700); err != nil {
			t.Fatal(err)
		}
		currentApp := m.Apps[0]
		if _, err = m.Activate(app.ID, currentApp.Fingerprint, staged, update.Result{}); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(m.Apps[0].Versions); got != 3 {
		t.Fatalf("retained %d versions, want 3", got)
	}
	// Select the oldest still retained version. Newest-first GUI order maps to this history index.
	want := contents[1]
	restored, err := m.RestoreVersion(app.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(live)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, want) {
		t.Fatal("selected saved version was not restored")
	}
	if len(restored.Versions) != 3 {
		t.Fatalf("restore retention = %d", len(restored.Versions))
	}
}

func TestSetKeepVersionsPrunesOldest(t *testing.T) {
	m, dir := fixture(t)
	m.ConfigPath = filepath.Join(dir, "config.json")
	live := filepath.Join(dir, "Example.AppImage")
	fake(t, live)
	app, err := m.Integrate(live)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		staged := filepath.Join(dir, ".download")
		current, _ := os.ReadFile(live)
		if err = os.WriteFile(staged, append(current, byte(i)), 0700); err != nil {
			t.Fatal(err)
		}
		if _, err = m.Activate(app.ID, m.Apps[0].Fingerprint, staged, update.Result{}); err != nil {
			t.Fatal(err)
		}
	}
	oldest := m.Apps[0].Versions[0].Path
	if err = m.SetKeepVersions(2); err != nil {
		t.Fatal(err)
	}
	if len(m.Apps[0].Versions) != 2 {
		t.Fatalf("retained %d", len(m.Apps[0].Versions))
	}
	if _, err = os.Stat(oldest); !os.IsNotExist(err) {
		t.Fatal("oldest backup was not deleted")
	}
}

func TestRetentionPrunesRemovedApplicationHistory(t *testing.T) {
	m, dir := fixture(t)
	m.ConfigPath = filepath.Join(dir, "config.json")
	m.Config.KeepVersions = 4
	live := filepath.Join(dir, "Example.AppImage")
	fake(t, live)
	app, err := m.Integrate(live)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		staged := filepath.Join(dir, ".download")
		current, _ := os.ReadFile(live)
		if err = os.WriteFile(staged, append(current, byte(i)), 0700); err != nil {
			t.Fatal(err)
		}
		if _, err = m.Activate(app.ID, m.Apps[0].Fingerprint, staged, update.Result{}); err != nil {
			t.Fatal(err)
		}
	}
	oldest := m.Apps[0].Versions[0].Path
	if err = m.Remove(app.ID, false); err != nil {
		t.Fatal(err)
	}
	if err = m.SetKeepVersions(2); err != nil {
		t.Fatal(err)
	}
	removed := m.Removed[live]
	if len(removed.App.Versions) != 2 {
		t.Fatalf("removed app retained %d backups", len(removed.App.Versions))
	}
	if _, err = os.Stat(oldest); !os.IsNotExist(err) {
		t.Fatal("oldest removed-app backup was not deleted")
	}
}
