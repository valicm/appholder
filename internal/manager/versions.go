package manager

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"appholder/internal/update"
)

type Version struct {
	Path    string    `json:"path"`
	Version string    `json:"version,omitempty"`
	SHA256  string    `json:"sha256"`
	SavedAt time.Time `json:"saved_at"`
}

func fingerprint(path string) (string, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("application must be a regular file")
	}
	return fmt.Sprintf("%d:%d", st.Size(), st.ModTime().UnixNano()), nil
}
func copyVersion(source, dir, pattern string) (string, string, error) {
	in, err := os.Open(source)
	if err != nil {
		return "", "", err
	}
	defer in.Close()
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", "", err
	}
	out, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", "", err
	}
	ok := false
	defer func() {
		out.Close()
		if !ok {
			os.Remove(out.Name())
		}
	}()
	hash := sha256.New()
	if _, err = io.Copy(io.MultiWriter(out, hash), in); err != nil {
		return "", "", err
	}
	if err = out.Chmod(0700); err != nil {
		return "", "", err
	}
	if err = out.Sync(); err != nil {
		return "", "", err
	}
	if err = out.Close(); err != nil {
		return "", "", err
	}
	ok = true
	return out.Name(), hex.EncodeToString(hash.Sum(nil)), nil
}
func (m *Manager) Activate(id, expected, staged string, offered update.Result) (App, error) {
	index := -1
	for i, a := range m.Apps {
		if a.ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return App{}, fmt.Errorf("application no longer registered")
	}
	old := m.Apps[index]
	actual, err := fingerprint(old.Path)
	if err != nil {
		return App{}, err
	}
	if actual != expected || old.Fingerprint != expected {
		return App{}, fmt.Errorf("application changed; check updates again")
	}
	if filepath.Dir(staged) != filepath.Dir(old.Path) {
		return App{}, fmt.Errorf("staged file must be on the application filesystem")
	}
	if err = Validate(staged); err != nil {
		return App{}, err
	}
	archive, hash, err := copyVersion(old.Path, filepath.Join(m.StateDir, "versions", id), "version-*.AppImage")
	if err != nil {
		return App{}, err
	}
	if actual, err = fingerprint(old.Path); err != nil || actual != expected {
		os.Remove(archive)
		return App{}, fmt.Errorf("application changed during backup")
	}
	history := append(append([]Version(nil), old.Versions...), Version{Path: archive, Version: old.Version, SHA256: hash, SavedAt: time.Now().UTC()})
	// Save the backup reference before changing the live file, so a restart can find it.
	m.Apps[index].Versions = history
	if err = m.Save(); err != nil {
		m.Apps[index] = old
		os.Remove(archive)
		return App{}, err
	}
	if err = os.Rename(staged, old.Path); err != nil {
		return App{}, err
	}
	installed, err := m.register(old.Path)
	if err == nil {
		installed.Versions = history
		if installed.UpdateInfo == "" {
			installed.UpdateInfo = old.UpdateInfo
		}
		installed.Update = offered
		installed.Update.Status = "up-to-date"
		installed.Update.CheckedAt = time.Now().UTC()
		m.Apps[index] = installed
		err = m.Save()
	}
	if err != nil {
		restore, _, restoreErr := copyVersion(archive, filepath.Dir(old.Path), ".appholder-restore-*")
		if restoreErr == nil {
			restoreErr = os.Rename(restore, old.Path)
			if restoreErr != nil {
				os.Remove(restore)
			}
		}
		if restoreErr != nil {
			return App{}, fmt.Errorf("activation failed: %v; restore failed: %v; backup: %s", err, restoreErr, archive)
		}
		restored, restoreErr := m.register(old.Path)
		if restoreErr == nil {
			restored.Versions = history
			m.Apps[index] = restored
			restoreErr = m.Save()
		}
		if restoreErr != nil {
			return App{}, fmt.Errorf("activation failed: %v; file restored but library repair failed: %v", err, restoreErr)
		}
		return App{}, err
	}
	if err = m.PruneVersions(); err != nil {
		return App{}, fmt.Errorf("version installed but history pruning failed: %w", err)
	}
	for _, current := range m.Apps {
		if current.ID == id {
			installed = current
			break
		}
	}
	return installed, nil
}

// Rollback restores the most recently saved version and archives the current one.
func (m *Manager) Rollback(id string) (App, error) {
	var app App
	for _, a := range m.Apps {
		if a.ID == id {
			app = a
			break
		}
	}
	if len(app.Versions) == 0 {
		return App{}, fmt.Errorf("no saved version available")
	}
	return m.RestoreVersion(id, len(app.Versions)-1)
}

// RestoreVersion activates a selected saved version and archives the current file.
func (m *Manager) RestoreVersion(id string, index int) (App, error) {
	var app App
	for _, a := range m.Apps {
		if a.ID == id {
			app = a
			break
		}
	}
	if index < 0 || index >= len(app.Versions) {
		return App{}, fmt.Errorf("saved version no longer exists")
	}
	version := app.Versions[index]
	staged, hash, err := copyVersion(version.Path, filepath.Dir(app.Path), ".appholder-rollback-*")
	if err != nil {
		return App{}, err
	}
	defer os.Remove(staged)
	if hash != version.SHA256 {
		return App{}, fmt.Errorf("saved version checksum does not match")
	}
	installed, err := m.Activate(id, app.Fingerprint, staged, update.Result{})
	if err != nil {
		return App{}, err
	}
	// An unknown status schedules a fresh check instead of claiming the older version is current.
	for i, a := range m.Apps {
		if a.ID == id {
			m.Apps[i].Update = update.Result{}
			m.Apps[i].AutoUpdate = false
			installed = m.Apps[i]
			break
		}
	}
	return installed, m.Save()
}
