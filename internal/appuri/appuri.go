package appuri

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LocalPath accepts appimg:///absolute/file.AppImage and opens the choice dialog.
func LocalPath(raw string) (string, error) {
	uri, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if uri.Scheme != "appimg" || uri.Opaque != "" || uri.User != nil || (uri.Host != "" && uri.Host != "localhost") || uri.RawQuery != "" || uri.Fragment != "" || !filepath.IsAbs(uri.Path) || strings.ContainsAny(uri.Path, "\x00\r\n") {
		return "", fmt.Errorf("expected a local AppImage URI: appimg:///absolute/path.AppImage")
	}
	return filepath.Clean(uri.Path), nil
}

// RemoteURL accepts the explicit appimg://download?url=https%3A... form.
func RemoteURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	query, queryErr := url.ParseQuery(u.RawQuery)
	if queryErr != nil || u.Scheme != "appimg" || u.Host != "download" || u.Path != "" || u.User != nil || u.Fragment != "" || len(query) != 1 || len(query["url"]) != 1 {
		return "", fmt.Errorf("expected appimg://download?url=https%%3A%%2F%%2Fexample.com%%2Fapp.AppImage")
	}
	target, err := url.Parse(query.Get("url"))
	if err != nil || target.Scheme != "https" || target.Host == "" || target.User != nil || target.Fragment != "" {
		return "", fmt.Errorf("remote AppImage links must use an HTTPS URL without credentials or fragments")
	}
	return target.String(), nil
}

// DownloadRemote saves a bounded HTTPS response in a private directory and validates its AppImage marker.
func DownloadRemote(ctx context.Context, client *http.Client, stateDir, target string) (string, error) {
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return "", fmt.Errorf("invalid HTTPS download URL")
	}
	if err = os.MkdirAll(filepath.Join(stateDir, "incoming"), 0700); err != nil {
		return "", err
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Minute}
	}
	copyClient := *client
	oldRedirect := copyClient.CheckRedirect
	copyClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || req.URL.User != nil {
			return fmt.Errorf("download redirected away from HTTPS")
		}
		if len(via) >= 10 {
			return fmt.Errorf("too many download redirects")
		}
		if oldRedirect != nil {
			return oldRedirect(req, via)
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	resp, err := copyClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download server returned %s", resp.Status)
	}
	if resp.ContentLength > 16*1024*1024*1024 {
		return "", fmt.Errorf("AppImage download exceeds 16 GiB limit")
	}
	f, err := os.CreateTemp(filepath.Join(stateDir, "incoming"), "remote-*.AppImage")
	if err != nil {
		return "", err
	}
	path, ok := f.Name(), false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(path)
		}
	}()
	n, err := io.Copy(f, io.LimitReader(resp.Body, 16*1024*1024*1024+1))
	if err != nil {
		return "", err
	}
	if n == 0 || n > 16*1024*1024*1024 {
		return "", fmt.Errorf("invalid AppImage download size")
	}
	if err = f.Chmod(0700); err != nil {
		return "", err
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	var marker [11]byte
	_, err = io.ReadFull(file, marker[:])
	file.Close()
	if err != nil || string(marker[:4]) != "\x7fELF" || marker[8] != 'A' || marker[9] != 'I' || (marker[10] != 1 && marker[10] != 2) {
		return "", fmt.Errorf("download is not a valid AppImage")
	}
	ok = true
	return path, nil
}
