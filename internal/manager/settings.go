package manager

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (m *Manager) SetDirectories(directories []string) error {
	if len(directories) == 0 {
		return fmt.Errorf("configure at least one watched folder")
	}
	if len(directories) > 32 {
		return fmt.Errorf("at most 32 watched folders are supported")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	var normalized []string
	seen := map[string]bool{}
	for _, path := range directories {
		path = strings.TrimSpace(path)
		if path == "~" {
			path = home
		} else if strings.HasPrefix(path, "~/") {
			path = filepath.Join(home, path[2:])
		}
		if !filepath.IsAbs(path) || strings.ContainsAny(path, "\r\n\x00") {
			return fmt.Errorf("folder must be an absolute path: %s", path)
		}
		path = filepath.Clean(path)
		if seen[path] {
			continue
		}
		seen[path] = true
		if err = os.MkdirAll(path, 0755); err != nil {
			return err
		}
		normalized = append(normalized, path)
	}
	if m.ConfigPath == "" {
		return fmt.Errorf("configuration path is unavailable")
	}
	config := m.Config
	config.Directories = normalized
	if err = writeJSON(m.ConfigPath, config); err != nil {
		return err
	}
	m.Config = config
	return nil
}

func (m *Manager) SetKeepVersions(count int) error {
	if count < 1 || count > 100 {
		return fmt.Errorf("keep between 1 and 100 saved versions")
	}
	config := m.Config
	config.KeepVersions = count
	if err := writeJSON(m.ConfigPath, config); err != nil {
		return err
	}
	m.Config = config
	return m.PruneVersions()
}

// PruneVersions removes oldest saved copies only after the shorter library is persisted.
func (m *Manager) PruneVersions() error {
	keep := m.Config.KeepVersions
	if keep == 0 {
		keep = 3
	}
	oldApps := make([][]Version, len(m.Apps))
	for i := range m.Apps {
		oldApps[i] = append([]Version(nil), m.Apps[i].Versions...)
	}
	oldRemoved := make(map[string]RemovedApp, len(m.Removed))
	for path, removed := range m.Removed {
		oldRemoved[path] = removed
	}
	var remove []Version
	changedRemoved := false
	for i := range m.Apps {
		m.Apps[i].Versions, remove = trimVersions(m.Apps[i].Versions, keep, remove)
	}
	for path, removed := range m.Removed {
		before := len(removed.App.Versions)
		removed.App.Versions, remove = trimVersions(removed.App.Versions, keep, remove)
		if len(removed.App.Versions) != before {
			changedRemoved = true
		}
		m.Removed[path] = removed
	}
	if err := m.Save(); err != nil {
		for i := range m.Apps {
			m.Apps[i].Versions = oldApps[i]
		}
		m.Removed = oldRemoved
		return err
	}
	if changedRemoved {
		if err := m.saveRemoved(); err != nil {
			for i := range m.Apps {
				m.Apps[i].Versions = oldApps[i]
			}
			m.Removed = oldRemoved
			_ = m.Save()
			return err
		}
	}
	return removeArchivedVersions(m.StateDir, remove)
}

func trimVersions(versions []Version, keep int, remove []Version) ([]Version, []Version) {
	if len(versions) <= keep {
		return versions, remove
	}
	remove = append(remove, versions[:len(versions)-keep]...)
	return append([]Version(nil), versions[len(versions)-keep:]...), remove
}

func removeArchivedVersions(stateDir string, versions []Version) error {
	root, err := filepath.EvalSymlinks(filepath.Join(stateDir, "versions"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, version := range versions {
		path, err := filepath.Abs(version.Path)
		if err != nil {
			return err
		}
		parent, err := filepath.EvalSymlinks(filepath.Dir(path))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		relative, err := filepath.Rel(root, parent)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			continue
		}
		if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
