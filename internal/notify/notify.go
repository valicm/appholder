package notify

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// Added is best-effort: missing desktop sessions or notify-send never break scanning.
func Added(names []string)   { send("Applications added", names) }
func Updated(names []string) { send("Applications updated", names) }
func Updates(names []string) { send("Updates available", names) }
func send(title string, names []string) {
	if len(names) == 0 {
		return
	}
	body := strings.Join(names, ", ")
	// Notification servers may interpret markup.
	body = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(body)
	if len(body) > 1024 {
		body = "Open AppHolder for details."
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	exec.CommandContext(ctx, "notify-send", "--app-name=AppHolder", "--icon=appholder", "--", title, body).Run()
}
