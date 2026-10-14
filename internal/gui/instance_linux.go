//go:build linux

package gui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type desktopRequest struct {
	Path string `json:"path"`
	Tray bool   `json:"tray"`
}

type desktopInstance struct {
	closeOnce sync.Once
	lock      *os.File
	listener  net.Listener
	server    *http.Server
}

// claimDesktop forwards to the owner, or holds the lock until the desktop exits.
// The lock also protects stale socket removal and simultaneous startup.
func claimDesktop(ctx context.Context, daemonSocket string, request desktopRequest, deliver func(desktopRequest)) (*desktopInstance, error) {
	socket := filepath.Join(filepath.Dir(daemonSocket), "desktop.sock")
	lock, err := os.OpenFile(filepath.Join(filepath.Dir(socket), "desktop.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			lock.Close()
			return nil, err
		}
		transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}}
		client := &http.Client{Transport: transport, Timeout: time.Second}
		body, _ := json.Marshal(request)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://desktop/activate", bytes.NewReader(body))
		response, forwardErr := client.Do(req)
		transport.CloseIdleConnections()
		if forwardErr == nil {
			response.Body.Close()
			lock.Close()
			if response.StatusCode != http.StatusAccepted {
				return nil, fmt.Errorf("desktop rejected activation: %s", response.Status)
			}
			return nil, nil
		}
		select {
		case <-ctx.Done():
			lock.Close()
			return nil, fmt.Errorf("existing desktop did not respond: %w", ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
	instance := &desktopInstance{lock: lock}
	if err = os.Remove(socket); err != nil && !os.IsNotExist(err) {
		instance.Close()
		return nil, err
	}
	instance.listener, err = net.Listen("unix", socket)
	if err != nil {
		instance.Close()
		return nil, err
	}
	if err = os.Chmod(socket, 0600); err != nil {
		instance.Close()
		return nil, err
	}
	instance.server = &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/activate" {
			http.NotFound(w, r)
			return
		}
		var incoming desktopRequest
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&incoming) != nil {
			http.Error(w, "invalid activation", 400)
			return
		}
		deliver(incoming)
		w.WriteHeader(http.StatusAccepted)
	})}
	go instance.server.Serve(instance.listener)
	return instance, nil
}

func (i *desktopInstance) Close() {
	i.closeOnce.Do(func() {
		if i.server != nil {
			i.server.Close()
		} else if i.listener != nil {
			i.listener.Close()
		}
		if i.lock != nil {
			syscall.Flock(int(i.lock.Fd()), syscall.LOCK_UN)
			i.lock.Close()
		}
	})
}
