package manager

import (
	"appholder/internal/metadata"
	"appholder/internal/update"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) (*Manager, string) {
	t.Helper()
	root := t.TempDir()
	apps := filepath.Join(root, "apps")
	if err := os.MkdirAll(apps, 0755); err != nil {
		t.Fatal(err)
	}
	return &Manager{Config: Config{Directories: []string{apps}}, StateDir: filepath.Join(root, "state"), DesktopDir: filepath.Join(root, "desktop")}, apps
}
func fake(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("\x7fELF\x00\x00\x00\x00AI\x02payload"), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestScanSettlesAndCleansDeletedApps(t *testing.T) {
	m, dir := fixture(t)
	path := filepath.Join(dir, "Example.AppImage")
	fake(t, path)
	previous, err := m.Scan(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Apps) != 0 {
		t.Fatal("registered before settling")
	}
	_, err = m.Scan(previous)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Apps) != 1 {
		t.Fatal("missing registration")
	}
	desktop := filepath.Join(m.DesktopDir, "appholder-"+m.Apps[0].ID+".desktop")
	if _, err = os.Stat(desktop); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Scan(previous); err != nil {
		t.Fatal(err)
	}
	if len(m.Apps) != 0 {
		t.Fatal("stale registration")
	}
	if _, err = os.Stat(desktop); !os.IsNotExist(err) {
		t.Fatal("stale desktop entry")
	}
}
func TestRejectsInvalidAndSymlinkFiles(t *testing.T) {
	m, dir := fixture(t)
	bad := filepath.Join(dir, "bad.AppImage")
	if err := os.WriteFile(bad, []byte("not an executable"), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target")
	fake(t, target)
	if err := os.Symlink(target, filepath.Join(dir, "link.AppImage")); err != nil {
		t.Fatal(err)
	}
	p, err := m.Scan(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Scan(p); err != nil {
		t.Fatal(err)
	}
	if len(m.Apps) != 0 {
		t.Fatal("invalid file registered")
	}
}
func TestIntegrateReusesIdenticalFileAndKeepsDifferentVersions(t *testing.T) {
	m, _ := fixture(t)
	source := filepath.Join(t.TempDir(), "Example.AppImage")
	fake(t, source)
	first, err := m.Integrate(source)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := m.Integrate(source)
	if err != nil || duplicate.ID != first.ID || len(m.Apps) != 1 {
		t.Fatal("identical file was not reused")
	}
	original, _ := os.ReadFile(first.Path)
	if err := os.WriteFile(source, append(append([]byte(nil), original...), []byte("second version")...), 0700); err != nil {
		t.Fatal(err)
	}
	second, err := m.Integrate(source)
	if err != nil {
		t.Fatal(err)
	}
	if second.Path == first.Path || len(m.Apps) != 2 {
		t.Fatal("versions were not installed side by side")
	}
	actual, _ := os.ReadFile(first.Path)
	if string(actual) != string(original) {
		t.Fatal("original version overwritten")
	}
	if again, err := m.Integrate(source); err != nil || again.ID != second.ID || len(m.Apps) != 2 {
		t.Fatal("second version duplicated on repeat import")
	}
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(first.Path), ".appholder-import-*"))
	if err != nil || len(leftovers) > 0 {
		t.Fatal("temporary import remains")
	}
}
func TestDesktopQuote(t *testing.T) {
	got := desktopQuote("/tmp/a% $b.AppImage")
	if !strings.Contains(got, "%%") || !strings.Contains(got, "\\$") {
		t.Fatalf("unsafe command: %s", got)
	}
}

func TestMetadataReinspectionPreservesManagementState(t *testing.T) {
	m, dir := fixture(t)
	path := filepath.Join(dir, "Example.AppImage")
	fake(t, path)
	if _, err := m.Integrate(path); err != nil {
		t.Fatal(err)
	}
	m.Apps[0].MetadataRevision = 0
	m.Apps[0].Pinned = true
	m.Apps[0].Update = update.Result{Status: "available", SHA1: "offered-file"}
	m.Apps[0].LastNotifiedSHA1 = "offered-file"
	m.Apps[0].Versions = []Version{{Path: "/saved/version", SHA256: "saved-hash"}}
	previous, err := m.Scan(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Scan(previous); err != nil {
		t.Fatal(err)
	}
	a := m.Apps[0]
	if a.MetadataRevision != metadata.Revision || !a.Pinned || a.Update.Status != "available" || a.LastNotifiedSHA1 != "offered-file" || len(a.Versions) != 1 {
		t.Fatalf("management state lost: %+v", a)
	}
}
