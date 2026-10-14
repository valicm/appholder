package update

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Download stages the complete file on the destination filesystem. Caller removes it.
func (c *Checker) Download(ctx context.Context, destination string, r Result) (string, error) {
	return c.DownloadProgress(ctx, destination, r, nil)
}
func (c *Checker) DownloadProgress(ctx context.Context, destination string, r Result, progress func(int64, int64)) (string, error) {
	sha1Bytes, sha1Err := hex.DecodeString(r.SHA1)
	sha256Bytes, sha256Err := hex.DecodeString(r.SHA256)
	if (r.SHA1 == "" && r.SHA256 == "") || (r.SHA1 != "" && (sha1Err != nil || len(sha1Bytes) != sha1.Size)) || (r.SHA256 != "" && (sha256Err != nil || len(sha256Bytes) != sha256.Size)) || r.Length <= 0 || r.Length > 16*1024*1024*1024 {
		return "", fmt.Errorf("invalid download metadata or file exceeds 16 GiB limit")
	}
	if r.ControlURL != "" && r.SHA1 != "" {
		if staged, ok := c.tryZsyncDelta(ctx, destination, r, progress); ok {
			return staged, nil
		}
	}
	client := *c.Client
	client.Timeout = 30 * time.Minute
	downloader := *c
	downloader.Client = &client
	response, err := downloader.get(ctx, r.URL)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.ContentLength >= 0 && response.ContentLength != r.Length {
		return "", fmt.Errorf("download length differs from update metadata")
	}
	file, err := os.CreateTemp(filepath.Dir(destination), ".appholder-download-*")
	if err != nil {
		return "", err
	}
	path := file.Name()
	ok := false
	defer func() {
		file.Close()
		if !ok {
			os.Remove(path)
		}
	}()
	var digests []io.Writer
	var sha1Digest, sha256Digest interface{ Sum([]byte) []byte }
	if r.SHA1 != "" {
		h := sha1.New()
		sha1Digest = h
		digests = append(digests, h)
	}
	if r.SHA256 != "" {
		h := sha256.New()
		sha256Digest = h
		digests = append(digests, h)
	}
	writers := []io.Writer{file}
	writers = append(writers, digests...)
	writers = append(writers, &progressWriter{total: r.Length, report: progress})
	n, err := io.Copy(io.MultiWriter(writers...), io.LimitReader(response.Body, r.Length+1))
	if err != nil {
		return "", err
	}
	if n != r.Length || (r.SHA1 != "" && hex.EncodeToString(sha1Digest.Sum(nil)) != r.SHA1) || (r.SHA256 != "" && hex.EncodeToString(sha256Digest.Sum(nil)) != r.SHA256) {
		return "", fmt.Errorf("download failed length or checksum verification")
	}
	if err = file.Chmod(0700); err != nil {
		return "", err
	}
	if err = file.Sync(); err != nil {
		return "", err
	}
	if err = file.Close(); err != nil {
		return "", err
	}
	ok = true
	return path, nil
}

// tryZsyncDelta uses the optional system zsync client, then independently checks its complete output.
func (c *Checker) tryZsyncDelta(ctx context.Context, destination string, result Result, progress func(int64, int64)) (string, bool) {
	binary, err := exec.LookPath("zsync")
	if err != nil {
		return "", false
	}
	child, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	controlResponse, err := c.get(child, result.ControlURL)
	if err != nil {
		return "", false
	}
	defer controlResponse.Body.Close()
	control, err := os.CreateTemp(filepath.Dir(destination), ".appholder-zsync-*.zsync")
	if err != nil {
		return "", false
	}
	controlPath := control.Name()
	defer os.Remove(controlPath)
	if _, err = io.Copy(control, io.LimitReader(controlResponse.Body, 64*1024*1024+1)); err != nil {
		control.Close()
		return "", false
	}
	if err = control.Close(); err != nil {
		return "", false
	}
	file, err := os.CreateTemp(filepath.Dir(destination), ".appholder-delta-*")
	if err != nil {
		return "", false
	}
	output := file.Name()
	file.Close()
	os.Remove(output)
	defer func() {
		if err != nil {
			os.Remove(output)
		}
	}()
	command := exec.CommandContext(child, binary, "-q", "-i", destination, "-o", output, "-u", result.ControlURL, controlPath)
	var outputLog bytes.Buffer
	command.Stdout = &outputLog
	command.Stderr = &outputLog
	err = command.Run()
	if err != nil {
		os.Remove(output)
		return "", false
	}
	if err = verifyDownload(output, result); err != nil {
		os.Remove(output)
		return "", false
	}
	if progress != nil {
		progress(result.Length, result.Length)
	}
	return output, true
}

func verifyDownload(path string, result Result) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != result.Length {
		return fmt.Errorf("delta output length differs from metadata")
	}
	if result.SHA1 != "" {
		hash := sha1.New()
		if _, err = io.Copy(hash, file); err != nil {
			return err
		}
		if hex.EncodeToString(hash.Sum(nil)) != result.SHA1 {
			return fmt.Errorf("delta output SHA-1 differs from metadata")
		}
	}
	if result.SHA256 != "" {
		if _, err = file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		hash := sha256.New()
		if _, err = io.Copy(hash, file); err != nil {
			return err
		}
		if hex.EncodeToString(hash.Sum(nil)) != result.SHA256 {
			return fmt.Errorf("delta output SHA-256 differs from metadata")
		}
	}
	return nil
}

type progressWriter struct {
	done, total int64
	last        time.Time
	report      func(int64, int64)
}

func (p *progressWriter) Write(data []byte) (int, error) {
	p.done += int64(len(data))
	if p.report != nil && (time.Since(p.last) >= 200*time.Millisecond || p.done >= p.total) {
		p.report(p.done, p.total)
		p.last = time.Now()
	}
	return len(data), nil
}
