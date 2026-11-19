package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"keepassxc-agent-middleware/internal/localfile"
)

type Config struct {
	Listen           string `json:"listen"`
	Upstream         string `json:"upstream,omitempty"`
	FallbackUpstream string `json:"fallback_upstream,omitempty"`
	Browser          string `json:"browser,omitempty"`
	Fingerprint      string `json:"fingerprint,omitempty"`
	Timeout          string `json:"timeout"`
	LogFile          string `json:"log_file,omitempty"`
}

func Root() string        { home, _ := os.UserHomeDir(); return filepath.Join(home, ".keepassxc-agent") }
func DefaultPath() string { return filepath.Join(Root(), "config.json") }
func Load(path string) (Config, error) {
	var c Config
	data, err := localfile.ReadConfig(path)
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("read configuration %s: %w", path, err)
	}
	return c, nil
}
