// Package selfupdate replaces AppHolder itself, independently of managed AppImages.
package selfupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"debug/elf"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"
)

var Version = "dev"
var ReleaseBaseURL = ""

// ReleasePublicKey is the base64-encoded PEM Ed25519 public key pinned at build time.
var ReleasePublicKey = ""
var versionPattern = regexp.MustCompile(`^v?[0-9][A-Za-z0-9._-]{0,100}$`)

type Release struct{ Version, URL, SHA256 string }
type Updater struct {
	Client  *http.Client
	APIBase string
}

func New() *Updater {
	return &Updater{APIBase: "https://api.github.com", Client: &http.Client{Timeout: 10 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		return https(r.URL.String())
	}}}
}
func https(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("release URLs must use HTTPS without credentials, query or fragment")
	}
	return nil
}
func (u *Updater) get(ctx context.Context, raw string) (*http.Response, error) {
	if err := https(raw); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "AppHolder/"+Version)
	r, err := u.Client.Do(req)
	if err != nil {
		return nil, err
	}
	if r.StatusCode != 200 {
		r.Body.Close()
		return nil, fmt.Errorf("release server returned HTTP %d", r.StatusCode)
	}
	return r, nil
}
func (u *Updater) text(ctx context.Context, raw string, limit int64) (string, error) {
	r, err := u.get(ctx, raw)
	if err != nil {
		return "", err
	}
	defer r.Body.Close()
	b, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return "", err
	}
	if int64(len(b)) > limit {
		return "", fmt.Errorf("release metadata too large")
	}
	return string(b), nil
}
func (u *Updater) Latest(ctx context.Context, base string) (Release, error) {
	if base == "" {
		return Release{}, fmt.Errorf("no release host configured; set APPHOLDER_RELEASE_BASE_URL or use --base-url")
	}
	base = strings.TrimRight(base, "/")
	if parsed, err := url.Parse(base); err != nil || parsed.RawQuery != "" {
		return Release{}, fmt.Errorf("release base must not contain a query")
	}
	if err := https(base); err != nil {
		return Release{}, err
	}
	if owner, repository, ok := githubRepository(base); ok {
		return u.latestGitHub(ctx, owner, repository)
	}
	text, err := u.text(ctx, base+"/latest.txt", 128)
	if err != nil {
		return Release{}, err
	}
	version := strings.TrimSpace(text)
	if !versionPattern.MatchString(version) {
		return Release{}, fmt.Errorf("invalid published version")
	}
	asset := "appholder-" + runtime.GOOS + "-" + runtime.GOARCH
	checksums, err := u.text(ctx, base+"/"+version+"/SHA256SUMS", 64*1024)
	if err != nil {
		return Release{}, err
	}
	signature, err := u.text(ctx, base+"/"+version+"/SHA256SUMS.sig", 1024)
	if err != nil {
		return Release{}, err
	}
	if err = VerifyManifest([]byte(checksums), []byte(signature), ReleasePublicKey); err != nil {
		return Release{}, err
	}
	hash := ""
	for _, line := range strings.Split(checksums, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == asset {
			if hash != "" {
				return Release{}, fmt.Errorf("duplicate asset checksum")
			}
			hash = strings.ToLower(fields[0])
		}
	}
	digest, err := hex.DecodeString(hash)
	if err != nil || len(digest) != sha256.Size {
		return Release{}, fmt.Errorf("no valid checksum for %s", asset)
	}
	return Release{Version: version, URL: base + "/" + version + "/" + asset, SHA256: hash}, nil
}

