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

func TestDownloadVerifiesAndCleansFailures(t *testing.T) {
	data := []byte("downloaded content")
	hash := sha1.Sum(data)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	defer server.Close()
	checker := New()
	checker.Client = server.Client()
	dir := t.TempDir()
	destination := filepath.Join(dir, "Demo.AppImage")
	offer := Result{URL: server.URL, SHA1: hex.EncodeToString(hash[:]), Length: int64(len(data))}
	staged, err := checker.Download(context.Background(), destination, offer)
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := os.ReadFile(staged)
	if string(actual) != string(data) {
		t.Fatal("incorrect download")
	}
	os.Remove(staged)
	offer.SHA1 = strings.Repeat("a", 40)
	if _, err = checker.Download(context.Background(), destination, offer); err == nil {
		t.Fatal("accepted checksum mismatch")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatal("left failed download behind")
	}
	offer.Length++
	if _, err = checker.Download(context.Background(), destination, offer); err == nil {
		t.Fatal("accepted wrong length")
	}
}

func TestDownloadVerifiesSHA256OnlyAsset(t *testing.T) {
	data := []byte("github release appimage")
	digest := sha256.Sum256(data)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	defer server.Close()
	checker := New()
	checker.Client = server.Client()
	staged, err := checker.Download(context.Background(), filepath.Join(t.TempDir(), "Demo.AppImage"), Result{URL: server.URL, SHA256: hex.EncodeToString(digest[:]), Length: int64(len(data))})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(staged)
	if err != nil || string(got) != string(data) {
		t.Fatalf("download %q, %v", got, err)
	}
}

func TestDeltaUsesZsyncClientAndVerifiesItsOutput(t *testing.T) {
	data := []byte("reconstructed AppImage from zsync blocks")
	digest := sha1.Sum(data)
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/meta.zsync" {
			fmt.Fprintf(w, "zsync: 0.6.2\nLength: %d\nURL: /target\nSHA-1: %x\n\nchecksums", len(data), digest)
			return
		}
		if r.URL.Path == "/target" {
			requests++
			w.Write(data)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "target.AppImage")
	if err := os.WriteFile(target, data, 0600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nwhile [ $# -gt 0 ]; do if [ \"$1\" = \"-o\" ]; then shift; out=$1; fi; shift; done\n/bin/cp \"$APPHOLDER_TEST_TARGET\" \"$out\"\n"
	if err := os.WriteFile(filepath.Join(binDir, "zsync"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APPHOLDER_TEST_TARGET", target)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	checker := New()
	checker.Client = server.Client()
	var done int64
	staged, err := checker.DownloadProgress(context.Background(), filepath.Join(dir, "Demo.AppImage"), Result{URL: server.URL + "/target", ControlURL: server.URL + "/meta.zsync", SHA1: hex.EncodeToString(digest[:]), Length: int64(len(data))}, func(value, total int64) { done = value })
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(staged); err != nil || string(got) != string(data) {
		t.Fatalf("delta output %q: %v", got, err)
	}
	if requests != 0 {
		t.Fatal("downloaded the full binary despite a successful delta")
	}
	if done != int64(len(data)) {
		t.Fatalf("final progress %d", done)
	}
}
