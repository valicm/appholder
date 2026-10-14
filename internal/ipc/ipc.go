// Package ipc provides private, per-user daemon communication over a Unix socket.
package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"appholder/internal/manager"
	"appholder/internal/notify"
	"appholder/internal/update"
)

type Service struct {
	automaticCancels map[string]context.CancelFunc
	jobsMu           sync.Mutex
	BuildVersion     string
	Checker          *update.Checker
	workers          sync.WaitGroup
	slots            chan struct{}
	Manager          *manager.Manager
	mu               sync.Mutex
	checking         bool
	operations       map[string]Operation
	Context          context.Context
}

func SocketPath(m *manager.Manager) string { return filepath.Join(m.StateDir, "daemon.sock") }
func (s *Service) Scan(previous map[string]string) (map[string]string, error) {
	s.mu.Lock()
	known := make(map[string]bool, len(s.Manager.Apps))
	for _, a := range s.Manager.Apps {
		known[a.ID] = true
	}
	next, err := s.Manager.Scan(previous)
	var added []string
	if err == nil {
		for _, a := range s.Manager.Apps {
			if !known[a.ID] {
				added = append(added, a.Name)
			}
		}
	}
	s.mu.Unlock()
	if len(added) > 0 {
		go notify.Added(added)
	}
	return next, err
}
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /apps/{id}/auto-update", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Enabled bool `json:"enabled"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid automatic update setting", 400)
			return
		}
		id := r.PathValue("id")
		s.mu.Lock()
		found := false
		for i, a := range s.Manager.Apps {
			if a.ID != id {
				continue
			}
			found = true
			if request.Enabled && (a.Pinned || a.UpdateInfo == "" || a.Update.Status == "unsupported") {
				s.mu.Unlock()
				http.Error(w, "unpin this version and configure a supported update source first", 409)
				return
			}
			s.Manager.Apps[i].AutoUpdate = request.Enabled
			if err := s.Manager.Save(); err != nil {
				s.Manager.Apps[i] = a
				s.mu.Unlock()
				http.Error(w, err.Error(), 500)
				return
			}
			break
		}
		s.mu.Unlock()
		if !found {
			http.Error(w, "unknown application", 404)
			return
		}
		if request.Enabled {
			ctx := s.Context
			if ctx == nil {
				ctx = context.Background()
			}
			_ = s.startAutomatic(ctx, id)
		} else {
			s.cancelAutomatic(id)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /settings", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Directories  []string `json:"directories"`
			KeepVersions int      `json:"keep_versions"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16384)
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid folders", 400)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.jobsMu.Lock()
		busy := false
		for _, job := range s.operations {
			if job.Status == "running" {
				busy = true
			}
		}
		s.jobsMu.Unlock()
		if busy {
			http.Error(w, "wait for active tasks before changing folders", 409)
			return
		}
		if err := s.Manager.SetDirectories(request.Directories); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if request.KeepVersions != 0 {
			if err := s.Manager.SetKeepVersions(request.KeepVersions); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.Manager.Config)
	})
	for _, action := range []string{"remove", "uninstall"} {
		mux.HandleFunc("POST /apps/{id}/"+action, func(w http.ResponseWriter, r *http.Request) {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.jobsMu.Lock()
			busy := s.operations[r.PathValue("id")].Status == "running"
			s.jobsMu.Unlock()
			if busy {
				http.Error(w, "wait for this application's active task", 409)
				return
			}
			if err := s.Manager.Remove(r.PathValue("id"), action == "uninstall"); err != nil {
				http.Error(w, err.Error(), 409)
				return
			}
			s.jobsMu.Lock()
			delete(s.operations, r.PathValue("id"))
			s.jobsMu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		})
	}

	mux.HandleFunc("POST /updates/install", func(w http.ResponseWriter, r *http.Request) {
		ctx := s.Context
		if ctx == nil {
			ctx = context.Background()
		}
		result := s.UpdateAll(ctx)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("POST /apps/{id}/pin", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Pinned bool `json:"pinned"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid pin setting", 400)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		for i, a := range s.Manager.Apps {
			if a.ID == r.PathValue("id") {
				s.Manager.Apps[i].Pinned = request.Pinned
				if request.Pinned {
					s.Manager.Apps[i].AutoUpdate = false
				}
				if err := s.Manager.Save(); err != nil {
					s.Manager.Apps[i] = a
					http.Error(w, err.Error(), 500)
					return
				}
				if request.Pinned {
					s.cancelAutomatic(a.ID)
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		http.Error(w, "unknown application", 404)
	})
	mux.HandleFunc("POST /apps/{id}/sandbox", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Enabled      bool `json:"enabled"`
			AllowNetwork bool `json:"allow_network"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid sandbox setting", 400)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		for i, app := range s.Manager.Apps {
			if app.ID == r.PathValue("id") {
				s.Manager.Apps[i].Sandboxed = request.Enabled
				s.Manager.Apps[i].SandboxNetwork = request.AllowNetwork
				if err := s.Manager.Save(); err != nil {
					s.Manager.Apps[i] = app
					http.Error(w, err.Error(), 500)
					return
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		http.Error(w, "unknown application", 404)
	})
	mux.HandleFunc("GET /settings", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"directories": s.Manager.Config.Directories, "backup_directory": filepath.Join(s.Manager.StateDir, "versions"), "keep_versions": s.Manager.Config.KeepVersions, "gui_theme": s.Manager.Config.GUITheme, "default_auto_update": s.Manager.Config.DefaultAutoUpdate, "default_sandboxed": s.Manager.Config.DefaultSandboxed, "default_sandbox_network": s.Manager.Config.DefaultSandboxNetwork})
	})
	mux.HandleFunc("POST /settings/theme", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Theme string `json:"theme"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&request); err != nil {
			http.Error(w, "invalid theme", 400)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := s.Manager.SetGUITheme(request.Theme); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /settings/defaults", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			AutoUpdate     bool `json:"default_auto_update"`
			Sandboxed      bool `json:"default_sandboxed"`
			SandboxNetwork bool `json:"default_sandbox_network"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&request); err != nil {
			http.Error(w, "invalid application defaults", 400)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := s.Manager.SetAppDefaults(request.AutoUpdate, request.Sandboxed, request.SandboxNetwork); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Info{Protocol: ProtocolVersion, Version: s.BuildVersion, PID: os.Getpid()})
	})
	mux.HandleFunc("GET /operations", func(w http.ResponseWriter, r *http.Request) {
		s.jobsMu.Lock()
		defer s.jobsMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.operations)
	})
	for _, action := range []string{"install", "rollback"} {
		mux.HandleFunc("POST /apps/{id}/"+action, func(w http.ResponseWriter, r *http.Request) {
			ctx := s.Context
			if ctx == nil {
				ctx = context.Background()
			}
			if err := s.startOperation(ctx, r.PathValue("id"), action == "rollback"); err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{"status": "running", "id": r.PathValue("id")})
		})
	}
	mux.HandleFunc("POST /apps/{id}/versions/{index}/restore", func(w http.ResponseWriter, r *http.Request) {
		index, err := strconv.Atoi(r.PathValue("index"))
		if err != nil || index < 0 {
			http.Error(w, "invalid saved version", http.StatusBadRequest)
			return
		}
		ctx := s.Context
		if ctx == nil {
			ctx = context.Background()
		}
		if err = s.startRestoreVersion(ctx, r.PathValue("id"), index); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("POST /updates/check", func(w http.ResponseWriter, r *http.Request) {
		ctx := s.Context
		if ctx == nil {
			ctx = context.Background()
		}
		started := s.CheckUpdates(ctx, true)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]bool{"started": started})
	})
	mux.HandleFunc("GET /apps", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.Manager.Apps)
	})
	mux.HandleFunc("POST /integrate", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Path string `json:"path"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16384)
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Path == "" || !filepath.IsAbs(request.Path) {
			http.Error(w, "an absolute path is required", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		a, err := s.Manager.Integrate(request.Path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(a)
	})
	return mux
}

// Listen must be called while holding the daemon's exclusive library lock.
func (s *Service) Listen() (net.Listener, error) {
	path := SocketPath(s.Manager)
	if st, err := os.Lstat(path); err == nil {
		if st.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("socket path is occupied by a non-socket file")
		}
		if err = os.Remove(path); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	return listener, nil
}
func Client(path string) *http.Client {
	return &http.Client{Timeout: 2 * time.Minute, Transport: &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", path)
	}}}
}

func (s *Service) Wait() { s.workers.Wait() }
func (s *Service) checker() *update.Checker {
	if s.Checker != nil {
		return s.Checker
	}
	return update.New()
}
