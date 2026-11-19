// Package credentials reads the account key without placing it in argv or logs.
package credentials

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func Load() string {
	if key := strings.TrimSpace(os.Getenv("ANNAS_ARCHIVE_KEY")); key != "" {
		return key
	}
	if runtime.GOOS != "darwin" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password", "-a", "bookget", "-s", "bookget.annas-archive", "-w").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func Base() string {
	if base := os.Getenv("BOOKGET_ANNA_URL"); base != "" {
		return base
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, "bookget", "config.json"))
	if err != nil {
		return ""
	}
	var settings struct {
		URL string `json:"anna_url"`
	}
	if json.Unmarshal(data, &settings) != nil {
		return ""
	}
	return settings.URL
}
