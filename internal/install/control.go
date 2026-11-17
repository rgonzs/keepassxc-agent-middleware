package install

import (
	"context"
	"errors"
	"fmt"
	"time"

	"keepassxc-agent-middleware/internal/transport"
)

// OpenTray always uses the registered task, so upgrades and uninstallation can
// stop the controller and release its executable, including manual openings.
func (SystemManager) OpenTray(ctx context.Context, p *Plan) error {
	if p.OS != "windows" {
		return errors.New("the tray controller is available on Windows only")
	}
	_, err := powershell(ctx, "$t = Get-ScheduledTask -TaskName "+ps(p.TaskName+"-Tray")+"; if ($t.Description -ne "+ps("keepassxc-agent:"+p.Root)+") { throw 'Refusing to control an unrelated scheduled task' }; Start-ScheduledTask -TaskName "+ps(p.TaskName+"-Tray"))
	return err
}

// Control operates the owned proxy task without unregistering it or touching
// the independent tray task. Stop is reserved for installation/removal.
func (SystemManager) Control(ctx context.Context, p *Plan, action string) error {
	if p.OS != "windows" {
		return errors.New("task control is available on Windows only")
	}
	if action != "start" && action != "stop" && action != "restart" {
		return fmt.Errorf("unknown task action %q", action)
	}
	guard := "$t = Get-ScheduledTask -TaskName " + ps(p.TaskName) + "; if ($t.Description -ne " + ps("keepassxc-agent:"+p.Root) + ") { throw 'Refusing to control an unrelated scheduled task' }; "
	if action == "stop" || action == "restart" {
		if _, err := powershell(ctx, guard+"Stop-ScheduledTask -TaskName "+ps(p.TaskName)); err != nil {
			return err
		}
		if err := waitEndpoint(ctx, p.Config.Listen, false); err != nil {
			return err
		}
	}
	if action == "start" || action == "restart" {
		if _, err := powershell(ctx, guard+"Start-ScheduledTask -TaskName "+ps(p.TaskName)); err != nil {
			return err
		}
		return waitEndpoint(ctx, p.Config.Listen, true)
	}
	return nil
}

func waitEndpoint(parent context.Context, endpoint string, reachable bool) error {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	for {
		if err := ctx.Err(); err != nil { return err }
		probe, stop := context.WithTimeout(ctx, 250*time.Millisecond)
		conn, err := transport.Dial(probe, endpoint)
		stop()
		if err == nil {
			conn.Close()
		}
		if err := ctx.Err(); err != nil { return err }
		if (err == nil) == reachable {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("proxy did not reach requested state (reachable=%t): %w", reachable, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}
