package ipc

import (
	"encoding/json"
	"fmt"
	"net/http"
)

const ProtocolVersion = 5

type Info struct {
	Protocol int    `json:"protocol"`
	Version  string `json:"version"`
	PID      int    `json:"pid"`
}

func RequireCurrent(socket string) error {
	response, err := Client(socket).Get("http://appholder/info")
	if err != nil {
		return fmt.Errorf("daemon unavailable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return fmt.Errorf("an older AppHolder daemon is still running; restart it and reopen the library (systemd: systemctl --user restart appholder.service; manual daemon: stop its terminal process, then run appholder daemon)")
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("cannot inspect daemon: HTTP %d", response.StatusCode)
	}
	var info Info
	if err = json.NewDecoder(response.Body).Decode(&info); err != nil {
		return err
	}
	if info.Protocol != ProtocolVersion {
		return fmt.Errorf("incompatible AppHolder daemon; restart it using the installed binary")
	}
	return nil
}
