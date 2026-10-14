package manager

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// importFile atomically publishes a complete copy without replacing any existing file.
func importFile(source, dir string) (string, error) {
	in, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer in.Close()
	before, err := in.Stat()
	if err != nil {
		return "", err
	}
	staged, err := os.CreateTemp(dir, ".appholder-import-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(staged.Name())
	defer staged.Close()
	digest := sha256.New()
	if _, err = io.Copy(io.MultiWriter(staged, digest), in); err != nil {
		return "", err
	}
	if err = staged.Chmod(0700); err != nil {
		return "", err
	}
	if err = staged.Sync(); err != nil {
		return "", err
	}
	if err = staged.Close(); err != nil {
		return "", err
	}
	after, err := os.Stat(source)
	if err != nil {
		return "", err
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return "", fmt.Errorf("source changed during import")
	}
	if err = Validate(staged.Name()); err != nil {
		return "", err
	}
	hash := hex.EncodeToString(digest.Sum(nil))
	name := filepath.Base(source)
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for attempt := 0; attempt < 100; attempt++ {
		candidate := filepath.Join(dir, name)
		if attempt == 1 {
			candidate = filepath.Join(dir, stem+"-"+hash[:12]+ext)
		} else if attempt > 1 {
			candidate = filepath.Join(dir, fmt.Sprintf("%s-%s-%d%s", stem, hash[:12], attempt, ext))
		}
		err = os.Link(staged.Name(), candidate)
		if err == nil {
			return candidate, nil
		}
		if !os.IsExist(err) {
			return "", err
		}
		st, err := os.Lstat(candidate)
		if err != nil {
			return "", err
		}
		if !st.Mode().IsRegular() {
			continue
		}
		existing, err := os.Open(candidate)
		if err != nil {
			return "", err
		}
		other := sha256.New()
		_, copyErr := io.Copy(other, existing)
		existing.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if hex.EncodeToString(other.Sum(nil)) == hash {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("too many conflicting filenames")
}
