package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
)

// Command builds a best-effort Bubblewrap launch with a private persistent home.
func Command(ctx context.Context, appPath, stateDir, appID string, allowNetwork bool) (*exec.Cmd, error) {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, fmt.Errorf("sandbox requested, but bubblewrap (bwrap) is not installed")
	}
	if len(appID) != 24 {
		return nil, fmt.Errorf("invalid AppHolder application ID")
	}
	for _, char := range appID {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return nil, fmt.Errorf("invalid AppHolder application ID")
		}
	}
	appPath, err = filepath.Abs(appPath)
	if err != nil {
		return nil, err
	}
	current, err := user.Current()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	profile := filepath.Join(stateDir, "sandboxes", appID, "home")
	if err = os.MkdirAll(profile, 0700); err != nil {
		return nil, err
	}
	if err = os.Chmod(profile, 0700); err != nil {
		return nil, err
	}
	runtimeDir := filepath.Join(profile, ".local", "run")
	if err = os.MkdirAll(runtimeDir, 0700); err != nil {
		return nil, err
	}
	args := []string{"--ro-bind", "/", "/", "--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp", "--dir", "/tmp/appholder", "--ro-bind", appPath, "/tmp/appholder/AppImage", "--tmpfs", "/home", "--dir", filepath.Join("/home", current.Username), "--bind", profile, filepath.Join("/home", current.Username)}
	wayland := os.Getenv("WAYLAND_DISPLAY")
	if wayland != "" {
		socket := wayland
		if !filepath.IsAbs(socket) {
			socket = filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), wayland)
		}
		if st, statErr := os.Stat(socket); statErr == nil && st.Mode()&os.ModeSocket != 0 {
			target := filepath.Join("/home", current.Username, ".local", "run", "wayland-0")
			placeholder := filepath.Join(runtimeDir, "wayland-0")
			if f, openErr := os.OpenFile(placeholder, os.O_CREATE|os.O_RDWR, 0600); openErr == nil {
				f.Close()
				args = append(args, "--bind", socket, target)
			}
		}
	}
	for _, mount := range []string{"/var/tmp", "/mnt", "/media", "/run/dbus"} {
		if _, statErr := os.Stat(mount); statErr == nil {
			args = append(args, "--tmpfs", mount)
		}
	}
	if _, statErr := os.Stat(filepath.Join("/run/user", current.Uid)); statErr == nil {
		args = append(args, "--tmpfs", filepath.Join("/run/user", current.Uid))
	}
	args = append(args, "--unshare-all")
	if allowNetwork {
		args = append(args, "--share-net")
	}
	args = append(args, "--new-session", "--die-with-parent", "--clearenv")
	sandboxHome := filepath.Join("/home", current.Username)
	for key, value := range map[string]string{"HOME": sandboxHome, "PATH": "/usr/local/bin:/usr/bin:/bin", "APPIMAGE_EXTRACT_AND_RUN": "1", "XDG_CONFIG_HOME": filepath.Join(sandboxHome, ".config"), "XDG_DATA_HOME": filepath.Join(sandboxHome, ".local", "share"), "XDG_CACHE_HOME": filepath.Join(sandboxHome, ".cache"), "XDG_STATE_HOME": filepath.Join(sandboxHome, ".local", "state"), "XDG_RUNTIME_DIR": filepath.Join(sandboxHome, ".local", "run"), "LANG": os.Getenv("LANG"), "LC_ALL": os.Getenv("LC_ALL"), "XDG_SESSION_TYPE": os.Getenv("XDG_SESSION_TYPE"), "XDG_CURRENT_DESKTOP": os.Getenv("XDG_CURRENT_DESKTOP"), "XDG_SESSION_DESKTOP": os.Getenv("XDG_SESSION_DESKTOP"), "GDK_BACKEND": os.Getenv("GDK_BACKEND"), "QT_QPA_PLATFORM": os.Getenv("QT_QPA_PLATFORM")} {
		if value != "" {
			args = append(args, "--setenv", key, value)
		}
	}
	if display := os.Getenv("DISPLAY"); display != "" {
		args = append(args, "--setenv", "DISPLAY", display)
		if _, err = os.Stat("/tmp/.X11-unix"); err == nil {
			args = append(args, "--dir", "/tmp/.X11-unix", "--bind", "/tmp/.X11-unix", "/tmp/.X11-unix")
		}
		xauth := os.Getenv("XAUTHORITY")
		if xauth == "" {
			xauth = filepath.Join(home, ".Xauthority")
		}
		if st, e := os.Stat(xauth); e == nil && st.Mode().IsRegular() {
			target := filepath.Join(sandboxHome, ".Xauthority")
			placeholder := filepath.Join(profile, ".Xauthority")
			if f, e := os.OpenFile(placeholder, os.O_CREATE|os.O_RDWR, 0600); e == nil {
				f.Close()
				args = append(args, "--bind", xauth, target)
				args = append(args, "--setenv", "XAUTHORITY", target)
			}
		}
	}
	if wayland != "" {
		target := filepath.Join(sandboxHome, ".local", "run", "wayland-0")
		if _, e := os.Stat(filepath.Join(runtimeDir, "wayland-0")); e == nil {
			args = append(args, "--setenv", "WAYLAND_DISPLAY", target)
		}
	}
	args = append(args, "--chdir", sandboxHome, "/tmp/appholder/AppImage")
	cmd := exec.CommandContext(ctx, bwrap, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd, nil
}

func Describe() string {
	return strings.TrimSpace("Bubblewrap gives this app a private persistent home and disables networking by default. Display access is shared for GUI support; this is an extra restriction layer, not a guarantee against a determined malicious app.")
}
