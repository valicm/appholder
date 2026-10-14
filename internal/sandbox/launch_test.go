package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestCommandIsolatesNetworkAndUsesPrivateAppHome(t *testing.T) {
	if _, err := os.Stat("/usr/bin/bwrap"); err != nil {
		t.Skip("bubblewrap is not installed")
	}
	state := t.TempDir()
	app := filepath.Join(t.TempDir(), "Demo.AppImage")
	if err := os.WriteFile(app, []byte("#!/bin/sh\nprintf '%s\\n' \"$HOME\" > \"$HOME/sandbox-marker\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	command, err := Command(context.Background(), app, state, "0123456789abcdef01234567", false)
	if err != nil {
		t.Fatal(err)
	}
	args := command.Args
	if !slices.Contains(args, "--unshare-all") || !slices.Contains(args, "--tmpfs") || slices.Contains(args, "--share-net") {
		t.Fatalf("sandbox args do not isolate network: %q", args)
	}
	if !slices.Contains(args, filepath.Join(state, "sandboxes", "0123456789abcdef01234567", "home")) {
		t.Fatalf("private home missing: %q", args)
	}
	if slices.Index(args, app) > slices.Index(args, "/home") {
		t.Fatalf("AppImage source must be mounted before masking the host home: %q", args)
	}
	if err = command.Run(); err != nil {
		t.Fatalf("bubblewrap launch failed: %v", err)
	}
	profile := filepath.Join(state, "sandboxes", "0123456789abcdef01234567", "home")
	if _, err = os.Stat(filepath.Join(profile, "sandbox-marker")); err != nil {
		t.Fatalf("sandbox did not write to its private home: %v", err)
	}
	command, err = Command(context.Background(), app, state, "0123456789abcdef01234567", true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(command.Args, "--share-net") {
		t.Fatalf("network opt-in missing: %q", command.Args)
	}
}
