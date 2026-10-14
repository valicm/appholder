package metadata

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDesktopMetadata(t *testing.T) {
	name, version, err := parseDesktop("[Desktop Entry]\nName=Example\\sApp\nX-AppImage-Version=2.3\n[Desktop Action Launch]\nName=Wrong\n")
	if err != nil || name != "Example App" || version != "2.3" {
		t.Fatalf("%q %q %v", name, version, err)
	}
	if _, _, err = parseDesktop("[Desktop Entry]\nName=One\n[Desktop Entry]\nName=Two\n"); err == nil {
		t.Fatal("accepted ambiguous metadata")
	}
}
func TestReadRealSquashFS(t *testing.T) {
	if _, err := exec.LookPath("mksquashfs"); err != nil {
		t.Skip("mksquashfs unavailable")
	}
	if _, err := exec.LookPath("unsquashfs"); err != nil {
		t.Skip("unsquashfs unavailable")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "appdir")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "example.desktop"), []byte("[Desktop Entry]\nName=Example App\nComment=Desktop summary\nX-AppImage-Name=Example\nX-AppImage-Version=1.2.3\nX-AppImage-Arch=x86_64\nCategories=Graphics;Utility;Graphics;\nKeywords=design;drawing;\nMimeType=image/png;\n"), 0600); err != nil {
		t.Fatal(err)
	}
	metainfo := filepath.Join(dir, "usr", "share", "metainfo")
	if err := os.MkdirAll(metainfo, 0755); err != nil {
		t.Fatal(err)
	}
	appstream := `<component type="desktop-application"><id>org.example.App</id><name>Example App</name><summary>Draw something</summary><launchable type="desktop-id">example.desktop</launchable><developer><name>Example Team</name></developer><project_license>MIT</project_license><url type="homepage">https://example.org/app</url><description><p>Drawing application.</p><ul><li>Works offline</li></ul></description></component>`
	if err := os.WriteFile(filepath.Join(metainfo, "org.example.App.metainfo.xml"), []byte(appstream), 0600); err != nil {
		t.Fatal(err)
	}
	// A bundled helper must not override the payload's matching component.
	helper := `<component type="desktop-application"><id>org.example.Helper</id><launchable type="desktop-id">helper.desktop</launchable><summary>Wrong app</summary></component>`
	if err := os.WriteFile(filepath.Join(metainfo, "org.example.Helper.metainfo.xml"), []byte(helper), 0600); err != nil {
		t.Fatal(err)
	}
	png := []byte("\x89PNG\r\n\x1a\nfixture")
	if err := os.WriteFile(filepath.Join(dir, "example.png"), png, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("example.png", filepath.Join(dir, ".DirIcon")); err != nil {
		t.Fatal(err)
	}
	squash := filepath.Join(root, "filesystem")
	cmd := exec.Command("mksquashfs", dir, squash, "-noappend", "-processors", "1", "-no-progress")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", output, err)
	}
	data, err := os.ReadFile(squash)
	if err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(root, "Example.AppImage")
	header := make([]byte, 4096)
	copy(header, []byte("\x7fELF\x00\x00\x00\x00AI\x02"))
	if err = os.WriteFile(image, append(header, data...), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := Read(image)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "Example App" || info.Version != "1.2.3" || string(info.PNG) != string(png) {
		t.Fatalf("unexpected metadata: %+v", info)
	}
	if info.Identity != "appstream:org.example.App" || info.Developer != "Example Team" || info.License != "MIT" || info.Homepage != "https://example.org/app" || info.Summary != "Draw something" || len(info.Categories) != 2 || info.ArchitectureSource != "desktop" {
		t.Fatalf("rich metadata not extracted: %+v", info)
	}
}

func TestAppStreamOptionalFieldsAndPlainText(t *testing.T) {
	c, err := parseAppStream([]byte(`<component type="desktop-application"><id>org.example.App</id><summary xml:lang="de">Deutsch</summary><summary>Default summary</summary><description><p>Hello <em>world</em>.</p></description><url type="homepage">javascript:alert(1)</url></component>`))
	if err != nil {
		t.Fatal(err)
	}
	info := Info{Version: "declared-version"}
	applyAppStream(&info, &c)
	if info.Summary != "Default summary" || info.Description != "Hello world." || info.Homepage != "" || info.Version != "declared-version" || info.Developer != "" {
		t.Fatalf("incorrect optional metadata: %+v", info)
	}
	if _, err := parseAppStream([]byte(`<component type="desktop-application"><id>&external;</id></component>`)); err == nil {
		t.Fatal("accepted unresolved entity")
	}
}
