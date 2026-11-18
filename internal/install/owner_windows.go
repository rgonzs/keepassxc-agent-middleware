package install

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"keepassxc-agent-middleware/internal/lifecycle"
	"keepassxc-agent-middleware/internal/settings"
	"keepassxc-agent-middleware/internal/transport"
)

type endpointProcess struct {
	pid        uint32
	image      string
	handle     windows.Handle
	connection net.Conn
	window     uintptr
}

func (p *endpointProcess) close() {
	if p.connection != nil {
		p.connection.Close()
		p.connection = nil
	}
	if p.handle != 0 {
		windows.CloseHandle(p.handle)
		p.handle = 0
	}
}

// Inspect the actual server of the connected pipe, never a process-name match.
// Keep a process handle open through shutdown so a reused PID cannot redirect
// the eventual termination to a different process.
func endpointOwner(ctx context.Context, p *Plan, endpoint string) (*endpointProcess, error) {
	probe, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	conn, err := transport.Dial(probe, endpoint)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect proxy endpoint %s: %w", endpoint, err)
	}
	fail := func(err error) (*endpointProcess, error) { conn.Close(); return nil, err }
	file, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return fail(errors.New("proxy pipe does not expose its server handle"))
	}
	var pid uint32
	if err := windows.GetNamedPipeServerProcessId(windows.Handle(file.Fd()), &pid); err != nil {
		return fail(err)
	}
	owner, err := inspectProcess(p, pid)
	if err != nil {
		return fail(err)
	}
	owner.connection = conn
	return owner, nil
}

func inspectProcess(p *Plan, pid uint32) (*endpointProcess, error) {
	if pid == uint32(os.Getpid()) {
		return nil, errors.New("refusing to stop the current installer process")
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return nil, fmt.Errorf("open middleware PID %d: %w", pid, err)
	}
	owner := &endpointProcess{pid: pid, handle: handle}
	reject := func(err error) (*endpointProcess, error) {
		owner.close()
		return nil, fmt.Errorf("middleware PID %d (%s); refusing automatic control: %w", pid, owner.image, err)
	}
	var token windows.Token
	if err := windows.OpenProcessToken(handle, windows.TOKEN_QUERY, &token); err != nil {
		return reject(err)
	}
	user, err := token.GetTokenUser()
	token.Close()
	if err != nil {
		return reject(err)
	}
	current, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return reject(err)
	}
	if !user.User.Sid.Equals(current.User.Sid) {
		return reject(errors.New("process belongs to another Windows user"))
	}
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
		return reject(err)
	}
	owner.image = windows.UTF16ToString(buffer[:size])
	if err := trustedImage(p, owner.image); err != nil {
		return reject(err)
	}
	return owner, nil
}

var lifecycleUser32 = windows.NewLazySystemDLL("user32.dll")

func trayOwner(p *Plan) (*endpointProcess, error) {
	class, _ := windows.UTF16PtrFromString("KeePassXCAgentTrayWindow")
	hwnd, _, _ := lifecycleUser32.NewProc("FindWindowW").Call(uintptr(unsafe.Pointer(class)), 0)
	if hwnd == 0 {
		return nil, nil
	}
	var pid uint32
	thread, _, err := lifecycleUser32.NewProc("GetWindowThreadProcessId").Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if thread == 0 {
		return nil, err
	}
	owner, err := inspectProcess(p, pid)
	if err != nil {
		return nil, err
	}
	owner.window = hwnd
	return owner, nil
}

func processExited(ctx context.Context, handle windows.Handle) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		state, err := windows.WaitForSingleObject(handle, 100)
		if err != nil {
			return err
		}
		if state == windows.WAIT_OBJECT_0 {
			return nil
		}
	}
}

