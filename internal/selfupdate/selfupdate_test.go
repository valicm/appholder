package selfupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleaseAndVerifiedReplacement(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	ReleasePublicKey = base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}))
	t.Cleanup(func() { ReleasePublicKey = "" })
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(binary)
	hash := hex.EncodeToString(digest[:])
	asset := "appholder-" + runtime.GOOS + "-" + runtime.GOARCH
	manifest := fmt.Sprintf("%s  %s\n", hash, asset)
	signature := ed25519.Sign(privateKey, []byte(manifest))
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest.txt":
			fmt.Fprintln(w, "v0.1.0")
		case "/v0.1.0/SHA256SUMS":
			fmt.Fprint(w, manifest)
		case "/v0.1.0/SHA256SUMS.sig":
			_, _ = w.Write(signature)
		default:
			w.Write(binary)
		}
	}))
	defer server.Close()
	updater := New()
	updater.Client = server.Client()
	release, err := updater.Latest(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if release.Version != "v0.1.0" || release.SHA256 != hash {
		t.Fatal(release)
	}
	target := filepath.Join(t.TempDir(), "appholder")
	if err = os.WriteFile(target, []byte("old executable"), 0755); err != nil {
		t.Fatal(err)
	}
	bad := release
	bad.SHA256 = strings.Repeat("a", 64)
	if err = updater.Replace(context.Background(), target, bad); err == nil {
		t.Fatal("accepted corrupt binary")
	}
	actual, _ := os.ReadFile(target)
	if string(actual) != "old executable" {
		t.Fatal("changed binary before verification")
	}
	if err = updater.Replace(context.Background(), target, release); err != nil {
		t.Fatal(err)
	}
	actual, _ = os.ReadFile(target)
	if string(actual) != string(binary) {
		t.Fatal("incorrect replacement")
	}
}
func TestRejectsUnsafeReleaseMetadata(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "../bad") }))
	defer server.Close()
	updater := New()
	updater.Client = server.Client()
	if _, err := updater.Latest(context.Background(), server.URL); err == nil {
		t.Fatal("accepted unsafe version")
	}
	if _, err := updater.Latest(context.Background(), "http://example.com"); err == nil {
		t.Fatal("accepted HTTP")
	}
}

func TestVerifyManifestRejectsTamperingAndMissingKey(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	pinned := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}))
	manifest := []byte("verified manifest\n")
	signature := ed25519.Sign(privateKey, manifest)
	if err = VerifyManifest(manifest, signature, pinned); err != nil {
		t.Fatal(err)
	}
	if err = VerifyManifest([]byte("tampered manifest\n"), signature, pinned); err == nil {
		t.Fatal("accepted a modified manifest")
	}
	if err = VerifyManifest(manifest, signature, ""); err == nil {
		t.Fatal("accepted a missing pinned key")
	}
}

func TestGitHubReleaseLatest(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	ReleasePublicKey = base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}))
	t.Cleanup(func() { ReleasePublicKey = "" })
	binary, err := os.ReadFile(mustExecutable(t))
	if err != nil {
		t.Fatal(err)
	}
	asset := "appholder-" + runtime.GOOS + "-" + runtime.GOARCH
	digest := sha256.Sum256(binary)
	manifest := []byte(fmt.Sprintf("%x  %s\n", digest, asset))
	signature := ed25519.Sign(privateKey, manifest)
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/appholder/releases/latest":
			fmt.Fprintf(w, `{"tag_name":"v1.2.3","assets":[{"name":"SHA256SUMS","browser_download_url":%q},{"name":"SHA256SUMS.sig","browser_download_url":%q},{"name":%q,"browser_download_url":%q}]}`, server.URL+"/download/SHA256SUMS", server.URL+"/download/SHA256SUMS.sig", asset, server.URL+"/download/"+asset)
		case "/download/SHA256SUMS":
			_, _ = w.Write(manifest)
		case "/download/SHA256SUMS.sig":
			_, _ = w.Write(signature)
		case "/download/" + asset:
			_, _ = w.Write(binary)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	updater := New()
	updater.APIBase = server.URL
	updater.Client = server.Client()
	release, err := updater.Latest(context.Background(), "https://github.com/acme/appholder")
	if err != nil {
		t.Fatal(err)
	}
	if release.Version != "v1.2.3" || release.SHA256 != fmt.Sprintf("%x", digest) || release.URL != server.URL+"/download/"+asset {
		t.Fatalf("unexpected release: %+v", release)
	}
}

func mustExecutable(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return path
}
