package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (p *Plan) prepareTray() error {
	if !p.EnableTray || p.OS == "windows" {
		return nil
	}
	if p.OS == "darwin" {
		source := p.NativeHelperSource
		if source == "" && p.Previous != nil {
			source = p.Previous.NativeHelper
		}
		if source == "" {
			p.EnableTray = false
			return nil
		} // Standalone CLI remains usable without AppKit helper.
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		p.NativeHelper = filepath.Join(p.Root, "bin", "keepassxc-agent-tray-"+digest(data)[:16])
		p.Files[p.NativeHelper] = data
		p.Files[p.TrayServicePath] = []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>%s.tray</string>
<key>ProgramArguments</key><array><string>%s</string><string>tray</string><string>--background</string></array>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
<key>ThrottleInterval</key><integer>10</integer>
<key>StandardOutPath</key><string>%s</string><key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, xmlText(p.serviceID()), xmlText(p.Binary), xmlText(filepath.Join(p.Root, "tray.log")), xmlText(filepath.Join(p.Root, "tray.log"))))
	} else {
		p.Files[p.TrayServicePath] = []byte(fmt.Sprintf(`[Unit]
Description=KeePassXC agent tray controller
PartOf=graphical-session.target

[Service]
Type=exec
ExecStart=%s tray --background
Restart=on-failure
RestartSec=5
KillMode=control-group
TimeoutStopSec=5s
UMask=0077
`, unitArg(p.Binary)))
		// Desktop Exec is not shell syntax. Quote reserved characters, including
		// field-code percent signs and literal backslashes (desktop + argv layers).
		escaped := strings.NewReplacer(`\`, `\\\\`, `"`, `\\"`, "`", "\\\\`", "$", `\\$`, "%", "%%").Replace(p.Binary)
		if strings.ContainsAny(escaped, "\r\n\t=") {
			return errors.New("tray executable path cannot contain tabs, newlines or equals signs")
		}
		p.Files[p.TrayAutostartPath] = []byte(fmt.Sprintf("[Desktop Entry]\nType=Application\nName=KeePassXC Agent\nComment=Controlar el proxy SSH de KeePassXC\nExec=\"%s\" tray\nTerminal=false\nOnlyShowIn=KDE;\nX-KDE-autostart-phase=2\n", escaped))
	}
	return nil
}

func (p *Plan) NativeTrayExecutable() (string, error) {
	m, err := ReadManifest(p.ManifestPath)
	if err != nil {
		return "", err
	}
	if err := p.validateManifest(m); err != nil {
		return "", err
	}
	if !m.TrayEnabled || m.NativeHelper == "" {
		return "", errors.New("native tray helper is not installed; use the macOS package or build the helper")
	}
	data, exists, err := readRegular(m.NativeHelper)
	if err != nil {
		return "", err
	}
	if !exists || digest(data) != m.Files[m.NativeHelper] {
		return "", errors.New("managed native tray executable was changed or removed")
	}
	return m.NativeHelper, nil
}
