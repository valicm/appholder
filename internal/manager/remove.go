package manager

import (
	"fmt"
	"os"
	"path/filepath"
)

type RemovedApp struct {
	App    App  `json:"app"`
	Ignore bool `json:"ignore"`
}

func (m *Manager) saveRemoved() error {
	return writeJSON(filepath.Join(m.StateDir, "removed.json"), m.Removed)
}

// Remove keeps saved versions. Unregistering excludes the retained file from scans.
func (m *Manager) Remove(id string, uninstall bool) error {
	index := -1
	for i, a := range m.Apps {
		if a.ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("unknown application")
	}
	app := m.Apps[index]
	if uninstall {
		actual, err := fingerprint(app.Path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && actual != app.Fingerprint {
			return fmt.Errorf("file changed; refresh before uninstalling")
		}
	}
	if m.Removed == nil {
		m.Removed = map[string]RemovedApp{}
	}
	previous, hadPrevious := m.Removed[app.Path]
	m.Removed[app.Path] = RemovedApp{App: app, Ignore: !uninstall}
	if err := m.saveRemoved(); err != nil {
		if hadPrevious {
			m.Removed[app.Path] = previous
		} else {
			delete(m.Removed, app.Path)
		}
		return err
	}
	if uninstall {
		if err := os.Remove(app.Path); err != nil && !os.IsNotExist(err) {
			if hadPrevious {
				m.Removed[app.Path] = previous
			} else {
				delete(m.Removed, app.Path)
			}
			restoreErr := m.saveRemoved()
			if restoreErr != nil {
				return fmt.Errorf("uninstall failed: %v; exclusion repair failed: %v", err, restoreErr)
			}
			return err
		}
	}
	next := append([]App(nil), m.Apps[:index]...)
	next = append(next, m.Apps[index+1:]...)
	m.Apps = next
	if err := m.Save(); err != nil {
		return fmt.Errorf("removal recorded, but library save failed: %w", err)
	}
	for _, path := range []string{filepath.Join(m.DesktopDir, "appholder-"+app.ID+".desktop"), filepath.Join(m.StateDir, "icons", app.ID+".png")} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("application removed, but integration cleanup failed: %w", err)
		}
	}
	return nil
}
