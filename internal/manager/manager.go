package manager

import (
	"appholder/internal/metadata"
	"appholder/internal/update"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Config struct {
	Directories           []string `json:"directories"`
	KeepVersions          int      `json:"keep_versions"`
	GUITheme              string   `json:"gui_theme,omitempty"`
	DefaultAutoUpdate     bool     `json:"default_auto_update,omitempty"`
	DefaultSandboxed      bool     `json:"default_sandboxed,omitempty"`
	DefaultSandboxNetwork bool     `json:"default_sandbox_network,omitempty"`
}
type App struct {
	AutoUpdate       bool          `json:"auto_update,omitempty"`
	Sandboxed        bool          `json:"sandboxed,omitempty"`
	SandboxNetwork   bool          `json:"sandbox_network,omitempty"`
	Metadata         metadata.Info `json:"metadata"`
	MetadataRevision int           `json:"metadata_revision"`
	Pinned           bool          `json:"pinned,omitempty"`
	Versions         []Version     `json:"versions,omitempty"`
	LastNotifiedSHA1 string        `json:"last_notified_sha1,omitempty"`
	Update           update.Result `json:"update"`
	ID               string        `json:"id"`
	Name             string        `json:"name"`
	Path             string        `json:"path"`
	RegisteredAt     time.Time     `json:"registered_at"`
	Version          string        `json:"version,omitempty"`
	UpdateInfo       string        `json:"update_info,omitempty"`
	MetadataError    string        `json:"metadata_error,omitempty"`
	IconPath         string        `json:"icon_path,omitempty"`
	Fingerprint      string        `json:"fingerprint,omitempty"`
}

func (a App) DisplayName() string {
	if a.Metadata.Name != "" {
		return a.Metadata.Name
	}
	if a.Path != "" {
		return displayNameFromFilename(a.Path)
	}
	return a.Name
}

type Manager struct {
	Removed              map[string]RemovedApp
	ConfigPath           string
	Config               Config
	StateDir, DesktopDir string
	Apps                 []App
}

func Load() (*Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	configRoot := os.Getenv("XDG_CONFIG_HOME")
	if configRoot == "" {
		configRoot = filepath.Join(home, ".config")
	}
	stateRoot := os.Getenv("XDG_STATE_HOME")
	if stateRoot == "" {
		stateRoot = filepath.Join(home, ".local", "state")
	}
	dataRoot := os.Getenv("XDG_DATA_HOME")
	if dataRoot == "" {
		dataRoot = filepath.Join(home, ".local", "share")
	}
	m := &Manager{Config: Config{Directories: []string{filepath.Join(home, "Applications")}, KeepVersions: 3, GUITheme: "system"}, StateDir: filepath.Join(stateRoot, "appholder"), DesktopDir: filepath.Join(dataRoot, "applications")}
	configPath := filepath.Join(configRoot, "appholder", "config.json")
	m.ConfigPath = configPath
	b, err := os.ReadFile(configPath)
	if err == nil {
		if err = json.Unmarshal(b, &m.Config); err != nil {
			return nil, fmt.Errorf("configuration: %w", err)
		}
	} else if os.IsNotExist(err) {
		if err = writeJSON(configPath, m.Config); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	if m.Config.KeepVersions == 0 {
		m.Config.KeepVersions = 3
	}
	if m.Config.GUITheme == "" {
		m.Config.GUITheme = "system"
	}
	if m.Config.GUITheme != "system" && m.Config.GUITheme != "light" && m.Config.GUITheme != "dark" {
		return nil, fmt.Errorf("gui_theme must be system, light, or dark")
	}
	if m.Config.KeepVersions < 1 || m.Config.KeepVersions > 100 {
		return nil, fmt.Errorf("keep_versions must be between 1 and 100")
	}
	if len(m.Config.Directories) == 0 {
		return nil, fmt.Errorf("configure at least one directory")
	}
	for i, p := range m.Config.Directories {
		if p == "~" {
			p = home
		} else if strings.HasPrefix(p, "~/") {
			p = filepath.Join(home, p[2:])
		}
		if !filepath.IsAbs(p) {
			return nil, fmt.Errorf("directory must be absolute: %s", p)
		}
		m.Config.Directories[i] = filepath.Clean(p)
		if err = os.MkdirAll(p, 0755); err != nil {
			return nil, err
		}
	}
	b, err = os.ReadFile(filepath.Join(m.StateDir, "library.json"))
	if err == nil {
		err = json.Unmarshal(b, &m.Apps)
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	b, err = os.ReadFile(filepath.Join(m.StateDir, "removed.json"))
	if err == nil {
		err = json.Unmarshal(b, &m.Removed)
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return m, nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(b, '\n'), 0600)
}
func atomicWrite(path string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".appholder-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// Validate reads the AppImage marker without executing the untrusted file.
func Validate(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("not a regular file")
	}
	var header [11]byte
	if _, err = io.ReadFull(f, header[:]); err != nil {
		return err
	}
	if string(header[:4]) != "\x7fELF" || header[8] != 'A' || header[9] != 'I' || (header[10] != 1 && header[10] != 2) {
		return fmt.Errorf("not an AppImage")
	}
	return nil
}

func desktopQuote(s string) string {
	s = strings.ReplaceAll(s, "%", "%%")
	r := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "`", "\\`", "$", "\\$")
	return "\"" + r.Replace(s) + "\""
}

func displayNameFromFilename(path string) string {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if cut := strings.LastIndexAny(name, "-_"); cut >= 0 {
		suffix := name[cut+1:]
		if len(suffix) >= 24 {
			hex := true
			for _, r := range suffix {
				if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
					hex = false
					break
				}
			}
			if hex {
				name = name[:cut]
			}
		}
	}
	for _, suffix := range []string{"-linux-x86_64", "-linux-aarch64", "-linux-arm64", "_linux-x86_64", "_linux-aarch64", "_linux-arm64", "-desktop"} {
		name = strings.TrimSuffix(name, suffix)
	}
	if strings.EqualFold(name, "opencode") {
		return "OpenCode"
	}
	return name
}

func (m *Manager) register(path string) (App, error) {
	if strings.ContainsAny(path, "\n\r") {
		return App{}, fmt.Errorf("unsupported newline in path")
	}
	if err := Validate(path); err != nil {
		return App{}, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return App{}, err
	}
	if err = os.Chmod(path, st.Mode().Perm()|0100); err != nil {
		return App{}, err
	}
	sum := sha256.Sum256([]byte(path))
	id := hex.EncodeToString(sum[:12])
	name := displayNameFromFilename(path)
	a := App{ID: id, Name: name, Path: path, RegisteredAt: time.Now().UTC(), AutoUpdate: m.Config.DefaultAutoUpdate, Sandboxed: m.Config.DefaultSandboxed, SandboxNetwork: m.Config.DefaultSandboxNetwork}
	var previous *App
	for _, old := range m.Apps {
		if old.ID == id {
			oldCopy := old
			previous = &oldCopy
			a.RegisteredAt = old.RegisteredAt
			a.Versions = old.Versions
			a.Pinned = old.Pinned
			a.AutoUpdate = old.AutoUpdate
			a.Sandboxed, a.SandboxNetwork = old.Sandboxed, old.SandboxNetwork
		}
	}
	if previous == nil {
		if removed, ok := m.Removed[path]; ok {
			old := removed.App
			previous = &old
			a.RegisteredAt = old.RegisteredAt
			a.Versions = old.Versions
			a.Pinned = old.Pinned
			a.AutoUpdate = old.AutoUpdate
			a.Sandboxed, a.SandboxNetwork = old.Sandboxed, old.SandboxNetwork
		}
	}
	a.Fingerprint = fmt.Sprintf("%d:%d", st.Size(), st.ModTime().UnixNano())
	info, metadataErr := metadata.Read(path)
	if info.Name != "" {
		a.Name = info.Name
	}
	a.Version, a.UpdateInfo = info.Version, info.UpdateInfo
	a.Metadata = info
	a.Metadata.PNG = nil
	a.MetadataRevision = metadata.Revision
	if previous != nil && previous.Fingerprint == a.Fingerprint && previous.UpdateInfo == a.UpdateInfo {
		a.Update = previous.Update
		a.LastNotifiedSHA1 = previous.LastNotifiedSHA1
	}
	if metadataErr != nil {
		a.MetadataError = metadataErr.Error()
	}
	icon := "application-x-executable"
	if len(info.PNG) > 0 {
		a.IconPath = filepath.Join(m.StateDir, "icons", id+".png")
		if err = atomicWrite(a.IconPath, info.PNG, 0600); err != nil {
			return App{}, err
		}
		icon = a.IconPath
	}
	displayName := a.Name
	if a.Version != "" {
		displayName += " (" + a.Version + ")"
	} else {
		for _, other := range m.Apps {
			if other.ID != a.ID && other.Name == a.Name {
				displayName += " [" + filepath.Base(path) + "]"
				break
			}
		}
	}
	name = strings.NewReplacer("\\", "\\\\", "\n", " ", "\r", " ", "\t", " ").Replace(displayName)
	entry := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=%s\nExec=%s\nIcon=%s\nTerminal=false\nCategories=Utility;\n", name, desktopQuote(path), icon)
	if err = atomicWrite(filepath.Join(m.DesktopDir, "appholder-"+id+".desktop"), []byte(entry), 0644); err != nil {
		return App{}, err
	}
	return a, nil
}

// Scan integrates only regular files unchanged across two observations.
func (m *Manager) Scan(previous map[string]string) (map[string]string, error) {
	current := map[string]string{}
	var next []App
	for _, dir := range m.Config.Directories {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return previous, err
		}
		for _, e := range entries {
			if !strings.EqualFold(filepath.Ext(e.Name()), ".appimage") || e.Type()&os.ModeSymlink != 0 {
				continue
			}
			st, err := e.Info()
			if err != nil {
				return previous, err
			}
			if !st.Mode().IsRegular() {
				continue
			}
			p := filepath.Join(dir, e.Name())
			if removed, ok := m.Removed[p]; ok && removed.Ignore {
				continue
			}
			fingerprint := fmt.Sprintf("%d:%d", st.Size(), st.ModTime().UnixNano())
			current[p] = fingerprint
			if previous[p] != fingerprint {
				for _, a := range m.Apps {
					if a.Path == p {
						next = append(next, a)
					}
				}
				continue
			}
			cached := false
			for _, old := range m.Apps {
				if old.Path == p && old.Fingerprint == fingerprint && old.MetadataRevision == metadata.Revision {
					if _, err := os.Stat(filepath.Join(m.DesktopDir, "appholder-"+old.ID+".desktop")); err == nil {
						next = append(next, old)
						cached = true
					}
					break
				}
			}
			if cached {
				continue
			}
			a, err := m.register(p)
			if err != nil {
				continue
			}
			next = append(next, a)
		}
	}
	retained := make(map[string]bool, len(next))
	for _, app := range next {
		retained[app.ID] = true
	}
	for _, old := range m.Apps {
		if !retained[old.ID] {
			if m.Removed == nil {
				m.Removed = map[string]RemovedApp{}
			}
			if _, exists := m.Removed[old.Path]; !exists {
				m.Removed[old.Path] = RemovedApp{App: old}
				if err := m.saveRemoved(); err != nil {
					return previous, err
				}
			}
			if err := os.Remove(filepath.Join(m.DesktopDir, "appholder-"+old.ID+".desktop")); err != nil && !os.IsNotExist(err) {
				return previous, err
			}
			if old.IconPath != "" {
				if err := os.Remove(filepath.Join(m.StateDir, "icons", old.ID+".png")); err != nil && !os.IsNotExist(err) {
					return previous, err
				}
			}
		}
	}
	sort.Slice(next, func(i, j int) bool { return next[i].Path < next[j].Path })
	m.Apps = next
	return current, writeJSON(filepath.Join(m.StateDir, "library.json"), m.Apps)
}

func (m *Manager) Integrate(source string) (App, error) {
	source, err := filepath.Abs(source)
	if err != nil {
		return App{}, err
	}
	if err = Validate(source); err != nil {
		return App{}, err
	}
	dest := filepath.Join(m.Config.Directories[0], filepath.Base(source))
	if source != dest {
		dest, err = importFile(source, m.Config.Directories[0])
		if err != nil {
			return App{}, err
		}
	}

	a, err := m.register(dest)
	if err != nil {
		return App{}, err
	}
	found := false
	for i, old := range m.Apps {
		if old.ID == a.ID {
			m.Apps[i] = a
			found = true
		}
	}
	if !found {
		m.Apps = append(m.Apps, a)
	}
	if _, ok := m.Removed[dest]; ok {
		delete(m.Removed, dest)
		if err = m.saveRemoved(); err != nil {
			return App{}, err
		}
	}
	return a, writeJSON(filepath.Join(m.StateDir, "library.json"), m.Apps)
}

func (m *Manager) Save() error { return writeJSON(filepath.Join(m.StateDir, "library.json"), m.Apps) }
