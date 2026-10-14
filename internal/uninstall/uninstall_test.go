package uninstall

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveGeneratedOnlyRemovesAppHolderFiles(t *testing.T) {
	dir := t.TempDir()
	owned := filepath.Join(dir, "appholder.desktop")
	other := filepath.Join(dir, "other.desktop")
	if err := os.WriteFile(owned, []byte(desktopMarker+"[Desktop Entry]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("user-created entry\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := removeGenerated(dir, []string{"appholder.desktop", "other.desktop"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(owned); !os.IsNotExist(err) {
		t.Fatalf("generated entry remains: %v", err)
	}
	if data, err := os.ReadFile(other); err != nil || string(data) != "user-created entry\n" {
		t.Fatalf("unrelated entry changed: %q, %v", data, err)
	}
}
