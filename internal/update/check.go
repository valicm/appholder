// Package update checks publisher-provided update metadata without executing images.
package update

import (
	"bufio"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Result struct {
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
	SHA1       string    `json:"sha1,omitempty"`
	SHA256     string    `json:"sha256,omitempty"`
	URL        string    `json:"url,omitempty"`
	ControlURL string    `json:"control_url,omitempty"`
	Filename   string    `json:"filename,omitempty"`
	Length     int64     `json:"length,omitempty"`
	CheckedAt  time.Time `json:"checked_at"`
}
type Checker struct {
	Client  *http.Client
	APIBase string
}

type releaseAsset struct {
	Name, URL, Digest string
	Size              int64
}

func New() *Checker {
	return &Checker{Client: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		return secureURL(r.URL.String())
	}}, APIBase: "https://api.github.com"}
}
func secureURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "https" || u.Host == "" || u.User != nil {
		return fmt.Errorf("update URLs must use HTTPS without credentials")
	}
	return nil
}
func (c *Checker) get(ctx context.Context, raw string) (*http.Response, error) {
	if err := secureURL(raw); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "AppHolder")
	response, err := c.Client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != 200 {
		response.Body.Close()
		return nil, fmt.Errorf("update server returned HTTP %d", response.StatusCode)
	}
	return response, nil
}
func (c *Checker) source(ctx context.Context, info string) (string, error) {
	asset, err := c.resolveSource(ctx, info)
	return asset.URL, err
}

