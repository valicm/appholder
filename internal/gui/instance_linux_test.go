//go:build linux

package gui

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestDesktopForwardAndRestart(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "daemon.sock")
	received := make(chan desktopRequest, 4)
	owner, err := claimDesktop(context.Background(), socket, desktopRequest{}, func(r desktopRequest) { received <- r })
	if err != nil || owner == nil {
		t.Fatalf("claim: %v", err)
	}
	defer owner.Close()
	for _, request := range []desktopRequest{{Path: "/tmp/My App.AppImage"}, {}, {Tray: true}} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		second, err := claimDesktop(ctx, socket, request, func(desktopRequest) { t.Error("secondary became owner") })
		cancel()
		if err != nil || second != nil {
			t.Fatalf("forward: %v", err)
		}
		if got := <-received; got != request {
			t.Fatalf("got %+v want %+v", got, request)
		}
	}
	info, err := os.Stat(filepath.Join(filepath.Dir(socket), "desktop.sock"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("socket permissions: %v %v", info, err)
	}
	owner.Close()
	next, err := claimDesktop(context.Background(), socket, desktopRequest{}, func(desktopRequest) {})
	if err != nil || next == nil {
		t.Fatalf("restart: %v", err)
	}
	next.Close()
}

func TestDesktopSimultaneousStartupAndStaleSocket(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "daemon.sock")
	stale, err := net.Listen("unix", filepath.Join(filepath.Dir(socket), "desktop.sock"))
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	stale.Close()
	var wg sync.WaitGroup
	owners := make(chan *desktopInstance, 8)
	errors := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			owner, err := claimDesktop(ctx, socket, desktopRequest{Tray: true}, func(desktopRequest) {})
			if err != nil {
				errors <- err
			}
			if owner != nil {
				owners <- owner
			}
		}()
	}
	wg.Wait()
	close(owners)
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	count := 0
	for owner := range owners {
		count++
		owner.Close()
	}
	if count != 1 {
		t.Fatalf("owners = %d", count)
	}
}
