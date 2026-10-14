package ipc

import (
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"appholder/internal/manager"
)

func TestDetectsOlderDaemon(t *testing.T) {
	for _, current := range []bool{false, true} {
		socket := filepath.Join(t.TempDir(), "daemon.sock")
		listener, err := net.Listen("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		handler := http.Handler(http.NewServeMux())
		if current {
			handler = (&Service{Manager: &manager.Manager{}, BuildVersion: "test"}).Handler()
		}
		server := &http.Server{Handler: handler}
		go server.Serve(listener)
		err = RequireCurrent(socket)
		server.Close()
		if current && err != nil {
			t.Fatal(err)
		}
		if !current && (err == nil || !strings.Contains(err.Error(), "older AppHolder daemon")) {
			t.Fatalf("missing restart instruction: %v", err)
		}
	}
}