func (c *Checker) resolveSource(ctx context.Context, info string) (releaseAsset, error) {
	fields := strings.Split(info, "|")
	if len(fields) == 2 && fields[0] == "zsync" {
		return releaseAsset{URL: fields[1], Name: path.Base(fields[1])}, nil
	}
	if len(fields) != 5 || fields[0] != "gh-releases-zsync" {
		return releaseAsset{}, fmt.Errorf("unsupported update source")
	}
	for _, field := range fields[1:4] {
		if field == "" || strings.ContainsAny(field, "/?#\\") {
			return releaseAsset{}, fmt.Errorf("invalid GitHub source")
		}
	}
	endpoint := c.APIBase + "/repos/" + url.PathEscape(fields[1]) + "/" + url.PathEscape(fields[2]) + "/releases/"
	if fields[3] == "latest" {
		endpoint += "latest"
	} else if fields[3] == "latest-pre" || fields[3] == "latest-all" {
		endpoint += "?per_page=100&page=1"
	} else {
		endpoint += "tags/" + url.PathEscape(fields[3])
	}
	response, err := c.get(ctx, endpoint)
	if err != nil {
		return releaseAsset{}, err
	}
	defer response.Body.Close()
	type releaseData struct {
		TagName     string `json:"tag_name"`
		Draft       bool   `json:"draft"`
		Prerelease  bool   `json:"prerelease"`
		PublishedAt string `json:"published_at"`
		Assets      []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"assets"`
	}
	var selected releaseData
	if fields[3] == "latest-pre" || fields[3] == "latest-all" {
		var foundEligible []releaseData
		for page := 1; page <= 10; page++ {
			if page > 1 {
				response.Body.Close()
				pageURL := c.APIBase + "/repos/" + url.PathEscape(fields[1]) + "/" + url.PathEscape(fields[2]) + "/releases/?per_page=100&page=" + strconv.Itoa(page)
				response, err = c.get(ctx, pageURL)
				if err != nil {
					return releaseAsset{}, err
				}
			}
			var releases []releaseData
			if err = json.NewDecoder(io.LimitReader(response.Body, 8*1024*1024)).Decode(&releases); err != nil {
				response.Body.Close()
				return releaseAsset{}, err
			}
			response.Body.Close()
			if len(releases) == 0 {
				break
			}
			for _, candidate := range releases {
				if candidate.Draft || candidate.PublishedAt == "" || (fields[3] == "latest-pre" && !candidate.Prerelease) {
					continue
				}
				foundEligible = append(foundEligible, candidate)
			}
			if len(releases) < 100 {
				break
			}
		}
		if len(foundEligible) == 0 {
			return releaseAsset{}, fmt.Errorf("no published release matches selector %s", fields[3])
		}
		sort.Slice(foundEligible, func(i, j int) bool { return foundEligible[i].PublishedAt > foundEligible[j].PublishedAt })
		selected = foundEligible[0]
		response = nil
	} else if err = json.NewDecoder(io.LimitReader(response.Body, 2*1024*1024)).Decode(&selected); err != nil {
		return releaseAsset{}, err
	}
	if response != nil {
		response.Body.Close()
	}
	var found releaseAsset
	for _, asset := range selected.Assets {
		match, err := path.Match(fields[4], asset.Name)
		if err != nil {
			return releaseAsset{}, err
		}
		if match {
			if found.URL != "" {
				return releaseAsset{}, fmt.Errorf("multiple matching release assets")
			}
			found = releaseAsset{Name: asset.Name, URL: asset.URL, Digest: asset.Digest, Size: asset.Size}
		}
	}
	if found.URL == "" && strings.HasSuffix(strings.ToLower(fields[4]), ".zsync") {
		binaryPattern := fields[4][:len(fields[4])-len(".zsync")]
		for _, asset := range selected.Assets {
			match, err := path.Match(binaryPattern, asset.Name)
			if err != nil {
				return releaseAsset{}, err
			}
			if !match || !strings.EqualFold(path.Ext(asset.Name), ".AppImage") {
				continue
			}
			if found.URL != "" {
				return releaseAsset{}, fmt.Errorf("multiple matching AppImage release assets")
			}
			found = releaseAsset{Name: asset.Name, URL: asset.URL, Digest: asset.Digest, Size: asset.Size}
		}
	}
	if found.URL == "" {
		return releaseAsset{}, fmt.Errorf("no matching zsync or AppImage release asset")
	}
	if err := secureURL(found.URL); err != nil {
		return releaseAsset{}, err
	}
	return found, nil
}
func (c *Checker) Check(ctx context.Context, file, info string) Result {
	result := Result{CheckedAt: time.Now().UTC()}
	if info == "" {
		result.Status = "no-source"
		return result
	}
	fields := strings.Split(info, "|")
	kind := fields[0]
	if kind != "zsync" && kind != "gh-releases-zsync" {
		result.Status = "unsupported"
		return result
	}
	asset, err := c.resolveSource(ctx, info)
	if err == nil {
		if kind == "gh-releases-zsync" && !strings.HasSuffix(strings.ToLower(asset.Name), ".zsync") {
			result, err = c.checkReleaseAsset(file, asset)
		} else {
			result, err = c.checkZsync(ctx, file, asset.URL)
		}
		result.CheckedAt = time.Now().UTC()
	}
	if err != nil {
		result.Status = "error"
		result.Error = err.Error()
	}
	return result
}

func (c *Checker) checkReleaseAsset(file string, asset releaseAsset) (Result, error) {
	result := Result{URL: asset.URL, Filename: asset.Name, Length: asset.Size}
	if asset.Size <= 0 || asset.Size > 16*1024*1024*1024 {
		return result, fmt.Errorf("invalid GitHub AppImage asset size")
	}
	if !strings.HasPrefix(asset.Digest, "sha256:") {
		return result, fmt.Errorf("GitHub did not provide a SHA-256 digest for this release asset; publisher must include .zsync metadata")
	}
	digest, err := hex.DecodeString(strings.TrimPrefix(asset.Digest, "sha256:"))
	if err != nil || len(digest) != sha256.Size {
		return result, fmt.Errorf("invalid GitHub asset SHA-256 digest")
	}
	result.SHA256 = strings.ToLower(strings.TrimPrefix(asset.Digest, "sha256:"))
	fileHandle, err := os.Open(file)
	if err != nil {
		return result, err
	}
	defer fileHandle.Close()
	stat, err := fileHandle.Stat()
	if err != nil {
		return result, err
	}
	if !stat.Mode().IsRegular() {
		return result, fmt.Errorf("installed AppImage is not a regular file")
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, fileHandle); err != nil {
		return result, err
	}
	after, err := os.Stat(file)
	if err != nil {
		return result, err
	}
	if !os.SameFile(stat, after) || stat.Size() != after.Size() || !stat.ModTime().Equal(after.ModTime()) {
		return result, fmt.Errorf("application changed during update check")
	}
	if hex.EncodeToString(hash.Sum(nil)) == result.SHA256 {
		result.Status = "up-to-date"
	} else {
		result.Status = "available"
	}
	return result, nil
}
func (c *Checker) checkZsync(ctx context.Context, file, raw string) (Result, error) {
	response, err := c.get(ctx, raw)
	if err != nil {
		return Result{}, err
	}
	defer response.Body.Close()
	result, err := parseHeader(response.Body)
	if err != nil {
		return result, err
	}
	result.ControlURL = raw
	// Relative zsync URLs refer to the publisher's stable metadata location.
	// GitHub redirects metadata to an opaque signed CDN URL; using that URL
	// as the base drops its token and constructs an invalid unsigned asset URL.
	base, err := url.Parse(raw)
	if err != nil {
		return result, err
	}
	relative, err := url.Parse(result.URL)
	if err != nil {
		return result, err
	}
	result.URL = base.ResolveReference(relative).String()
	if err = secureURL(result.URL); err != nil {
		return result, err
	}
	f, err := os.Open(file)
	if err != nil {
		return result, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return result, err
	}
	digest := sha1.New()
	buffer := make([]byte, 128*1024)
	for {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		n, readErr := f.Read(buffer)
		if n > 0 {
			digest.Write(buffer[:n])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return result, readErr
		}
	}
	after, err := os.Stat(file)
	if err != nil {
		return result, err
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return result, fmt.Errorf("application changed during update check")
	}
	result.Status = "available"
	if hex.EncodeToString(digest.Sum(nil)) == result.SHA1 {
		result.Status = "up-to-date"
	}
	return result, nil
}
func parseHeader(reader io.Reader) (Result, error) {
	var r Result
	scanner := bufio.NewScanner(io.LimitReader(reader, 64*1024))
	scanner.Buffer(make([]byte, 4096), 64*1024)
	ended := false
	hasVersion := false
	seen := map[string]bool{}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			ended = true
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if seen[key] {
			return r, fmt.Errorf("duplicate zsync header")
		}
		seen[key] = true
		switch key {
		case "zsync":
			hasVersion = true
		case "SHA-1":
			r.SHA1 = strings.ToLower(value)
		case "URL":
			r.URL = value
		case "Filename":
			r.Filename = value
		case "Length":
			var err error
			r.Length, err = strconv.ParseInt(value, 10, 64)
			if err != nil {
				return r, err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return r, err
	}
	hash, err := hex.DecodeString(r.SHA1)
	if !ended || !hasVersion || err != nil || len(hash) != sha1.Size || r.URL == "" || r.Length <= 0 {
		return r, fmt.Errorf("invalid zsync metadata")
	}
	return r, nil
}
