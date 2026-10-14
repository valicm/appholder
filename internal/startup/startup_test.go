package startup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnableDisablePreservesCurrentSession(t *testing.T) {
	var calls []string
	c := &Controller{ConfigDir: t.TempDir(), Executable: "/usr/local/bin/appholder", Run: func(args ...string) error { calls = append(calls, strings.Join(args, " ")); return nil }}
	if err := c.Enable(); err != nil {
		t.Fatal(err)
	}
	unit, desktop := c.paths()
	data, err := os.ReadFile(unit)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `ExecStart="/usr/local/bin/appholder" daemon`) || !strings.Contains(string(data), "WantedBy=default.target") {
		t.Fatal(string(data))
	}
	data, err = os.ReadFile(desktop)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `Exec="/usr/local/bin/appholder" tray`) {
		t.Fatal(string(data))
	}
	if !c.Enabled() {
		t.Fatal("not enabled")
	}
	if err = c.Disable(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(desktop); !os.IsNotExist(err) {
		t.Fatal("autostart entry remains")
	}
	for _, call := range calls {
		if strings.Contains(call, "--now") || strings.HasPrefix(call, "stop") {
			t.Fatal("stopped current session")
		}
	}
}
func TestEnableRefusesUnownedEntries(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "autostart", "appholder.desktop")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("unrelated entry"), 0644); err != nil {
		t.Fatal(err)
	}
	c := &Controller{ConfigDir: root, Executable: "/usr/local/bin/appholder", Run: func(...string) error { t.Fatal("systemctl called before checking ownership"); return nil }}
	if err := c.Enable(); err == nil {
		t.Fatal("overwrote unowned entry")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "unrelated entry" {
		t.Fatal("modified entry")
	}
}
