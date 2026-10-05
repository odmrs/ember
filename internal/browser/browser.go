// Package browser opens a URL in the user's default browser.
package browser

import (
	"os/exec"
	"runtime"
)

// Open asks the operating system to show url in the default browser. It does
// not wait for the browser to start.
func Open(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
