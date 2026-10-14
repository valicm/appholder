package ipc

import (
	"context"
	"errors"
	"fmt"
	"os"

	"appholder/internal/manager"
	"appholder/internal/notify"
)

type Operation struct {
	Stage      string `json:"stage,omitempty"`
	Downloaded int64  `json:"downloaded,omitempty"`
	Total      int64  `json:"total,omitempty"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
}

func (s *Service) startOperation(ctx context.Context, id string, rollback bool) error {
	return s.startOperationMode(ctx, id, rollback, false, nil)
}
func (s *Service) startAutomatic(ctx context.Context, id string) error {
	return s.startOperationMode(ctx, id, false, true, nil)
}
func (s *Service) startRestoreVersion(ctx context.Context, id string, index int) error {
	return s.startOperationMode(ctx, id, true, false, &index)
}
func (s *Service) startOperationMode(ctx context.Context, id string, rollback, automatic bool, restoreIndex *int) error {
	s.mu.Lock()
	if ctx.Err() != nil {
		s.mu.Unlock()
		return ctx.Err()
	}
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if s.operations == nil {
		s.operations = map[string]Operation{}
	}
	if s.operations[id].Status == "running" {
		s.mu.Unlock()
		return fmt.Errorf("an operation is already running for this application")
	}
	var app manager.App
	for _, a := range s.Manager.Apps {
		if a.ID == id {
			app = a
			break
		}
	}
	if app.ID == "" {
		s.mu.Unlock()
		return fmt.Errorf("unknown application ID")
	}
	if automatic && (!app.AutoUpdate || app.Pinned) {
		s.mu.Unlock()
		return fmt.Errorf("automatic updates are disabled")
	}
	if rollback && len(app.Versions) == 0 {
		s.mu.Unlock()
		return fmt.Errorf("no saved version available")
	}
	if !rollback && app.Update.Status != "available" {
		s.mu.Unlock()
		return fmt.Errorf("check updates first; no available update is recorded")
	}
	if s.slots == nil {
		s.slots = make(chan struct{}, 2)
	}
	if automatic {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		if s.automaticCancels == nil {
			s.automaticCancels = map[string]context.CancelFunc{}
		}
		s.automaticCancels[id] = cancel
	}
	s.operations[id] = Operation{Status: "running", Stage: "queued"}
	s.workers.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.workers.Done()
		var err error
		select {
		case s.slots <- struct{}{}:
			s.progress(id, "checking", 0, 0)
			err = s.performOperation(ctx, app, rollback, automatic, restoreIndex)
			<-s.slots
		case <-ctx.Done():
			err = ctx.Err()
		}
		s.jobsMu.Lock()
		operation := s.operations[id]
		operation.Status = "completed"
		operation.Stage = "completed"
		if err != nil {
			operation.Status = "failed"
			operation.Stage = "failed"
			if automatic && (ctx.Err() != nil || errors.Is(err, context.Canceled)) {
				operation.Status = "cancelled"
				operation.Stage = "cancelled"
			}
			operation.Error = err.Error()
		}
		if automatic {
			if cancel := s.automaticCancels[id]; cancel != nil {
				cancel()
			}
			delete(s.automaticCancels, id)
		}
		s.operations[id] = operation
		s.jobsMu.Unlock()
		if automatic && operation.Status == "completed" {
			notify.Updated([]string{app.Name})
		}
	}()
	return nil
}
func (s *Service) performOperation(ctx context.Context, app manager.App, rollback, automatic bool, restoreIndex *int) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if rollback {
		s.progress(app.ID, "restoring", 0, 0)
		s.mu.Lock()
		defer s.mu.Unlock()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		for _, current := range s.Manager.Apps {
			if current.ID == app.ID && current.Fingerprint != app.Fingerprint {
				return fmt.Errorf("application changed before rollback")
			}
		}
		if restoreIndex != nil {
			_, err := s.Manager.RestoreVersion(app.ID, *restoreIndex)
			return err
		}
		_, err := s.Manager.Rollback(app.ID)
		return err
	}
	checker := s.checker()
	offer := checker.Check(ctx, app.Path, app.UpdateInfo)
	if offer.Status != "available" {
		return fmt.Errorf("update no longer available: %s %s", offer.Status, offer.Error)
	}
	s.progress(app.ID, "downloading", 0, offer.Length)
	staged, err := checker.DownloadProgress(ctx, app.Path, offer, func(done, total int64) {
		stage := "downloading"
		if done >= total {
			stage = "verifying"
		}
		s.progress(app.ID, stage, done, total)
	})
	if err != nil {
		return err
	}
	defer os.Remove(staged)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	s.progress(app.ID, "installing", offer.Length, offer.Length)
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if automatic {
		allowed := false
		for _, current := range s.Manager.Apps {
			if current.ID == app.ID {
				allowed = current.AutoUpdate && !current.Pinned
				break
			}
		}
		if !allowed {
			return context.Canceled
		}
	}
	_, err = s.Manager.Activate(app.ID, app.Fingerprint, staged, offer)
	return err
}

func (s *Service) progress(id, stage string, done, total int64) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	operation := s.operations[id]
	operation.Stage = stage
	operation.Downloaded = done
	operation.Total = total
	s.operations[id] = operation
}

type BatchResult struct {
	Started []string          `json:"started"`
	Skipped map[string]string `json:"skipped"`
}

func (s *Service) UpdateAll(ctx context.Context) BatchResult {
	result := BatchResult{Started: []string{}, Skipped: map[string]string{}}
	s.mu.Lock()
	var candidates []string
	for _, a := range s.Manager.Apps {
		if a.Update.Status == "available" {
			if a.Pinned {
				result.Skipped[a.ID] = "Version kept"
			} else {
				candidates = append(candidates, a.ID)
			}
		}
	}
	s.mu.Unlock()
	for _, id := range candidates {
		if err := s.startOperation(ctx, id, false); err != nil {
			result.Skipped[id] = err.Error()
		} else {
			result.Started = append(result.Started, id)
		}
	}
	return result
}

func (s *Service) cancelAutomatic(id string) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if cancel := s.automaticCancels[id]; cancel != nil {
		cancel()
	}
}
