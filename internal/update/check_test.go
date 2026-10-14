package update

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckZsyncAndChangedFile(t *testing.T) {
	data := []byte("installed version")
	digest := sha1.Sum(data)
	remote := hex.EncodeToString(digest[:])
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "zsync: 0.6.2\nFilename: Demo.AppImage\nLength: %d\nURL: Demo.AppImage\nSHA-1: %s\n\nbinary checksums", len(data), remote)
	}))
	defer server.Close()
	file := filepath.Join(t.TempDir(), "Demo.AppImage")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	checker := New()
	checker.Client = server.Client()
	result := checker.Check(context.Background(), file, "zsync|"+server.URL+"/Demo.zsync")
	if result.Status != "up-to-date" || result.URL != server.URL+"/Demo.AppImage" {
		t.Fatalf("unexpected result: %+v", result)
	}
	remote = strings.Repeat("a", 40)
	result = checker.Check(context.Background(), file, "zsync|"+server.URL+"/Demo.zsync")
	if result.Status != "available" {
		t.Fatalf("%+v", result)
	}
}
func TestGitHubAssetMatching(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo/releases/tags/continuous" {
			t.Errorf("unexpected API path %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"assets":[{"name":"Demo-x86_64.AppImage.zsync","browser_download_url":"https://example.com/demo.zsync"},{"name":"Demo-aarch64.AppImage.zsync","browser_download_url":"https://example.com/arm.zsync"}]}`)
	}))
	defer server.Close()
	checker := New()
	checker.Client = server.Client()
	checker.APIBase = server.URL
	source, err := checker.source(context.Background(), "gh-releases-zsync|owner|repo|continuous|Demo-*x86_64.AppImage.zsync")
	if err != nil || source != "https://example.com/demo.zsync" {
		t.Fatalf("%q %v", source, err)
	}
	if _, err = checker.source(context.Background(), "gh-releases-zsync|owner|repo|continuous|*.zsync"); err == nil {
		t.Fatal("accepted ambiguous release assets")
	}
}

func TestGitHubLatestPrereleaseSelectors(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo/releases/" || r.URL.Query().Get("per_page") != "100" {
			t.Errorf("unexpected request %s", r.URL.String())
		}
		fmt.Fprint(w, `[
{"tag_name":"v3","draft":false,"prerelease":false,"published_at":"2026-10-10T00:00:00Z","assets":[{"name":"App-x86_64.zsync","browser_download_url":"https://example.com/stable"}]},
{"tag_name":"v4-beta","draft":false,"prerelease":true,"published_at":"2026-10-12T00:00:00Z","assets":[{"name":"App-x86_64.zsync","browser_download_url":"https://example.com/beta"}]},
{"tag_name":"v5-draft","draft":true,"prerelease":true,"published_at":"2026-10-14T00:00:00Z","assets":[{"name":"App-x86_64.zsync","browser_download_url":"https://example.com/draft"}]}]`)
	}))
	defer server.Close()
	checker := New()
	checker.Client = server.Client()
	checker.APIBase = server.URL
	for selector, want := range map[string]string{"latest-pre": "https://example.com/beta", "latest-all": "https://example.com/beta"} {
		got, err := checker.source(context.Background(), "gh-releases-zsync|owner|repo|"+selector+"|App-*.zsync")
		if err != nil || got != want {
			t.Errorf("%s source = %q, %v; want %q", selector, got, err, want)
		}
	}
}

func TestGitHubReleaseWithoutZsyncUsesPublishedDigest(t *testing.T) {
	payload := []byte("new AppImage payload")
	digest := sha256.Sum256(payload)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo/releases/tags/v2" {
			t.Errorf("unexpected %s", r.URL.Path)
		}
		fmt.Fprintf(w, `{"assets":[{"name":"Demo-x86_64.AppImage","browser_download_url":"%s/download","digest":"sha256:%x","size":%d}]}`, serverURL(r), digest, len(payload))
	}))
	defer server.Close()
	local := filepath.Join(t.TempDir(), "Demo.AppImage")
	if err := os.WriteFile(local, []byte("old AppImage payload"), 0700); err != nil {
		t.Fatal(err)
	}
	checker := New()
	checker.Client = server.Client()
	checker.APIBase = server.URL
	result := checker.Check(context.Background(), local, "gh-releases-zsync|owner|repo|v2|Demo-x86_64.AppImage.zsync")
	if result.Status != "available" || result.SHA256 != hex.EncodeToString(digest[:]) || result.Length != int64(len(payload)) {
		t.Fatalf("unexpected release fallback: %+v", result)
	}
}

func serverURL(r *http.Request) string { return "https://" + r.Host }

func TestRelativeDownloadUsesStableURLAfterSignedRedirect(t *testing.T) {
	data := []byte("installed version")
	digest := sha1.Sum(data)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/download/latest/Demo.AppImage.zsync":
			http.Redirect(w, r, "/opaque/cdn/asset?token=metadata-only", http.StatusFound)
		case "/opaque/cdn/asset":
			fmt.Fprintf(w, "zsync: 0.6.2\nFilename: Demo.AppImage\nLength: %d\nURL: Demo.AppImage\nSHA-1: %s\n\n", len(data), hex.EncodeToString(digest[:]))
		case "/releases/download/latest/Demo.AppImage":
			w.Write(data)
		default:
			http.Error(w, "jwt-not-provided", 618)
		}
	}))
	defer server.Close()
	file := filepath.Join(t.TempDir(), "Demo.AppImage")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	checker := New()
	checker.Client = server.Client()
	result := checker.Check(context.Background(), file, "zsync|"+server.URL+"/releases/download/latest/Demo.AppImage.zsync")
	if result.Status != "up-to-date" || result.URL != server.URL+"/releases/download/latest/Demo.AppImage" {
		t.Fatalf("incorrect download URL after redirect: %+v", result)
	}
	staged, err := checker.Download(context.Background(), file, result)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(staged)
}
func TestInvalidSourcesAndHeaders(t *testing.T) {
	checker := New()
	if result := checker.Check(context.Background(), "missing", ""); result.Status != "no-source" {
		t.Fatal(result)
	}
	if result := checker.Check(context.Background(), "missing", "pling-v1-zsync|123"); result.Status != "unsupported" {
		t.Fatal(result)
	}
	if result := checker.Check(context.Background(), "missing", "zsync|http://example.com/demo.zsync"); result.Status != "error" {
		t.Fatal(result)
	}
	for _, header := range []string{"garbage\n\n", "zsync: 0.6.2\nLength: 1\nURL: file\nSHA-1: abc\n\n", "zsync: 0.6.2\nLength: 1\nLength: 2\n\n"} {
		if _, err := parseHeader(strings.NewReader(header)); err == nil {
			t.Fatalf("accepted %q", header)
		}
	}
}
