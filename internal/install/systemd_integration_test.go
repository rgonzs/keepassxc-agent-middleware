//go:build linux

package install

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in only: ordinary tests never register jobs in a developer's session.
// Run in an isolated Linux VM with KAM_TEST_SYSTEMD=1.
func TestIsolatedSystemdLifecycle(t *testing.T) {
	if os.Getenv("KAM_TEST_SYSTEMD") != "1" {
		t.Skip("isolated systemd session not requested")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if _, err := command(ctx, "systemctl", "--user", "show-environment"); err != nil {
		t.Fatalf("isolated user systemd unavailable: %v", err)
	}
	p := isolatedUnixPlan(t)
	old := p.ServicePath
	p.job = fmt.Sprintf("kam-lifecycle-test-%d-%s.service", os.Getpid(), filepath.Base(p.Root))
	p.ServicePath = filepath.Join(filepath.Dir(old), p.job)
	delete(p.Files, old)
	delete(p.Manifest.Files, old)
	p.Files[p.ServicePath] = p.serviceFile()
	p.Manifest.Files[p.ServicePath] = digest(p.Files[p.ServicePath])
	mustWrite(t, p.ServicePath, p.Files[p.ServicePath])
	manifest, _ := json.Marshal(p.Manifest)
	mustWrite(t, p.ManifestPath, manifest)
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		command(cleanup, "systemctl", "--user", "stop", p.serviceID())
		command(cleanup, "systemctl", "--user", "disable", p.serviceID())
		command(cleanup, "systemctl", "--user", "daemon-reload")
	})
	if _, err := command(ctx, "systemctl", "--user", "link", p.ServicePath); err != nil {
		t.Fatal(err)
	}
	if err := (SystemManager{}).Start(ctx, p); err != nil {
		t.Fatal(err)
	}
	pid := func() string {
		value, err := command(ctx, "systemctl", "--user", "show", p.serviceID(), "--property=MainPID", "--value")
		if err != nil || value == "0" || value == "" {
			t.Fatalf("missing main PID: %q %v", value, err)
		}
		return value
	}
	first := pid()
	if err := (SystemManager{}).Control(ctx, p, "start"); err != nil {
		t.Fatal(err)
	}
	if pid() != first {
		t.Fatal("start created a duplicate")
	}
	if err := (SystemManager{}).Control(ctx, p, "stop"); err != nil {
		t.Fatal(err)
	}
	if enabled, err := command(ctx, "systemctl", "--user", "is-enabled", p.serviceID()); err != nil || !strings.Contains(enabled, "enabled") {
		t.Fatalf("stop removed login startup: %s %v", enabled, err)
	}
	if err := (SystemManager{}).Control(ctx, p, "stop"); err != nil {
		t.Fatal(err)
	}
	if err := (SystemManager{}).Control(ctx, p, "start"); err != nil {
		t.Fatal(err)
	}
	second := pid()
	if err := (SystemManager{}).Control(ctx, p, "restart"); err != nil {
		t.Fatal(err)
	}
	if pid() == second {
		t.Fatal("restart kept the previous process")
	}
	if err := (SystemManager{}).Stop(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := waitEndpoint(ctx, p.Config.Listen, false); err != nil {
		t.Fatal(err)
	}
}