func githubRepository(base string) (string, string, bool) {
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host != "github.com" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func (u *Updater) latestGitHub(ctx context.Context, owner, repository string) (Release, error) {
	endpoint := strings.TrimRight(u.APIBase, "/") + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repository) + "/releases/latest"
	r, err := u.get(ctx, endpoint)
	if err != nil {
		return Release{}, err
	}
	defer r.Body.Close()
	var data struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err = json.NewDecoder(io.LimitReader(r.Body, 4*1024*1024)).Decode(&data); err != nil {
		return Release{}, err
	}
	if !versionPattern.MatchString(data.TagName) {
		return Release{}, fmt.Errorf("invalid published version")
	}
	assetName := "appholder-" + runtime.GOOS + "-" + runtime.GOARCH
	assets := map[string]string{}
	for _, asset := range data.Assets {
		if asset.Name == "SHA256SUMS" || asset.Name == "SHA256SUMS.sig" || asset.Name == assetName {
			if assets[asset.Name] != "" {
				return Release{}, fmt.Errorf("duplicate GitHub release asset %s", asset.Name)
			}
			if err = https(asset.URL); err != nil {
				return Release{}, err
			}
			assets[asset.Name] = asset.URL
		}
	}
	for _, required := range []string{"SHA256SUMS", "SHA256SUMS.sig", assetName} {
		if assets[required] == "" {
			return Release{}, fmt.Errorf("GitHub release is missing %s", required)
		}
	}
	checksums, err := u.text(ctx, assets["SHA256SUMS"], 64*1024)
	if err != nil {
		return Release{}, err
	}
	signature, err := u.text(ctx, assets["SHA256SUMS.sig"], 1024)
	if err != nil {
		return Release{}, err
	}
	if err = VerifyManifest([]byte(checksums), []byte(signature), ReleasePublicKey); err != nil {
		return Release{}, err
	}
	hash := ""
	for _, line := range strings.Split(checksums, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == assetName {
			if hash != "" {
				return Release{}, fmt.Errorf("duplicate asset checksum")
			}
			hash = strings.ToLower(fields[0])
		}
	}
	digest, err := hex.DecodeString(hash)
	if err != nil || len(digest) != sha256.Size {
		return Release{}, fmt.Errorf("no valid checksum for %s", assetName)
	}
	return Release{Version: data.TagName, URL: assets[assetName], SHA256: hash}, nil
}

// VerifyManifest authenticates the exact checksum manifest bytes using a pinned Ed25519 key.
func VerifyManifest(manifest, signature []byte, publicKeyB64 string) error {
	if publicKeyB64 == "" {
		return fmt.Errorf("this AppHolder build has no release-signing public key")
	}
	encoded, err := base64.StdEncoding.DecodeString(publicKeyB64)
	if err != nil {
		return fmt.Errorf("invalid pinned release public key")
	}
	block, _ := pem.Decode(encoded)
	if block == nil {
		return fmt.Errorf("invalid pinned release public key")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("invalid pinned release public key")
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok || len(signature) != ed25519.SignatureSize || !ed25519.Verify(key, manifest, signature) {
		return fmt.Errorf("release checksum signature verification failed")
	}
	return nil
}
func (u *Updater) Replace(ctx context.Context, target string, release Release) error {
	target, err := filepath.EvalSymlinks(target)
	if err != nil {
		return err
	}
	if strings.HasPrefix(target, "/usr/bin/") || strings.HasPrefix(target, "/bin/") {
		return fmt.Errorf("use your package manager to update %s", target)
	}
	lock, err := os.OpenFile(target+".update.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("cannot write to %s; system-wide installations require sudo: %w", filepath.Dir(target), err)
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("another self-update is running")
	}
	expected, err := hex.DecodeString(release.SHA256)
	if err != nil || len(expected) != sha256.Size {
		return fmt.Errorf("invalid release checksum")
	}
	file, err := os.CreateTemp(filepath.Dir(target), ".appholder-update-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	response, err := u.get(ctx, release.URL)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	digest := sha256.New()
	n, err := io.Copy(io.MultiWriter(file, digest), io.LimitReader(response.Body, 256*1024*1024+1))
	if err != nil {
		return err
	}
	if n > 256*1024*1024 {
		return fmt.Errorf("release binary exceeds 256 MiB")
	}
	if hex.EncodeToString(digest.Sum(nil)) != strings.ToLower(release.SHA256) {
		return fmt.Errorf("release checksum mismatch; installed binary unchanged")
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	binary, err := elf.Open(file.Name())
	if err != nil {
		return fmt.Errorf("release is not an ELF executable: %w", err)
	}
	defer binary.Close()
	wanted := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}[runtime.GOARCH]
	if wanted == 0 || binary.Machine != wanted || (binary.Type != elf.ET_EXEC && binary.Type != elf.ET_DYN) {
		return fmt.Errorf("release is not an executable for this architecture")
	}
	if err = os.Chmod(file.Name(), 0755); err != nil {
		return err
	}
	return os.Rename(file.Name(), target)
}
