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
<key>StandardOutPath</key><string>%s</string>
<key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, Label, xmlText(p.Binary), xmlText(p.ConfigPath), xmlText(p.LogPath), xmlText(p.LogPath)))
	case "linux":
		return []byte(fmt.Sprintf(`[Unit]
Description=KeePassXC SSH agent middleware
PartOf=graphical-session.target

[Service]
Type=exec
ExecStart=%s run --config %s
Restart=always
RestartSec=5
UMask=0077

[Install]
WantedBy=default.target
`, unitArg(p.Binary), unitArg(p.ConfigPath)))
	case "windows":
		// A hidden PowerShell host remains attached to the native child, allowing
		// Task Scheduler to monitor and restart it without a console window.
		worker := "& " + ps(p.Binary) + " run --config " + ps(p.ConfigPath) + "; exit $LASTEXITCODE"
		return []byte(fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$name = %s
$description = %s
$old = Get-ScheduledTask -TaskName $name -ErrorAction SilentlyContinue
if ($old -and $old.Description -ne $description) { throw 'Refusing to replace an unrelated scheduled task' }
$identity = [System.Security.Principal.WindowsIdentity]::GetCurrent().Name
$action = New-ScheduledTaskAction -Execute (Join-Path $PSHOME 'powershell.exe') -Argument %s
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $identity
$principal = New-ScheduledTaskPrincipal -UserId $identity -LogonType Interactive -RunLevel Limited
$settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) -MultipleInstances IgnoreNew -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
Register-ScheduledTask -TaskName $name -Description $description -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null
Start-ScheduledTask -TaskName $name
`, ps(p.TaskName), ps("keepassxc-agent:"+p.Root), ps("-NoProfile -NonInteractive -WindowStyle Hidden -EncodedCommand "+encodedScript(worker))))
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
	data, err := cmd.CombinedOutput()
	if err != nil {
		return string(data), fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(data)))
	}
	return strings.TrimSpace(string(data)), nil
}
func powershell(ctx context.Context, script string) (string, error) {
	return command(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-EncodedCommand", encodedScript("$ErrorActionPreference = 'Stop'; "+script))
}
func (SystemManager) State(ctx context.Context, p *Plan) (string, error) {
	switch p.OS {
	case "darwin":
		text, err := command(ctx, "launchctl", "print", "gui/"+p.UserID+"/"+Label)
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) && strings.Contains(text, "Could not find service") {
				return "absent", nil
			}
			return "", err
		}
		return "registered", nil
	case "linux":
		text, err := command(ctx, "systemctl", "--user", "show", "keepassxc-agent.service", "--property=LoadState", "--value")
		if err != nil {
			return "", err
		}
		if text == "not-found" {
			return "absent", nil
		}
		if text != "loaded" {
			return "", fmt.Errorf("unexpected systemd LoadState: %s", text)
		}
		return "registered", nil
	case "windows":
		return powershell(ctx, "$t = Get-ScheduledTask -TaskName "+ps(p.TaskName)+" -ErrorAction SilentlyContinue; if ($t) { if ($t.Description -ne "+ps("keepassxc-agent:"+p.Root)+") { throw 'Unrelated scheduled task uses this name' }; 'registered' } else { 'absent' }")
	}
	return "", errors.New("unsupported service manager")
}
func (m SystemManager) Stop(ctx context.Context, p *Plan) error {
	state, err := m.State(ctx, p)
	if err != nil || state == "absent" {
		return err
	}
	switch p.OS {
	case "darwin":
		_, err = command(ctx, "launchctl", "bootout", "gui/"+p.UserID+"/"+Label)
	case "linux":
		_, err = command(ctx, "systemctl", "--user", "disable", "--now", "keepassxc-agent.service")
	case "windows":
		_, err = powershell(ctx, "Stop-ScheduledTask -TaskName "+ps(p.TaskName)+"; Unregister-ScheduledTask -TaskName "+ps(p.TaskName)+" -Confirm:$false")
	}
	return err
}
func (SystemManager) start(ctx context.Context, p *Plan) error {
	switch p.OS {
	case "darwin":
		if _, err := command(ctx, "launchctl", "enable", "gui/"+p.UserID+"/"+Label); err != nil {
			return err
		}
		_, err := command(ctx, "launchctl", "bootstrap", "gui/"+p.UserID, p.ServicePath)
		return err
	case "linux":
		if _, err := command(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
			return err
		}
		if _, err := command(ctx, "systemctl", "--user", "enable", "keepassxc-agent.service"); err != nil {
			return err
		}
		_, err := command(ctx, "systemctl", "--user", "restart", "keepassxc-agent.service")
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
