package main

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"keepassxc-agent-middleware/internal/settings"
)

func TestConfigDefaultsSessionUpstreamAndCLIOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	config := settings.Config{Listen: "saved-listener", FallbackUpstream: "saved-fallback", Browser: "saved-browser", Timeout: "12s", LogFile: "saved.log"}
	data, _ := json.Marshal(config)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, session := range []string{"", "current-session-agent"} {
		for _, override := range []bool{false, true} {
			flags := flag.NewFlagSet("test", flag.ContinueOnError)
			listen := flags.String("listen", "default-listener", "")
			upstream := flags.String("upstream", session, "")
			browserPath := flags.String("browser", "default-browser", "")
			expected := flags.String("fingerprint", "", "")
			timeout := flags.Duration("timeout", time.Minute, "")
			logFile := flags.String("log-file", "", "")
			args := []string{}
			wantListen := config.Listen
			if override {
				args = []string{"--listen", "explicit-listener"}
				wantListen = "explicit-listener"
			}
			if err := flags.Parse(args); err != nil {
				t.Fatal(err)
			}
			if err := applyConfig(flags, path, listen, upstream, browserPath, expected, timeout, logFile); err != nil {
				t.Fatal(err)
			}
			if *listen != wantListen || *browserPath != "saved-browser" || *timeout != 12*time.Second || *logFile != "saved.log" {
				t.Fatal("saved configuration or CLI precedence failed")
			}
			want := session
			if want == "" {
				want = "saved-fallback"
			}
			if *upstream != want {
				t.Fatalf("upstream: %s", *upstream)
			}
		}
	}
}
