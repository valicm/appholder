package ipc

import (
	"context"
	"fmt"
	"os"
	"time"

	"appholder/internal/manager"
	"appholder/internal/notify"
)

// CheckUpdates snapshots the library; network and hashing never hold its mutex.
func (s *Service) CheckUpdates(ctx context.Context, force bool) bool {
	s.mu.Lock()
	if s.checking || ctx.Err() != nil {
		s.mu.Unlock()
		return false
	}
	var apps []manager.App
	for _, a := range s.Manager.Apps {
		if force || a.Update.CheckedAt.IsZero() || time.Since(a.Update.CheckedAt) >= 6*time.Hour {
			apps = append(apps, a)
		}
	}
	if len(apps) == 0 {
		s.mu.Unlock()
		return false
	}
	s.checking = true
	s.workers.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.workers.Done()
		defer func() { s.mu.Lock(); s.checking = false; s.mu.Unlock() }()
		checker := s.checker()
		for _, a := range apps {
			if ctx.Err() != nil {
				return
			}
			result := checker.Check(ctx, a.Path, a.UpdateInfo)
			if ctx.Err() != nil {
				return
			}
			var names []string
			automatic := false
			s.mu.Lock()
			for i, current := range s.Manager.Apps {
				if current.ID != a.ID || current.Fingerprint != a.Fingerprint {
					continue
				}
				st, err := os.Stat(a.Path)
				if err != nil || fmt.Sprintf("%d:%d", st.Size(), st.ModTime().UnixNano()) != a.Fingerprint {
					continue
				}
				s.Manager.Apps[i].Update = result
				shouldNotify := result.Status == "available" && current.LastNotifiedSHA1 != result.SHA1
				if shouldNotify {
					s.Manager.Apps[i].LastNotifiedSHA1 = result.SHA1
				}
				if err = s.Manager.Save(); err != nil {
					s.Manager.Apps[i] = current
					break
				}
				automatic = result.Status == "available" && current.AutoUpdate && !current.Pinned
				if shouldNotify && !automatic {
					names = append(names, current.Name)
				}
			}
			s.mu.Unlock()
			if automatic {
				_ = s.startAutomatic(ctx, a.ID)
			}
			if len(names) > 0 {
				notify.Updates(names)
			}
		}
	}()
	return true
}