func (p *endpointProcess) stop(ctx context.Context) error {
	signaled, err := lifecycle.RequestStop(p.pid)
	if err != nil {
		return err
	}
	if !signaled && p.window != 0 {
		ok, _, _ := lifecycleUser32.NewProc("PostMessageW").Call(p.window, 0x10, 0, 0) // WM_CLOSE for older trays
		signaled = ok != 0
	}
	if signaled {
		grace, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := processExited(grace, p.handle)
		cancel()
		if err == nil {
			return nil
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Old versions have no stop event. The verified handle targets only this
	// installed process, even if the original task no longer owns it.
	if state, err := windows.WaitForSingleObject(p.handle, 0); err != nil {
		return err
	} else if state == windows.WAIT_OBJECT_0 {
		return nil
	}
	if err := windows.TerminateProcess(p.handle, 0); err != nil {
		if state, waitErr := windows.WaitForSingleObject(p.handle, 0); waitErr == nil && state == windows.WAIT_OBJECT_0 {
			return nil
		}
		return fmt.Errorf("stop proxy PID %d: %w", p.pid, err)
	}
	return processExited(ctx, p.handle)
}

func windowsTaskGuard(p *Plan, names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = ps(name)
	}
	return "$names = @(" + strings.Join(quoted, ",") + "); foreach ($name in $names) { $t = Get-ScheduledTask -TaskName $name -ErrorAction SilentlyContinue; if ($t -and $t.Description -ne " + ps("keepassxc-agent:"+p.Root) + ") { throw 'Refusing to control an unrelated scheduled task' } }; "
}

func stopWindows(ctx context.Context, p *Plan, remove bool) error {
	return stopWindowsTasks(ctx, p, remove, powershell)
}

func stopWindowsTasks(ctx context.Context, p *Plan, remove bool, run func(context.Context, string) (string, error)) error {
	endpoint := p.Config.Listen
	// During an upgrade --listen may change. Stop the endpoint in the installed
	// config before replacing it, rather than probing only the new address.
	if config, err := settings.Load(p.ConfigPath); err == nil {
		endpoint = config.Listen
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var owner *endpointProcess
	var err error
	if endpoint != "" {
		owner, err = endpointOwner(ctx, p, endpoint)
		if err != nil {
			return err
		}
		if owner != nil {
			defer owner.close()
		}
	}
	var controller *endpointProcess
	if remove {
		controller, err = trayOwner(p)
		if err != nil {
			return err
		}
		if controller != nil {
			defer controller.close()
		}
	}
	names := []string{p.TaskName}
	if remove {
		names = []string{p.TaskName + "-Tray", p.TaskName}
	}
	guard := windowsTaskGuard(p, names)
	// Disable before signaling: login/recovery triggers must not bring an old
	// proxy back while its files and task definition are being replaced.
	previous, err := run(ctx, guard+"foreach ($name in $names) { $t = Get-ScheduledTask -TaskName $name -ErrorAction SilentlyContinue; if ($t) { if ($t.Settings.Enabled) { 'enabled' }; Disable-ScheduledTask -TaskName $name | Out-Null } }")
	if err != nil {
		return err
	}
	if controller != nil {
		if err := controller.stop(ctx); err != nil {
			return err
		}
	}
	if owner != nil {
		if err := owner.stop(ctx); err != nil {
			return err
		}
		owner.connection.Close()
	}
	script := guard + "foreach ($name in $names) { $t = Get-ScheduledTask -TaskName $name -ErrorAction SilentlyContinue; if ($t) { if ($t.State -eq 'Running') { Stop-ScheduledTask -TaskName $name }; while ((Get-ScheduledTask -TaskName $name).State -eq 'Running') { Start-Sleep -Milliseconds 100 }; "
	if remove {
		script += "Unregister-ScheduledTask -TaskName $name -Confirm:$false; "
	}
	script += "} }"
	if _, err := run(ctx, script); err != nil {
		return err
	}
	if endpoint != "" {
		if err := waitEndpoint(ctx, endpoint, false); err != nil {
			return err
		}
	}
	if !remove && strings.TrimSpace(previous) == "enabled" {
		_, err := run(ctx, guard+"Enable-ScheduledTask -TaskName "+ps(p.TaskName)+" | Out-Null")
		return err
	}
	return nil
}

func startWindowsProxy(ctx context.Context, p *Plan) error {
	owner, err := endpointOwner(ctx, p, p.Config.Listen)
	if err != nil {
		return err
	}
	if owner != nil {
		defer owner.close()
	}
	script := windowsTaskGuard(p, []string{p.TaskName}) + "Enable-ScheduledTask -TaskName " + ps(p.TaskName) + " | Out-Null; "
	if owner == nil {
		script += "Start-ScheduledTask -TaskName " + ps(p.TaskName)
	}
	if _, err := powershell(ctx, script); err != nil {
		return err
	}
	if err := waitEndpoint(ctx, p.Config.Listen, true); err != nil {
		return err
	}
	// Readiness must come from a registered executable, not another agent that
	// happened to claim the pipe while the scheduled task was starting.
	active, err := endpointOwner(ctx, p, p.Config.Listen)
	if err != nil {
		return err
	}
	if active == nil {
		return errors.New("proxy exited before startup completed")
	}
	active.close()
	return nil
}

func verifyStartedProxy(ctx context.Context, p *Plan) error {
	owner, err := endpointOwner(ctx, p, p.Config.Listen)
	if err != nil {
		return err
	}
	if owner == nil {
		return errors.New("proxy exited before startup completed")
	}
	defer owner.close()
	actual, err := os.Stat(owner.image)
	if err != nil {
		return err
	}
	expected, err := os.Stat(p.Binary)
	if err != nil {
		return err
	}
	if !os.SameFile(actual, expected) {
		return fmt.Errorf("proxy PID %d is running a different installed version: %s", owner.pid, owner.image)
	}
	return nil
}

func stopUnix(context.Context, *Plan, bool, commandRunner) error {
	return errors.New("Unix service control is unavailable on Windows")
}
func startUnixProxy(context.Context, *Plan, commandRunner) error {
	return errors.New("Unix service control is unavailable on Windows")
}

// Only Unix rollback can restore an archived controller.
func restoreLegacyTray(context.Context, *Plan, commandRunner) error { return nil }
