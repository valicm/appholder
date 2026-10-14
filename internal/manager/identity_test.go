package manager

import (
	"appholder/internal/metadata"
	"testing"
)

func TestRelatedUsesDeclaredIdentity(t *testing.T) {
	a := App{ID: "first", Name: "Same name", Metadata: metadata.Info{Identity: "appstream:org.example.App", IdentitySource: "appstream"}}
	b := App{ID: "second", Name: "Different title", Metadata: a.Metadata}
	if !a.Related(b) {
		t.Fatal("canonical identity not recognized")
	}
	b.Metadata = metadata.Info{}
	b.Name = a.Name
	if a.Related(b) {
		t.Fatal("grouped by display name")
	}
	a.Metadata = metadata.Info{Identity: "appimage:Example", IdentitySource: "appimage"}
	b.Metadata = a.Metadata
	if a.Related(b) {
		t.Fatal("unqualified name grouped without publisher context")
	}
	a.UpdateInfo = "zsync|https://example.org/app.zsync"
	b.UpdateInfo = a.UpdateInfo
	if !a.Related(b) {
		t.Fatal("same publisher/name not recognized")
	}
}
