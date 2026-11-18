//go:build !windows

package install

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"keepassxc-agent-middleware/internal/settings"
	"keepassxc-agent-middleware/internal/transport"
)

// The copied test executable acts as an isolated proxy when launched by a
// simulated service manager. It never opens the user's KeePassXC or SSH agent.
func TestMain(m *testing.M) {
	if len(os.Args) == 4 && os.Args[1] == "run" && os.Args[2] == "--config" {
		if err := isolatedProxy(os.Args[3]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func isolatedProxy(path string) error {
	config, err := settings.Load(path)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if os.Getenv("KAM_TEST_IGNORE_TERM") == "1" {
		signal.Ignore(syscall.SIGTERM)
	}
	listener, err := transport.Listen(config.Listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); io.Copy(io.Discard, conn) }()
		}
	}()
	<-ctx.Done()
	if result := os.Getenv("KAM_TEST_STOP_RESULT"); result != "" {
		return os.WriteFile(result, []byte("graceful"), 0600)
	}
	return nil
}

func isolatedUnixPlan(t *testing.T) *Plan {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "kam-owner-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p := mustPlan(t, Options{Home: dir, OS: runtime.GOOS, Source: source, UserID: strconv.Itoa(os.Getuid()), Config: settings.Config{Listen: filepath.Join(dir, "agent.sock"), FallbackUpstream: filepath.Join(dir, "unused-original.sock"), Timeout: "1s"}})
	for path, data := range p.Files {
		mustWrite(t, path, data)
	}
	if err := os.Chmod(p.Binary, 0700); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(p.Manifest)
	mustWrite(t, p.ManifestPath, manifest)
	return p
}

