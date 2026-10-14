//go:build desktop && linux && cgo

package gui

/*
#cgo LDFLAGS: -ldl
#cgo pkg-config: gtk+-3.0
#include <stdlib.h>
#include "gui.h"
*/
import "C"

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"appholder/internal/ipc"
	"appholder/internal/manager"
	"appholder/internal/selfupdate"
	"appholder/internal/startup"
)

var client *http.Client
var apps []manager.App
var appholderRelease *selfupdate.Release
var releaseBase string
var tasks = make(chan func(), 32)
var uiEvents = make(chan func(), 64)
var polling atomic.Bool

func postUI(task func()) { uiEvents <- task; C.holder_wake() }
func enqueue(task func()) bool {
	select {
	case tasks <- task:
		return true
	default:
		return false
	}
}

//export goHolderDrain
func goHolderDrain() {
	for {
		select {
		case task := <-uiEvents:
			task()
		default:
			return
		}
	}
}

func Available() bool                { return true }
func Run(socket string) error        { return Open(socket, "") }
func Tray(socket string) error       { return display(socket, "", true) }
func Open(socket, path string) error { return display(socket, path, false) }
func display(socket, path string, tray bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	instance, err := claimDesktop(ctx, socket, desktopRequest{Path: path, Tray: tray}, func(request desktopRequest) {
		postUI(func() {
			if request.Tray {
				return
			}
			C.holder_present()
			if request.Path != "" {
				value := C.CString(request.Path)
				defer C.free(unsafe.Pointer(value))
				C.holder_open(value)
			}
		})
	})
	cancel()
	if err != nil {
		return err
	}
	if instance == nil {
		return nil
	}
	defer instance.Close()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if tray {
		C.holder_tray_mode()
	}
	if C.holder_init() == 0 {
		return fmt.Errorf("cannot connect to graphical display")
	}
	releaseBase = selfupdate.ReleaseBaseURL
	if value := os.Getenv("APPHOLDER_RELEASE_BASE_URL"); value != "" {
		releaseBase = value
	}
	version := C.CString("AppHolder " + selfupdate.Version)
	C.holder_version(version)
	C.free(unsafe.Pointer(version))
	client = ipc.Client(socket)
	client.Timeout = 5 * time.Second
	go func() {
		for task := range tasks {
			task()
		}
	}()
	refresh()
	enqueue(func() {
		enabled := false
		if controller, err := startup.New(); err == nil {
			enabled = controller.Enabled()
		}
		postUI(func() {
			if enabled {
				C.holder_startup_state(1)
			}
		})
		response, err := client.Get("http://appholder/settings")
		if err != nil {
			return
		}
		defer response.Body.Close()
		var settings struct {
			Directories           []string `json:"directories"`
			BackupDirectory       string   `json:"backup_directory"`
			KeepVersions          int      `json:"keep_versions"`
			GUITheme              string   `json:"gui_theme"`
			DefaultAutoUpdate     bool     `json:"default_auto_update"`
			DefaultSandboxed      bool     `json:"default_sandboxed"`
			DefaultSandboxNetwork bool     `json:"default_sandbox_network"`
		}
		if response.StatusCode == 200 && json.NewDecoder(response.Body).Decode(&settings) == nil {
			folders := strings.Join(settings.Directories, "\n")
			backup := settings.BackupDirectory
			keep := settings.KeepVersions
			theme := settings.GUITheme
			autoDefault, sandboxDefault, networkDefault := settings.DefaultAutoUpdate, settings.DefaultSandboxed, settings.DefaultSandboxNetwork
			if theme == "" {
				theme = "system"
			}
			postUI(func() {
				renderFolders(folders, backup)
				C.holder_settings_keep(C.int(keep))
				themeC := C.CString(theme)
				defer C.free(unsafe.Pointer(themeC))
				C.holder_theme(themeC)
				C.holder_app_defaults(boolean(autoDefault), boolean(sandboxDefault), boolean(networkDefault))
			})
		}
	})
	if path != "" {
		value := C.CString(path)
		C.holder_open(value)
		C.free(unsafe.Pointer(value))
	}
	C.holder_run()
	return nil
}
func status(text string) { s := C.CString(text); defer C.free(unsafe.Pointer(s)); C.holder_status(s) }
func refresh() {
	if !polling.CompareAndSwap(false, true) {
		return
	}
	if !enqueue(func() {
		defer polling.Store(false)
		operations := map[string]ipc.Operation{}
		response, err := client.Get("http://appholder/operations")
		if err == nil {
			if response.StatusCode == 200 {
				json.NewDecoder(response.Body).Decode(&operations)
			}
			response.Body.Close()
		}
		postUI(func() { render(apps, operations) })
		response, err = client.Get("http://appholder/apps")
		if err != nil {
			postUI(func() { status("Cannot reach AppHolder: " + err.Error()) })
			return
		}
		defer response.Body.Close()
		var snapshot []manager.App
		if response.StatusCode != 200 {
			postUI(func() { status("Could not load library") })
			return
		}
		if err = json.NewDecoder(response.Body).Decode(&snapshot); err != nil {
			postUI(func() { status(err.Error()) })
			return
		}

		postUI(func() { render(snapshot, operations) })
	}) {
		polling.Store(false)
	}
}
func render(snapshot []manager.App, operations map[string]ipc.Operation) {
	apps = snapshot
	C.holder_begin_refresh()
	available, offered, active := 0, 0, 0
	for _, a := range apps {
		version := a.Version
		if version == "" {
			version = "Unknown"
		}
		text := "Not checked"
		if a.UpdateInfo == "" {
			text = "No update source"
		}
		switch a.Update.Status {
		case "available":
			text = "Update available"
			offered++
			if !a.Pinned {
				available++
			}
		case "up-to-date":
			text = "Up to date"
		case "no-source":
			text = "No update source"
		case "unsupported":
			text = "Unsupported source"
		case "error":
			text = "Check failed: " + a.Update.Error
		}
		percent := -1
		running := false
		if operation, ok := operations[a.ID]; ok {
			switch operation.Status {
			case "running":
				running = true
				active++
				switch operation.Stage {
				case "queued":
					text = "Queued"
				case "downloading":
					text = "Downloading"
				case "verifying":
					text = "Verifying download"
				case "installing":
					text = "Saving backup and installing"
				case "restoring":
					text = "Restoring saved version"
				default:
					text = "Checking update"
				}
				if operation.Total > 0 {
					percent = int(operation.Downloaded * 100 / operation.Total)
					if percent > 100 {
						percent = 100
					}
					text += fmt.Sprintf(" · %.1f / %.1f MB", float64(operation.Downloaded)/1e6, float64(operation.Total)/1e6)
				}
			case "cancelled":
				text = "Automatic update cancelled"
			case "failed":
				text = "Failed: " + operation.Error
			}
		}
		progressText := text
		if running {
			text = strings.SplitN(text, " · ", 2)[0]
		}
		values := []string{a.ID, a.DisplayName(), version, a.Path, a.IconPath, text, a.Fingerprint, progressText}
		var stringsC []*C.char
		for _, value := range values {
			stringsC = append(stringsC, C.CString(value))
		}
		C.holder_add(stringsC[0], stringsC[1], stringsC[2], stringsC[3], stringsC[4], stringsC[5], stringsC[6], stringsC[7], C.int(percent), boolean(running), boolean(a.Update.Status == "available"), boolean(len(a.Versions) > 0), C.int(len(a.Versions)), boolean(a.Pinned), boolean(a.AutoUpdate), boolean(a.AutoUpdate || (!a.Pinned && a.UpdateInfo != "" && a.Update.Status != "unsupported")), boolean(a.Sandboxed), boolean(a.SandboxNetwork))
		for _, value := range stringsC {
			C.free(unsafe.Pointer(value))
		}
	}
	C.holder_finish(C.int(len(apps)), C.int(available), C.int(offered), C.int(active))
}
func boolean(value bool) C.int {
	if value {
		return 1
	}
	return 0
}
func requestAction(endpoint string, body []byte, done func([]byte)) {
	requestWithFeedback(endpoint, body, done, status)
}
func requestWithFeedback(endpoint string, body []byte, done func([]byte), feedback func(string)) {
	if !enqueue(func() {
		response, err := client.Post("http://appholder"+endpoint, "application/json", bytes.NewReader(body))
		if err != nil {
			postUI(func() { feedback(err.Error()) })
			return
		}
		defer response.Body.Close()
		data, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
		if err != nil {
			postUI(func() { feedback(err.Error()) })
			return
		}
		if response.StatusCode != 200 && response.StatusCode != 202 && response.StatusCode != 204 {
			postUI(func() { feedback(strings.TrimSpace(string(data))) })
			return
		}
		postUI(func() { done(data); refresh() })
	}) {
		feedback("AppHolder is busy. Please try again shortly.")
	}
}
func launch(path string) error {
	// Reuse the CLI's validation and launch behavior, keeping app lifetime independent of the GUI.
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(executable, "run", path)
	if err = cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

//export goHolderAction
func goHolderAction(action C.int, value *C.char) {
	path := C.GoString(value)
	switch int(action) {
	case 20, 21:
		enabled := int(action) == 20
		body, _ := json.Marshal(map[string]bool{"enabled": enabled})
		requestAction("/apps/"+url.PathEscape(path)+"/auto-update", body, func([]byte) {
			if enabled {
				status("Automatic updates enabled for this copy. Verified updates will be installed with backups.")
			} else {
				status("Automatic updates disabled for this copy.")
			}
		})
	case 15, 19:
		endpoint := "remove"
		if int(action) == 19 {
			endpoint = "uninstall"
		}
		requestAction("/apps/"+url.PathEscape(path)+"/"+endpoint, nil, func([]byte) { status("Application removed. Saved backups were kept.") })
	case 16:
		settingsParts := strings.SplitN(path, "\n--KEEP--\n", 2)
		keep := 3
		if len(settingsParts) == 2 {
			keep, _ = strconv.Atoi(settingsParts[1])
		}
		var directories []string
		for _, line := range strings.Split(settingsParts[0], "\n") {
			if value := strings.TrimSpace(line); value != "" {
				directories = append(directories, value)
			}
		}
		body, _ := json.Marshal(map[string]any{"directories": directories, "keep_versions": keep})
		requestWithFeedback("/settings", body, func(data []byte) {
			var config manager.Config
			if err := json.Unmarshal(data, &config); err != nil {
				settingsStatus(err.Error())
				return
			}
			folders := C.CString(strings.Join(config.Directories, "\n"))
			C.holder_settings_folders(folders)
			C.holder_settings_keep(C.int(config.KeepVersions))
			C.free(unsafe.Pointer(folders))
			settingsStatus("Watched folders saved. Files in removed folders are kept.")
		}, settingsStatus)
	case 17:
		checkAppHolderUpdate()
	case 18:
		installAppHolderUpdate()
	case 11:
		for _, a := range apps {
			if a.ID == path {
				name := C.CString(a.Name)
				text := C.CString(applicationDetails(a, apps))
				C.holder_details(name, text)
				C.free(unsafe.Pointer(name))
				C.free(unsafe.Pointer(text))
				return
			}
		}
	case 12:
		requestAction("/updates/install", nil, func(data []byte) {
			var result ipc.BatchResult
			if err := json.Unmarshal(data, &result); err != nil {
				status(err.Error())
				return
			}
			status(fmt.Sprintf("Queued %d updates; skipped %d applications.", len(result.Started), len(result.Skipped)))
		})
	case 13, 14:
		body, _ := json.Marshal(map[string]bool{"pinned": int(action) == 13})
		requestAction("/apps/"+url.PathEscape(path)+"/pin", body, func([]byte) { status("Version preference saved.") })
	case 0:
		refresh()
	case 9:
		if !enqueue(func() {
			controller, err := startup.New()
			enabled := false
			if err == nil {
				if path == "enable" {
					err = controller.Enable()
				} else {
					err = controller.Disable()
				}
				enabled = controller.Enabled()
			}
			postUI(func() {
				C.holder_startup_state(boolean(enabled))
				text := "Startup setting saved."
				if err != nil {
					text = err.Error()
				}
				value := C.CString(text)
				C.holder_settings_status(value)
				C.free(unsafe.Pointer(value))
			})
		}) {
			status("AppHolder is busy. Please try again shortly.")
		}
	case 8:
		for _, a := range apps {
			if a.ID == path {
				var lines []string
				for i := len(a.Versions) - 1; i >= 0; i-- {
					v := a.Versions[i]
					version := v.Version
					if version == "" {
						version = "Unknown version"
					}
					lines = append(lines, version+" · "+v.SavedAt.Local().Format("2006-01-02 15:04"))
				}
				text := strings.Join(lines, "\n")
				if text == "" {
					text = "No versions have been saved yet."
				}
				value := C.CString(text)
				id := C.CString(a.ID)
				C.holder_versions(id, value)
				C.free(unsafe.Pointer(id))
				C.free(unsafe.Pointer(value))
				return
			}
		}
	case 22:
		parts := strings.SplitN(path, ":", 2)
		if len(parts) != 2 {
			status("Invalid saved version selection.")
			return
		}
		index, err := strconv.Atoi(parts[1])
		if err != nil || index < 0 {
			status("Invalid saved version selection.")
			return
		}
		requestAction("/apps/"+url.PathEscape(parts[0])+"/versions/"+strconv.Itoa(index)+"/restore", nil, func([]byte) { status("Restore started. Progress appears in your library.") })
	case 23:
		parts := strings.Split(path, "|")
		if len(parts) != 3 {
			status("Invalid sandbox selection.")
			return
		}
		body, _ := json.Marshal(map[string]bool{"enabled": parts[1] == "1", "allow_network": parts[2] == "1"})
		requestAction("/apps/"+url.PathEscape(parts[0])+"/sandbox", body, func([]byte) {
			if parts[1] == "1" {
				status("Sandbox enabled for the next launch. Bubblewrap is required.")
			} else {
				status("Sandbox disabled for the next launch.")
			}
		})
	case 24:
		body, _ := json.Marshal(map[string]string{"theme": path})
		requestAction("/settings/theme", body, func([]byte) { status("Appearance preference saved.") })
	case 25:
		parts := strings.Split(path, "|")
		if len(parts) != 3 {
			status("Invalid application defaults.")
			return
		}
		body, _ := json.Marshal(map[string]bool{"default_auto_update": parts[0] == "1", "default_sandboxed": parts[1] == "1", "default_sandbox_network": parts[2] == "1"})
		requestAction("/settings/defaults", body, func([]byte) { settingsStatus("Defaults saved for newly added applications.") })
	case 6, 7:
		operation := "install"
		if int(action) == 7 {
			operation = "rollback"
		}
		requestAction("/apps/"+url.PathEscape(path)+"/"+operation, nil, func([]byte) { status("Task started. Progress appears in your library.") })
	case 5:
		requestAction("/updates/check", nil, func([]byte) { status("Checking for updates…") })
	case 1:
		for _, a := range apps {
			if a.ID == path {
				if err := launch(a.Path); err != nil {
					status(err.Error())
				} else {
					status("Launch requested for " + a.Name)
				}
				return
			}
		}
	case 2:
		if err := launch(path); err != nil {
			status(err.Error())
		} else {
			status("Launch requested")
		}
	case 3, 4:
		body, _ := json.Marshal(map[string]string{"path": path})
		requestAction("/integrate", body, func(data []byte) {
			var a manager.App
			if err := json.Unmarshal(data, &a); err != nil {
				status(err.Error())
				return
			}
			status("Added " + a.Name)
			if int(action) == 4 {
				if err := launch(a.Path); err != nil {
					status(err.Error())
				}
			}
		})
	}
}

func renderFolders(folders, backup string, keep ...int) {
	f := C.CString(folders)
	b := C.CString(backup)
	count := 3
	if len(keep) > 0 && keep[0] > 0 {
		count = keep[0]
	}
	C.holder_settings(f, b, C.int(count))
	C.free(unsafe.Pointer(f))
	C.free(unsafe.Pointer(b))
}
func settingsStatus(text string) {
	value := C.CString(text)
	C.holder_settings_status(value)
	C.free(unsafe.Pointer(value))
}
func checkAppHolderUpdate() {
	if releaseBase == "" {
		settingsStatus("Self-update is not available for this testing build: no release server is configured.")
		return
	}
	settingsStatus("Checking AppHolder updates…")
	C.holder_update_available(0)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		release, err := selfupdate.New().Latest(ctx, releaseBase)
		postUI(func() {
			if err != nil {
				settingsStatus(err.Error())
				return
			}
			appholderRelease = &release
			available := strings.TrimPrefix(release.Version, "v") != strings.TrimPrefix(selfupdate.Version, "v")
			C.holder_update_available(boolean(available))
			if available {
				settingsStatus("AppHolder " + release.Version + " is available.")
			} else {
				settingsStatus("AppHolder is up to date.")
			}
		})
	}()
}
func installAppHolderUpdate() {
	if appholderRelease == nil {
		return
	}
	release := *appholderRelease
	C.holder_update_available(0)
	settingsStatus("Updating AppHolder…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		executable, err := os.Executable()
		if err == nil {
			err = selfupdate.New().Replace(ctx, executable, release)
		}
		if errors.Is(err, fs.ErrPermission) && trustedSystemExecutable(executable) {
			if helper, lookupErr := exec.LookPath("pkexec"); lookupErr == nil {
				command := exec.CommandContext(ctx, helper, executable, "self-update", "--base-url", releaseBase)
				output, runErr := command.CombinedOutput()
				if runErr != nil {
					err = fmt.Errorf("administrator authorization or update failed: %s (%w)", strings.TrimSpace(string(output)), runErr)
				} else {
					err = nil
				}
			}
		}
		postUI(func() {
			if err != nil {
				settingsStatus(err.Error())
				C.holder_update_available(1)
				return
			}
			settingsStatus("AppHolder updated. Restart the daemon and reopen AppHolder to use it.")
		})
	}()
}
func trustedSystemExecutable(path string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != "/usr/local/bin/appholder" {
		return false
	}
	for _, p := range []string{resolved, filepath.Dir(resolved)} {
		info, err := os.Stat(p)
		if err != nil || info.Mode().Perm()&022 != 0 {
			return false
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != 0 {
			return false
		}
	}
	return true
}
