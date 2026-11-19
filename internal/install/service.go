package install

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"unicode/utf16"
)

func xmlText(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
func ps(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func unitArg(s string) string {
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$", "\n", `\n`, "\r", `\r`).Replace(s)
	return `"` + s + `"`
}

func encodedScript(script string) string {
	words := utf16.Encode([]rune(script))
	data := make([]byte, len(words)*2)
	for i, w := range words {
		binary.LittleEndian.PutUint16(data[i*2:], w)
	}
	return base64.StdEncoding.EncodeToString(data)
}

func (p *Plan) serviceFile() []byte {
	switch p.OS {
	case "darwin":
		return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array><string>%s</string><string>run</string><string>--config</string><string>%s</string></array>
<key>RunAtLoad</key><true/><key>KeepAlive</key><true/>
<key>ThrottleInterval</key><integer>10</integer>
<key>ExitTimeOut</key><integer>5</integer>
<key>StandardOutPath</key><string>%s</string>
<key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, p.serviceID(), xmlText(p.Binary), xmlText(p.ConfigPath), xmlText(p.LogPath), xmlText(p.LogPath)))
	case "linux":
		return []byte(fmt.Sprintf(`[Unit]
Description=KeePassXC SSH agent middleware
PartOf=graphical-session.target

[Service]
Type=exec
ExecStart=%s run --config %s
Restart=always
RestartSec=5
KillMode=control-group
TimeoutStopSec=5s
UMask=0077

[Install]
WantedBy=default.target
`, unitArg(p.Binary), unitArg(p.ConfigPath)))
	case "windows":
		// Windows paths cannot contain double quotes; ConfigPath ends in
		// config.json, so quoting it handles spaces without a trailing backslash.
		arguments := `run --config "` + p.ConfigPath + `"`
		return []byte(fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$name = %s
$trayName = $name + '-Tray'
$description = %s
foreach ($taskName in @($name, $trayName)) {
    $old = Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
    if ($old -and $old.Description -ne $description) { throw 'Refusing to replace an unrelated scheduled task' }
}
$identity = [System.Security.Principal.WindowsIdentity]::GetCurrent().Name
$action = New-ScheduledTaskAction -Execute %s -Argument %s
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $identity
$principal = New-ScheduledTaskPrincipal -UserId $identity -LogonType Interactive -RunLevel Limited
$settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) -MultipleInstances IgnoreNew -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
Register-ScheduledTask -TaskName $name -Description $description -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null
Enable-ScheduledTask -TaskName $name | Out-Null
Start-ScheduledTask -TaskName $name
`, ps(p.TaskName), ps("keepassxc-agent:"+p.Root), ps(p.serviceBinary()), ps(arguments)))
	}
	return nil
}

type Manager interface {
	State(context.Context, *Plan) (string, error)
	Start(context.Context, *Plan) error
	Stop(context.Context, *Plan) error
}

type SystemManager struct{}

func command(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	hideWindow(cmd)
	// Only stdout is the command's result. Windows PowerShell can emit
	// informational CLIXML on stderr even when the command succeeds.
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		data := stdout.String() + stderr.String()
		return data, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(data))
	}
	return strings.TrimSpace(stdout.String()), nil
}
func powershell(ctx context.Context, script string) (string, error) {
	return command(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-EncodedCommand", encodedScript("$ErrorActionPreference = 'Stop'; "+script))
}
func (SystemManager) State(ctx context.Context, p *Plan) (string, error) {
	return serviceState(ctx, p, command)
}

type commandRunner func(context.Context, string, ...string) (string, error)

func serviceState(ctx context.Context, p *Plan, run commandRunner) (string, error) {
	switch p.OS {
	case "darwin":
		text, err := run(ctx, "launchctl", "print", "gui/"+p.UserID+"/"+p.serviceID())
		if err != nil {
			var exit *exec.ExitError
			if (errors.As(err, &exit) || errors.Is(err, os.ErrNotExist)) && strings.Contains(text, "Could not find service") {
				return "absent", nil
			}
			return "", err
		}
		return "registered", nil
	case "linux":
		text, err := run(ctx, "systemctl", "--user", "show", p.serviceID(), "--property=LoadState", "--value")
		if text == "not-found" {
			return "absent", nil
		}
		if err != nil {
			return "", err
		}
		if text != "loaded" {
			return "", fmt.Errorf("unexpected systemd LoadState: %s", text)
		}
		return "registered", nil
	case "windows":
		state, err := powershell(ctx, "$state = 'absent'; foreach ($name in @("+ps(p.TaskName)+", "+ps(p.TaskName+"-Tray")+")) { $t = Get-ScheduledTask -TaskName $name -ErrorAction SilentlyContinue; if ($t) { if ($t.Description -ne "+ps("keepassxc-agent:"+p.Root)+") { throw 'Unrelated scheduled task uses this name' }; $state = 'registered' } }; $state")
		if err != nil {
			return "", err
		}
		if state != "absent" && state != "registered" {
			return "", fmt.Errorf("unexpected scheduled task state: %q", state)
		}
		return state, nil
	}
	return "", errors.New("unsupported service manager")
}
func (m SystemManager) Stop(ctx context.Context, p *Plan) error {
	if p.OS != "windows" {
		return stopUnix(ctx, p, true, command)
	}
	_, err := m.State(ctx, p)
	if err != nil {
		return err
	}
	return stopWindows(ctx, p, true)
}
func (SystemManager) start(ctx context.Context, p *Plan) error {
	if err := startService(ctx, p, command); err != nil {
		return err
	}
	if p.OS != "windows" {
		return restoreLegacyTray(ctx, p, command)
	}
	return nil
}

func startService(ctx context.Context, p *Plan, run commandRunner) error {
	switch p.OS {
	case "darwin":
		if _, err := run(ctx, "launchctl", "enable", "gui/"+p.UserID+"/"+p.serviceID()); err != nil {
			return err
		}
		_, err := run(ctx, "launchctl", "bootstrap", "gui/"+p.UserID, p.ServicePath)
		return err
	case "linux":
		if _, err := run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
			return err
		}
		if _, err := run(ctx, "systemctl", "--user", "enable", p.serviceID()); err != nil {
			return err
		}
		_, err := run(ctx, "systemctl", "--user", "restart", p.serviceID())
		return err
	case "windows":
		data, err := os.ReadFile(p.ServicePath)
		if err != nil {
			return err
		}
		_, err = powershell(ctx, string(data))
		return err
	}
	return errors.New("unsupported service manager")
}
