package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallEntriesAndPreserveExisting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "share", "applications")
	executable := "/tmp/App Holder%/appholder"
	if err := Install(dir, executable); err != nil {
		t.Fatal(err)
	}
	handler, err := os.ReadFile(filepath.Join(dir, "appholder-open.desktop"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(handler), `Exec="/tmp/App Holder%%/appholder" open %f`) || !strings.Contains(string(handler), "NoDisplay=true") || !strings.Contains(string(handler), "Icon=appholder") {
		t.Fatalf("invalid handler: %s", handler)
	}
	library, err := os.ReadFile(filepath.Join(dir, "appholder.desktop"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(library), `Exec="/tmp/App Holder%%/appholder" gui`) || !strings.Contains(string(library), "Icon=appholder") {
		t.Fatalf("invalid library entry: %s", library)
	}
	uri, err := os.ReadFile(filepath.Join(dir, "appholder-uri.desktop"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(uri), "MimeType=x-scheme-handler/appimg;") || !strings.Contains(string(uri), `open-uri %u`) {
		t.Fatalf("invalid URI handler: %s", uri)
	}
	if err = Install(dir, executable); err != nil {
		t.Fatal("reinstallation failed:", err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "appholder-open.desktop"))
	if string(after) != string(handler) {
		t.Fatal("existing entry changed")
	}
	for _, path := range []string{
		filepath.Join(dir, "..", "icons", "hicolor", "scalable", "apps", "appholder.svg"),
		filepath.Join(dir, "..", "icons", "hicolor", "symbolic", "apps", "appholder-symbolic.svg"),
		filepath.Join(dir, "..", "icons", "hicolor", "256x256", "apps", "appholder.png"),
	} {
		if info, err := os.Stat(path); err != nil || info.Size() == 0 {
			t.Fatalf("icon was not installed: %s (%v)", path, err)
		}
	}
	if err = Install(dir, "/tmp/other"); err != nil {
		t.Fatal("could not update AppHolder's own entries:", err)
	}
	after, _ = os.ReadFile(filepath.Join(dir, "appholder.desktop"))
	if !strings.Contains(string(after), `Exec="/tmp/other" gui`) {
		t.Fatal("desktop entry still points at the old executable")
	}
}

func TestInstallPreservesUnrelatedEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "appholder.desktop")
	if err := os.WriteFile(path, []byte("unrelated desktop entry"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Install(dir, "/tmp/appholder"); err == nil {
		t.Fatal("overwrote unrelated entry")
	}
	if _, err := os.Stat(filepath.Join(dir, "appholder-open.desktop")); !os.IsNotExist(err) {
		t.Fatal("partially registered before preflight completed")
	}
}
