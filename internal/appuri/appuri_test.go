package appuri

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalURI(t *testing.T) {
	path, err := LocalPath("appimg:///home/test/My%20App.AppImage")
	if err != nil || path != "/home/test/My App.AppImage" {
		t.Fatalf("%q %v", path, err)
	}
	for _, raw := range []string{"appimg://remote/path", "appimg:relative", "https://example.com/app", "appimg:///tmp/app?run=true", "appimg:///tmp/%00app"} {
		if _, err = LocalPath(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestRemoteURIAndDownload(t *testing.T) {
	for _, raw := range []string{"appimg://download?url=https%3A%2F%2Fexample.com%2Fapp.AppImage", "appimg://download?url=http%3A%2F%2Fexample.com%2Fapp.AppImage", "appimg://host?url=https%3A%2F%2Fexample.com%2Fx", "appimg://download?url=https%3A%2F%2Fuser%3Apass%40example.com%2Fx"} {
		if got, err := RemoteURL(raw); raw == "appimg://download?url=https%3A%2F%2Fexample.com%2Fapp.AppImage" {
			if err != nil || got != "https://example.com/app.AppImage" {
				t.Fatalf("RemoteURL: %q %v", got, err)
			}
		} else if err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	image := make([]byte, 32)
	copy(image, []byte("\x7fELF"))
	image[8], image[9], image[10] = 'A', 'I', 2
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(image) }))
	defer server.Close()
	dir := t.TempDir()
	path, err := DownloadRemote(context.Background(), server.Client(), dir, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(filepath.Dir(path)) != dir {
		t.Fatalf("unexpected download path %s", path)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0700 {
		t.Fatalf("download permissions: %v %v", st, err)
	}
	bad := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("not an AppImage")) }))
	defer bad.Close()
	if _, err = DownloadRemote(context.Background(), bad.Client(), dir, bad.URL); err == nil {
		t.Fatal("accepted invalid AppImage")
	}
}