func launchIsolatedProxy(t *testing.T, p *Plan, binary string, ignoreTerm bool) (*exec.Cmd, string) {
	t.Helper()
	result := filepath.Join(t.TempDir(), "stopped")
	cmd := exec.Command(binary, "run", "--config", p.ConfigPath)
	cmd.Env = append(os.Environ(), "KAM_TEST_STOP_RESULT="+result)
	if ignoreTerm {
		cmd.Env = append(cmd.Env, "KAM_TEST_IGNORE_TERM=1")
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := waitEndpoint(ctx, p.Config.Listen, true); err != nil {
		t.Fatal(err)
	}
	return cmd, result
}

func absentUnixManager(_ context.Context, name string, args ...string) (string, error) {
	if name == "launchctl" && args[0] == "print" {
		return "Could not find service", os.ErrNotExist
	}
	if name == "systemctl" && strings.Contains(strings.Join(args, " "), "LoadState") {
		return "not-found", nil
	}
	return "", fmt.Errorf("unexpected service mutation: %s %v", name, args)
}

func TestUnixOwnedOrphanLifecycle(t *testing.T) {
	for _, mode := range []string{"cooperative", "stubborn", "foreign"} {
		t.Run(mode, func(t *testing.T) {
			p := isolatedUnixPlan(t)
			binary := p.Binary
			if mode == "foreign" {
				binary, _ = os.Executable()
			}
			cmd, result := launchIsolatedProxy(t, p, binary, mode == "stubborn")
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			owner, err := unixEndpointOwner(ctx, p, p.Config.Listen)
			if mode == "foreign" {
				if err == nil || owner != nil || !strings.Contains(err.Error(), "refusing automatic control") {
					t.Fatalf("foreign process accepted: %v", err)
				}
				calls := 0
				run := func(ctx context.Context, name string, args ...string) (string, error) {
					calls++
					return absentUnixManager(ctx, name, args...)
				}
				if err := stopUnix(ctx, p, true, run); err == nil || calls != 1 {
					t.Fatalf("foreign socket caused service mutation: %d %v", calls, err)
				}
				if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
					t.Fatalf("foreign process was stopped: %v", err)
				}
				return
			}
			if err != nil || owner == nil {
				t.Fatalf("cannot identify own process: %v", err)
			}
			if owner.pid != cmd.Process.Pid {
				t.Fatalf("wrong peer: %d", owner.pid)
			}
			owner.close()
			// Starting twice must not ask the manager to create another process.
			if err := startUnixProxy(ctx, p, func(context.Context, string, ...string) (string, error) {
				t.Fatal("duplicate service start")
				return "", nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := stopUnix(ctx, p, false, absentUnixManager); err != nil {
				t.Fatal(err)
			}
			waitErr := cmd.Wait()
			if mode == "cooperative" {
				if waitErr != nil || string(mustRead(t, result)) != "graceful" {
					t.Fatalf("not a graceful stop: %v", waitErr)
				}
			} else if waitErr == nil {
				t.Fatal("stubborn process was not terminated")
			}
			// A second stop is harmless, including the stale socket of SIGKILL.
			if err := stopUnix(ctx, p, false, absentUnixManager); err != nil {
				t.Fatal(err)
			}
			// A different process identity must never receive a signal.
			fake := &unixProcess{pid: os.Getpid(), fd: -1, identity: processIdentity{uid: uint32(os.Getuid()), birth: "stale"}}
			if err := fake.signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUnixStopPreservesLoginConfiguration(t *testing.T) {
	p := isolatedUnixPlan(t)
	cmd, _ := launchIsolatedProxy(t, p, p.Binary, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var calls []string
	run := func(_ context.Context, name string, args ...string) (string, error) {
		call := name + " " + strings.Join(args, " ")
		calls = append(calls, call)
		if name == "launchctl" {
			switch args[0] {
			case "print":
				return "registered", nil
			case "bootout":
				return "", nil
			}
		} else {
			if strings.Contains(call, "LoadState") {
				return "loaded", nil
			}
			if strings.Contains(call, "ActiveState") {
				return "inactive", nil
			}
			if strings.Contains(call, " stop --no-block ") {
				return "", nil
			}
		}
		return "", fmt.Errorf("unexpected login configuration change: %s", call)
	}
	if err := stopUnix(ctx, p, false, run); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "darwin" {
		for _, call := range calls {
			if strings.Contains(call, " enable ") || strings.Contains(call, " disable ") {
				t.Fatalf("stop changed login preference: %v", calls)
			}
		}
	} else {
		for _, call := range calls {
			if strings.Contains(call, " disable ") {
				t.Fatalf("stop disabled login startup: %v", calls)
			}
		}
	}
	if _, err := os.Stat(p.ServicePath); err != nil {
		t.Fatal("stop removed login definition")
	}
}

func TestUnixStopUsesInstalledEndpoint(t *testing.T) {
	p := isolatedUnixPlan(t)
	old := p.Config.Listen
	cmd, result := launchIsolatedProxy(t, p, p.Binary, false)
	p.Config.Listen = filepath.Join(filepath.Dir(old), "next.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := stopUnix(ctx, p, true, absentUnixManager); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if string(mustRead(t, result)) != "graceful" {
		t.Fatal("old endpoint did not stop")
	}
	if err := waitEndpoint(ctx, old, false); err != nil {
		t.Fatal(err)
	}
}

func TestUnixRefusesChangedService(t *testing.T) {
	p := isolatedUnixPlan(t)
	cmd, _ := launchIsolatedProxy(t, p, p.Binary, false)
	mustWrite(t, p.ServicePath, []byte("manually edited"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	calls := 0
	run := func(context.Context, string, ...string) (string, error) {
		calls++
		if calls > 1 {
			t.Fatal("changed definition caused a service mutation")
		}
		if p.OS == "darwin" {
			return "registered", nil
		}
		return "loaded", nil
	}
	if err := stopUnix(ctx, p, false, run); err == nil || !strings.Contains(err.Error(), "changed service definition") {
		t.Fatalf("edited service accepted: %v", err)
	}
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("edited service's process was stopped")
	}
}
