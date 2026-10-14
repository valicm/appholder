package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"appholder/internal/appuri"
	"appholder/internal/desktop"
	"appholder/internal/gui"
	"appholder/internal/ipc"
	"appholder/internal/manager"
	"appholder/internal/sandbox"
	"appholder/internal/selfupdate"
	"appholder/internal/startup"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "appholder:", err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 2 {
		fmt.Println("Usage: appholder <version|self-update|startup enable|disable|status|daemon|gui|tray|check-updates|update-all|update ID|rollback ID|remove ID|uninstall ID --yes|operations|open FILE|install-desktop|list|integrate FILE|run FILE|integrate-run FILE>")
		return nil
	}
	command := os.Args[1]
	if command == "open-uri" {
		if len(os.Args) != 3 {
			return fmt.Errorf("open-uri requires one URI")
		}
		path, err := appuri.LocalPath(os.Args[2])
		if err != nil {
			remoteURL, remoteErr := appuri.RemoteURL(os.Args[2])
			if remoteErr != nil {
				return remoteErr
			}
			m, loadErr := manager.Load()
			if loadErr != nil {
				return loadErr
			}
			downloadCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			path, err = appuri.DownloadRemote(downloadCtx, nil, m.StateDir, remoteURL)
			if err != nil {
				return err
			}
		}
		os.Args[2] = path
		command = "open"
	}
	if command == "version" || command == "--version" {
		fmt.Println("AppHolder", selfupdate.Version)
		return nil
	}
	if command == "startup" {
		return configureStartup(os.Args[2:])
	}
	if command == "self-update" {
		return selfUpdate(os.Args[2:])
	}
	if command != "remove" && command != "uninstall" && command != "update-all" && command != "update" && command != "rollback" && command != "operations" && command != "check-updates" && command != "tray" && command != "open" && command != "install-desktop" && command != "gui" && command != "daemon" && command != "list" && command != "integrate" && command != "run" && command != "integrate-run" {
		return fmt.Errorf("unknown command %q", command)
	}
	if command == "uninstall" && (len(os.Args) != 4 || os.Args[3] != "--yes") {
		return fmt.Errorf("usage: appholder uninstall ID --yes (permanently deletes the selected AppImage)")
	}
	if command == "remove" || command == "update" || command == "rollback" || command == "open" || command == "run" || command == "integrate" || command == "integrate-run" {
		if len(os.Args) != 3 {
			return fmt.Errorf("%s requires one file", command)
		}
	}
	m, err := manager.Load()
	if err != nil {
		return err
	}
	if command == "run" {
		absolute, absErr := filepath.Abs(os.Args[2])
		if absErr != nil {
			return absErr
		}
		for i := range m.Apps {
			if filepath.Clean(m.Apps[i].Path) == filepath.Clean(absolute) {
				return launch(absolute, m, &m.Apps[i])
			}
		}
		return launch(absolute, m, nil)
	}
	if command == "install-desktop" {
		if !gui.Available() {
			return fmt.Errorf("build with -tags desktop before installing the desktop handler")
		}
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		if err = desktop.Install(m.DesktopDir, executable); err != nil {
			return err
		}
		if tool, err := exec.LookPath("xdg-mime"); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if output, err := exec.CommandContext(ctx, tool, "default", "appholder-uri.desktop", "x-scheme-handler/appimg").CombinedOutput(); err != nil {
				return fmt.Errorf("desktop entries installed, but appimg registration failed: %s: %w", output, err)
			}
		}
		fmt.Println("Installed AppHolder desktop handler. Select it using your file manager's Open With menu.")
		return nil
	}
	if command == "tray" || command == "gui" || command == "open" {
		openPath := ""
		if command == "open" {
			openPath, err = filepath.Abs(os.Args[2])
			if err != nil {
				return err
			}
			if err = manager.Validate(openPath); err != nil {
				return err
			}
		}
		if !gui.Available() {
			return gui.Run("")
		}
		if _, err := os.Lstat(ipc.SocketPath(m)); os.IsNotExist(err) {
			controller, startupErr := startup.New()
			if startupErr == nil && controller.Enabled() {
				if err = controller.Start(); err != nil {
					return err
				}
			} else {
				executable, err := os.Executable()
				if err != nil {
					return err
				}
				child := exec.Command(executable, "daemon")
				child.Stdout = os.Stdout
				child.Stderr = os.Stderr
				if err = child.Start(); err != nil {
					return err
				}
				go child.Wait()
			}
			ready := false
			for i := 0; i < 100; i++ {
				if _, err := os.Stat(ipc.SocketPath(m)); err == nil {
					ready = true
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			if !ready {
				return fmt.Errorf("daemon did not start")
			}
		}
		if err = ipc.RequireCurrent(ipc.SocketPath(m)); err != nil {
			return err
		}
		if command == "tray" {
			return gui.Tray(ipc.SocketPath(m))
		}
		return gui.Open(ipc.SocketPath(m), openPath)
	}
	if _, socketErr := os.Lstat(ipc.SocketPath(m)); socketErr == nil {
		if command != "daemon" {
			return remote(m, command)
		}
	} else if !os.IsNotExist(socketErr) {
		return socketErr
	}
	if command == "remove" || command == "uninstall" || command == "update-all" || command == "update" || command == "rollback" || command == "operations" || command == "check-updates" {
		return fmt.Errorf("start AppHolder daemon or GUI before checking updates")
	}
	if command == "list" {
		return json.NewEncoder(os.Stdout).Encode(m.Apps)
	}
	if err = os.MkdirAll(m.StateDir, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(m.StateDir, "manager.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("another AppHolder process is managing the library; retry shortly")
	}
	// Reload after acquiring the lock so a concurrent writer cannot leave stale state.
	m, err = manager.Load()
	if err != nil {
		return err
	}
	if command == "integrate" || command == "integrate-run" {
		a, err := m.Integrate(os.Args[2])
		if err != nil {
			return err
		}
		fmt.Println(a.Path)
		if command == "integrate-run" {
			return launch(a.Path, m, &a)
		}
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	service := &ipc.Service{Manager: m, Context: ctx, BuildVersion: selfupdate.Version}
	listener, err := service.Listen()
	if err != nil {
		return err
	}
	server := &http.Server{Handler: service.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.Serve(listener) }()
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		stop()
		server.Shutdown(shutdown)
		listener.Close()
		service.Wait()
	}()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	previous := map[string]string{}
	fmt.Println("AppHolder watching", m.Config.Directories)
	for {
		previous, err = service.Scan(previous)
		service.CheckUpdates(ctx, false)
		if err != nil {
			fmt.Fprintln(os.Stderr, "scan:", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case err := <-serverErrors:
			if err != http.ErrServerClosed {
				return err
			}
			return nil
		case <-ticker.C:
		}
	}
}
func launch(path string, m *manager.Manager, app *manager.App) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err = manager.Validate(absolute); err != nil {
		return err
	}
	st, err := os.Stat(absolute)
	if err != nil {
		return err
	}
	if err = os.Chmod(absolute, st.Mode().Perm()|0100); err != nil {
		return err
	}
	var cmd *exec.Cmd
	if app != nil && app.Sandboxed {
		cmd, err = sandbox.Command(context.Background(), absolute, m.StateDir, app.ID, app.SandboxNetwork)
		if err != nil {
			return err
		}
	} else {
		cmd = exec.Command(absolute)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func remote(m *manager.Manager, command string) error {
	if err := ipc.RequireCurrent(ipc.SocketPath(m)); err != nil {
		return err
	}
	client := ipc.Client(ipc.SocketPath(m))
	var response *http.Response
	var err error
	if command == "update-all" {
		response, err = client.Post("http://appholder/updates/install", "application/json", nil)
	} else if command == "operations" {
		response, err = client.Get("http://appholder/operations")
	} else if command == "remove" || command == "uninstall" || command == "update" || command == "rollback" {
		action := "install"
		if command != "update" {
			action = command
		}
		response, err = client.Post("http://appholder/apps/"+url.PathEscape(os.Args[2])+"/"+action, "application/json", nil)
	} else if command == "check-updates" {
		response, err = client.Post("http://appholder/updates/check", "application/json", nil)
	} else if command == "list" {
		response, err = client.Get("http://appholder/apps")
	} else {
		absolute, e := filepath.Abs(os.Args[2])
		if e != nil {
			return e
		}
		body, e := json.Marshal(map[string]string{"path": absolute})
		if e != nil {
			return e
		}
		response, err = client.Post("http://appholder/integrate", "application/json", bytes.NewReader(body))
	}
	if err != nil {
		return fmt.Errorf("daemon unavailable (restart it if its socket is stale): %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusAccepted && response.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 16384))
		return fmt.Errorf("daemon: %s", body)
	}
	if command == "remove" {
		fmt.Println("Removed from AppHolder. The AppImage file and saved backups are kept.")
		return nil
	}
	if command == "uninstall" {
		fmt.Println("AppImage uninstalled. Saved backups are kept.")
		return nil
	}
	if command == "remove" || command == "uninstall" || command == "update-all" || command == "operations" || command == "update" || command == "rollback" || command == "check-updates" || command == "list" {
		_, err = io.Copy(os.Stdout, response.Body)
		return err
	}
	var a manager.App
	if err = json.NewDecoder(response.Body).Decode(&a); err != nil {
		return err
	}
	fmt.Println(a.Path)
	if command == "integrate-run" {
		return launch(a.Path, m, &a)
	}
	return nil
}

func selfUpdate(args []string) error {
	flags := flag.NewFlagSet("self-update", flag.ContinueOnError)
	check := flags.Bool("check", false, "check without downloading or replacing")
	dryRun := flags.Bool("dry-run", false, "show the update without downloading")
	defaultBase := selfupdate.ReleaseBaseURL
	if value := os.Getenv("APPHOLDER_RELEASE_BASE_URL"); value != "" {
		defaultBase = value
	}
	base := flags.String("base-url", defaultBase, "HTTPS release base URL")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected self-update arguments")
	}
	updater := selfupdate.New()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	release, err := updater.Latest(ctx, *base)
	if err != nil {
		return err
	}
	fmt.Printf("Installed: %s; published: %s\n", selfupdate.Version, release.Version)
	if strings.TrimPrefix(release.Version, "v") == strings.TrimPrefix(selfupdate.Version, "v") {
		fmt.Println("Already current")
		return nil
	}
	if *check || *dryRun {
		return nil
	}
	target, err := os.Executable()
	if err != nil {
		return err
	}
	if err = updater.Replace(ctx, target, release); err != nil {
		return err
	}
	fmt.Println("Updated AppHolder. Restart your daemon and tray to use the new version.")
	return nil
}

func configureStartup(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: appholder startup <enable|disable|status>")
	}
	controller, err := startup.New()
	if err != nil {
		return err
	}
	switch args[0] {
	case "enable":
		if err = controller.Enable(); err != nil {
			return err
		}
		m, err := manager.Load()
		if err != nil {
			return err
		}
		if _, err = os.Stat(ipc.SocketPath(m)); os.IsNotExist(err) {
			if err = controller.Start(); err != nil {
				return err
			}
		}
		fmt.Println("Startup enabled: user daemon and desktop tray. An existing daemon remains running until next login.")
	case "disable":
		if err = controller.Disable(); err != nil {
			return err
		}
		fmt.Println("Startup disabled. Current daemon and tray remain running.")
	case "status":
		if controller.Enabled() {
			fmt.Println("Startup enabled")
		} else {
			fmt.Println("Startup disabled or unavailable")
		}
	default:
		return fmt.Errorf("unknown startup action %q", args[0])
	}
	return nil
}
