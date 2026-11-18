package install

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func pathFixture(t *testing.T, platform string) Options {
	t.Helper()
	if runtime.GOOS == "windows" && platform != "windows" {
		t.Skip("Unix PATH tests require Unix paths and file permissions")
	}
	return fixture(t, platform)
}

func TestUserPathInstallUpgradeUninstall(t *testing.T) {
	for _, platform := range []string{"darwin", "linux", "windows"} {
		t.Run(platform, func(t *testing.T) {
			o := pathFixture(t, platform)
			o.ConfigurePath = true
			profile := filepath.Join(o.Home, ".profile")
			original := []byte("# user's settings without newline")
			if platform != "windows" {
				mustWrite(t, profile, original)
				if err := os.Chmod(profile, 0640); err != nil {
					t.Fatal(err)
				}
			}
			manager := &fakeManager{}
			p := mustPlan(t, o)
			if err := p.Install(context.Background(), manager); err != nil {
				t.Fatal(err)
			}
			launcher := mustRead(t, p.Launcher)
			if !bytes.Contains(launcher, []byte(p.Binary)) {
				t.Fatal("launcher did not select installed version")
			}
			if platform != "windows" {
				info, _ := os.Stat(profile)
				if info.Mode().Perm() != 0640 {
					t.Fatal("profile mode changed")
				}
				if bytes.Count(mustRead(t, profile), []byte(beginPath)) != 1 {
					t.Fatal("missing PATH block")
				}
			}
			mustWrite(t, o.Source, []byte("version two"))
			next := mustPlan(t, o)
			if err := next.Install(context.Background(), manager); err != nil {
				t.Fatal(err)
			}
			if next.Binary == p.Binary || !bytes.Contains(mustRead(t, next.Launcher), []byte(next.Binary)) {
				t.Fatal("stable CLI did not update")
			}
			if platform != "windows" {
				if bytes.Count(mustRead(t, profile), []byte(beginPath)) != 1 {
					t.Fatal("duplicate PATH block")
				}
				mustWrite(t, profile, append(mustRead(t, profile), []byte("\n# later user change\n")...))
			}
			removal, err := Existing(o)
			if err != nil {
				t.Fatal(err)
			}
			if err := removal.Uninstall(context.Background(), manager); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(p.Launcher); !os.IsNotExist(err) {
				t.Fatal("launcher remains")
			}
			if platform != "windows" {
				if got := string(mustRead(t, profile)); got != string(original)+"\n# later user change\n" {
					t.Fatalf("lost user profile settings: %q", got)
				}
				shellPath := filepath.Join(o.Home, ".bashrc")
				if platform == "darwin" {
					shellPath = filepath.Join(o.Home, ".zshrc")
				}
				if _, err := os.Stat(shellPath); !os.IsNotExist(err) {
					t.Fatal("installer-created empty profile remains")
				}
			}
		})
	}
}
func TestUserPathOptOutAndRollback(t *testing.T) {
	for _, platform := range []string{"darwin", "linux", "windows"} {
		t.Run(platform, func(t *testing.T) {
			o := pathFixture(t, platform)
			o.ConfigurePath = true
			manager := &fakeManager{failNext: 1}
			p := mustPlan(t, o)
			if err := p.Install(context.Background(), manager); err == nil {
				t.Fatal("expected startup failure")
			}
			if _, err := os.Stat(p.Launcher); !os.IsNotExist(err) {
				t.Fatal("failed install left launcher")
			}
			for path := range p.Profiles {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("failed install left profile")
				}
			}
			p = mustPlan(t, o)
			if err := p.Install(context.Background(), manager); err != nil {
				t.Fatal(err)
			}
			o.ConfigurePath = false
			off := mustPlan(t, o)
			if err := off.Install(context.Background(), manager); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(p.Launcher); !os.IsNotExist(err) {
				t.Fatal("opt-out left launcher")
			}
			if len(off.Manifest.PathEdits) != 0 || off.Manifest.Launcher != "" {
				t.Fatal("opt-out still manages PATH")
			}
		})
	}
}
func TestPathProfileConcurrentEditAndUnownedLauncher(t *testing.T) {
	o := pathFixture(t, "linux")
	o.ConfigurePath = true
	p := mustPlan(t, o)
	profile := filepath.Join(o.Home, ".profile")
	mustWrite(t, profile, []byte("external edit"))
	m := &fakeManager{}
	if err := p.Install(context.Background(), m); err == nil || !strings.Contains(err.Error(), "profile changed") {
		t.Fatalf("stale profile accepted: %v", err)
	}
	if m.starts != 0 {
		t.Fatal("service changed before rejecting profile")
	}
	p = mustPlan(t, o)
	mustWrite(t, p.Launcher, []byte("other application"))
	if _, err := NewPlan(o); err == nil {
		t.Fatal("unowned CLI would be overwritten")
	}
}
func TestWindowsPathOwnership(t *testing.T) {
	t.Setenv("KAM_PATH_BASE", `C:\Users\test\AppData\Local`)
	dir := `C:\Users\test\AppData\Local\Programs\keepassxc-agent`
	cases := []struct {
		value      string
		add, owned bool
		want       string
		wantOwned  bool
	}{
		{`C:\one`, true, false, `C:\one;` + dir, true},
		{`C:\one;` + strings.ToUpper(dir) + `\`, true, false, `C:\one;` + strings.ToUpper(dir) + `\`, false},
		{`C:\one;` + dir, true, true, `C:\one;` + dir, true},
		{`C:\one;` + dir + `;C:\new`, false, true, `C:\one;C:\new`, false},
		{dir, false, false, dir, false},
		{`%KAM_PATH_BASE%\Programs\keepassxc-agent`, true, false, `%KAM_PATH_BASE%\Programs\keepassxc-agent`, false},
		{`C:\new`, false, true, `C:\new`, false},
	}
	for _, c := range cases {
		got, owned := editWindowsPath(c.value, dir, c.add, c.owned)
		if got != c.want || owned != c.wantOwned {
			t.Fatalf("edit %q: %q,%v want %q,%v", c.value, got, owned, c.want, c.wantOwned)
		}
	}
}
func TestUpgradeRetiresLegacyTrayFiles(t *testing.T) {
	for _, platform := range []string{"darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			o := pathFixture(t, platform)
			p := mustPlan(t, o)
			manager := &fakeManager{}
			if err := p.Install(context.Background(), manager); err != nil {
				t.Fatal(err)
			}
			legacy := p.Manifest
			legacy.TrayEnabled = true
			startup := []byte("legacy UI startup definition")
			mustWrite(t, p.TrayServicePath, startup)
			legacy.Files[p.TrayServicePath] = digest(startup)
			if platform == "linux" {
				mustWrite(t, p.TrayAutostartPath, startup)
				legacy.Files[p.TrayAutostartPath] = digest(startup)
			} else {
				helper := []byte("legacy AppKit helper")
				legacy.NativeHelper = filepath.Join(p.Root, "bin", "keepassxc-agent-tray-"+digest(helper)[:16])
				mustWrite(t, legacy.NativeHelper, helper)
				legacy.Files[legacy.NativeHelper] = digest(helper)
			}
			data, _ := json.Marshal(legacy)
			mustWrite(t, p.ManifestPath, data)
			o.ConfigurePath = true
			next := mustPlan(t, o)
			if err := next.Install(context.Background(), manager); err != nil {
				t.Fatal(err)
			}
			if next.Manifest.TrayEnabled {
				t.Fatal("new install enables UI")
			}
			for _, path := range []string{p.TrayServicePath, p.TrayAutostartPath} {
				if path != "" {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatalf("legacy startup remains: %s", path)
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

func TestRegistryPathTransactionPreservesConcurrentEdits(t *testing.T) {
	before := pathValue{Value: `C:\old`, Type: 2, Exists: true}
	after := pathValue{Value: `C:\old;C:\managed`, Type: 2, Exists: true}
	value := before
	read := func() (pathValue, error) { return value, nil }
	write := func(v pathValue) error { value = v; return nil }
	p := &Plan{PathBefore: &before, PathAfter: after}
	undo, err := p.applyUserPathUsing(read, write)
	if err != nil {
		t.Fatal(err)
	}
	if value != after {
		t.Fatal("registry not updated")
	}
	if err := undo(); err != nil || value != before {
		t.Fatalf("rollback did not restore original: %v", err)
	}
	value = pathValue{Value: "external edit", Type: 1, Exists: true}
	if _, err := p.applyUserPathUsing(read, write); err == nil || value.Value != "external edit" {
		t.Fatal("concurrent edit overwritten")
	}
	value = before
	undo, err = p.applyUserPathUsing(read, write)
	if err != nil {
		t.Fatal(err)
	}
	value.Value += `;C:\later`
	if err := undo(); err == nil || !strings.Contains(value.Value, "later") {
		t.Fatal("rollback overwrote later edit")
	}
	value = before
	if _, err := p.applyUserPathUsing(read, func(pathValue) error { return errors.New("registry denied") }); err == nil {
		t.Fatal("write error discarded")
	}
}
