package install

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnixTrayInstallUpgradeOptOutUninstall(t *testing.T) {
	for _, platform := range []string{"darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			o := fixture(t, platform)
			o.EnableTray = true
			if platform == "darwin" {
				o.NativeHelperSource = filepath.Join(t.TempDir(), "keepassxc-agent-tray")
				mustWrite(t, o.NativeHelperSource, []byte("native-helper-v1"))
			}
			p := mustPlan(t, o)
			manager := &fakeManager{}
			if !p.Manifest.TrayEnabled || p.Files[p.TrayServicePath] == nil {
				t.Fatal("tray not planned")
			}
			if err := p.Install(context.Background(), manager); err != nil {
				t.Fatal(err)
			}
			if platform == "darwin" {
				info, err := os.Stat(p.NativeHelper)
				if err != nil || info.Mode().Perm() != 0700 {
					t.Fatalf("native helper not executable: %v", err)
				}
				if path, err := p.NativeTrayExecutable(); err != nil || path != p.NativeHelper {
					t.Fatalf("helper not verified: %s %v", path, err)
				}
			}
			if platform == "linux" && !strings.Contains(string(mustRead(t, p.TrayAutostartPath)), "OnlyShowIn=KDE;") {
				t.Fatal("KDE autostart missing")
			}
			// A standalone CLI upgrade keeps an already verified macOS helper.
			o.NativeHelperSource = ""
			mustWrite(t, o.Source, []byte("proxy-v2"))
			upgrade := mustPlan(t, o)
			if !upgrade.Manifest.TrayEnabled {
				t.Fatal("upgrade lost tray")
			}
			if err := upgrade.Install(context.Background(), manager); err != nil {
				t.Fatal(err)
			}
			o.EnableTray = false
			off := mustPlan(t, o)
			if err := off.Install(context.Background(), manager); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{off.TrayServicePath, off.TrayAutostartPath} {
				if path != "" {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatalf("opt-out left startup file: %s", path)
					}
				}
			}
			removal, err := Existing(o)
			if err != nil {
				t.Fatal(err)
			}
			if err := removal.Uninstall(context.Background(), manager); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNativeHelperRemovalCanResume(t *testing.T) {
	o := fixture(t, "darwin")
	o.EnableTray = true
	o.NativeHelperSource = filepath.Join(t.TempDir(), "helper")
	mustWrite(t, o.NativeHelperSource, []byte("native"))
	p := mustPlan(t, o)
	m := &fakeManager{}
	if err := p.Install(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	p.Manifest.Uninstalling = true
	delete(p.Manifest.Files, p.NativeHelper)
	if err := os.Remove(p.NativeHelper); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(p.Manifest)
	mustWrite(t, p.ManifestPath, data)
	removal, err := Existing(o)
	if err != nil {
		t.Fatal(err)
	}
	if err := removal.Uninstall(context.Background(), m); err != nil {
		t.Fatal(err)
	}
}
