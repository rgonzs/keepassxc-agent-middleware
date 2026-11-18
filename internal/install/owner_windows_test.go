package install

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"keepassxc-agent-middleware/internal/lifecycle"
	"keepassxc-agent-middleware/internal/testutil"
	"keepassxc-agent-middleware/internal/transport"
)

func TestRecoverOwnedPipeProcess(t *testing.T) {
	for _, mode := range []string{"cooperative", "legacy", "foreign"} {
		t.Run(mode, func(t *testing.T) {
			o := fixture(t, "windows")
			source, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			data := mustRead(t, source)
			mustWrite(t, o.Source, data)
			o.Config.Listen = testutil.PipeEndpoint("kam-lifecycle")
			p := mustPlan(t, o)
			mustWrite(t, p.Binary, data)
			binary := p.Binary
			if mode == "foreign" {
				binary = source
			}
			result := filepath.Join(t.TempDir(), "stopped.txt")
			cmd := exec.Command(binary, "-test.run=^TestPipeLifecycleHelper$")
			cmd.Env = append(os.Environ(), "KAM_TEST_PIPE="+o.Config.Listen, "KAM_TEST_MODE="+mode, "KAM_TEST_RESULT="+result)
			hideWindow(cmd)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer cmd.Process.Kill()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := waitEndpoint(ctx, o.Config.Listen, true); err != nil {
				t.Fatal(err)
			}
			owner, err := endpointOwner(ctx, p, o.Config.Listen)
			calls := 0
			tasks := func(_ context.Context, script string) (string, error) {
				calls++
				handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(cmd.Process.Pid))
				if err != nil {
					t.Fatal(err)
				}
				defer windows.CloseHandle(handle)
				state, err := windows.WaitForSingleObject(handle, 0)
				if err != nil {
					t.Fatal(err)
				}
				if calls == 1 {
					if !strings.Contains(script, "Disable-ScheduledTask") || state != uint32(windows.WAIT_TIMEOUT) {
						t.Fatal("task was not disabled before stopping the child")
					}
					return "enabled", nil
				}
				if state != windows.WAIT_OBJECT_0 {
					t.Fatal("task cleanup ran before the actual child exited")
				}
				if strings.Contains(script, "Unregister-ScheduledTask") || strings.Contains(script, p.TaskName+"-Tray") {
					t.Fatal("stop removed login registration or touched the tray")
				}
				return "", nil
			}
			if mode == "foreign" {
				if err == nil || !strings.Contains(err.Error(), "refusing automatic control") {
					t.Fatalf("accepted foreign pipe owner: %v", err)
				}
				if err := stopWindowsTasks(ctx, p, false, tasks); err == nil || calls != 0 {
					t.Fatal("foreign endpoint caused task mutations")
				}
				handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(cmd.Process.Pid))
				if err != nil {
					t.Fatal(err)
				}
				defer windows.CloseHandle(handle)
				state, err := windows.WaitForSingleObject(handle, 0)
				if err != nil || state != uint32(windows.WAIT_TIMEOUT) {
					t.Fatalf("foreign process was stopped: %v %v", state, err)
				}
				cmd.Process.Kill()
				cmd.Wait()
				return
			}
			if err != nil || owner == nil {
				t.Fatalf("missing owned process: %v", err)
			}
			defer owner.close()
			if owner.pid != uint32(cmd.Process.Pid) {
				t.Fatalf("wrong pipe owner: %d", owner.pid)
			}
			owner.close()
			if err := stopWindowsTasks(ctx, p, false, tasks); err != nil {
				t.Fatal(err)
			}
			if calls != 3 {
				t.Fatalf("login enablement was not restored: %d task operations", calls)
			}
			if err := cmd.Wait(); err != nil {
				t.Fatal(err)
			}
			if err := waitEndpoint(ctx, o.Config.Listen, false); err != nil {
				t.Fatal(err)
			}
			if mode == "cooperative" && string(mustRead(t, result)) != "graceful" {
				t.Fatal("cooperative shutdown was bypassed")
			}
			if other, err := endpointOwner(ctx, p, o.Config.Listen); err != nil || other != nil {
				t.Fatalf("stopped endpoint still active: %v", err)
			}
		})
	}
}

func TestPipeLifecycleHelper(t *testing.T) {
	pipe := os.Getenv("KAM_TEST_PIPE")
	if pipe == "" {
		return
	}
	ctx := context.Background()
	if os.Getenv("KAM_TEST_MODE") == "cooperative" {
		var cancel func()
		var err error
		ctx, cancel, err = lifecycle.Context(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer cancel()
	}
	listener, err := transport.Listen(pipe)
	if err != nil {
		t.Fatal(err)
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
	if err := os.WriteFile(os.Getenv("KAM_TEST_RESULT"), []byte("graceful"), 0600); err != nil {
		t.Fatal(err)
	}
}
