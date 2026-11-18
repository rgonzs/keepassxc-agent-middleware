//go:build darwin

package install

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestIsolatedLaunchdLifecycle(t *testing.T) {
	if os.Getenv("KAM_TEST_LAUNCHD") != "1" {
		t.Skip("isolated launchd job not requested")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	p := isolatedUnixPlan(t)
	p.job = fmt.Sprintf("org.keepassxc.agent-middleware.test-%d", os.Getpid())
	p.Files[p.ServicePath] = p.serviceFile()
	p.Manifest.Files[p.ServicePath] = digest(p.Files[p.ServicePath])
	mustWrite(t, p.ServicePath, p.Files[p.ServicePath])
	manifest, _ := json.Marshal(p.Manifest)
	mustWrite(t, p.ManifestPath, manifest)
	target := "gui/" + p.UserID + "/" + p.serviceID()
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 8*time.Second)
		defer stop()
		command(cleanup, "launchctl", "bootout", target)
	})
	run := func(ctx context.Context, name string, args ...string) (string, error) {
		// A fresh, unique test label is enabled by default. Avoid persisting
		// an enable override in the user's launchd preference database.
		if name == "launchctl" && args[0] == "enable" && args[1] == target {
			return "", nil
		}
		return command(ctx, name, args...)
	}
	if err := startService(ctx, p, run); err != nil {
		t.Fatal(err)
	}
	if err := waitEndpoint(ctx, p.Config.Listen, true); err != nil {
		t.Fatal(err)
	}
	if err := verifyStartedProxy(ctx, p); err != nil {
		t.Fatal(err)
	}
	owner, err := unixEndpointOwner(ctx, p, p.Config.Listen)
	if err != nil || owner == nil {
		t.Fatalf("missing running job: %v", err)
	}
	first := owner.pid
	owner.close()
	if err := startUnixProxy(ctx, p, run); err != nil {
		t.Fatal(err)
	}
	owner, err = unixEndpointOwner(ctx, p, p.Config.Listen)
	if err != nil || owner == nil {
		t.Fatalf("job disappeared: %v", err)
	}
	if owner.pid != first {
		t.Fatal("start created a duplicate")
	}
	owner.close()
	if err := stopUnix(ctx, p, false, run); err != nil {
		t.Fatal(err)
	}
	if state, err := serviceState(ctx, p, run); err != nil || state != "absent" {
		t.Fatalf("stopped job still registered: %s %v", state, err)
	}
	if _, err := os.Stat(p.ServicePath); err != nil {
		t.Fatal("login plist was removed")
	}
	if err := stopUnix(ctx, p, false, run); err != nil {
		t.Fatal(err)
	}
	if err := startUnixProxy(ctx, p, run); err != nil {
		t.Fatal(err)
	}
	owner, err = unixEndpointOwner(ctx, p, p.Config.Listen)
	if err != nil || owner == nil {
		t.Fatalf("job did not restart: %v", err)
	}
	if owner.pid == first {
		t.Fatal("restart retained the old process")
	}
	owner.close()
	if err := stopUnix(ctx, p, true, run); err != nil {
		t.Fatal(err)
	}
}
