package gui

import (
	"appholder/internal/manager"
	"appholder/internal/metadata"
	"strings"
	"testing"
)

func TestDetailsDistinguishesUnknownFieldsAndRelatedCopies(t *testing.T) {
	a := manager.App{ID: "a", Name: "Example", Path: "/apps/example.AppImage", Metadata: metadata.Info{Identity: "appstream:org.example.App", IdentitySource: "appstream", Architecture: "x86_64", ArchitectureSource: "runtime-elf"}}
	b := a
	b.ID = "b"
	b.Path = "/apps/older.AppImage"
	b.Version = "1.0"
	text := applicationDetails(a, []manager.App{a, b})
	for _, expected := range []string{"Version: Unknown", "Developer: Unknown", "x86_64 (AppImage runtime)", "1.0 · x86_64", b.Path} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing %q in details", expected)
		}
	}
}
