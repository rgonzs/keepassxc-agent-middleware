package install

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"keepassxc-agent-middleware/internal/settings"
)

type fakeManager struct {
	state                   string
	lastBinary              string
	starts, stops, failNext int
}

func (m *fakeManager) State(context.Context, *Plan) (string, error) {
	if m.state == "" {
		return "absent", nil
	}
	return m.state, nil
}
func (m *fakeManager) Start(_ context.Context, p *Plan) error {
	m.lastBinary = p.Binary
	m.starts++
	if m.failNext > 0 {
		m.failNext--
		return errors.New("simulated activation failure")
	}
	m.state = "registered"
	return nil
}
func (m *fakeManager) Stop(context.Context, *Plan) error { m.stops++; m.state = "absent"; return nil }

func fixture(t *testing.T, platform string) Options {
	t.Helper()
	home := t.TempDir()
	source := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(source, fixtureBinary(platform, "test-binary-v1"), 0700); err != nil {
		t.Fatal(err)
	}
	return Options{Home: home, OS: platform, Source: source, UserID: "501", ConfigureSSH: true, Config: settings.Config{Listen: filepath.Join(home, ".keepassxc-agent", "agent.sock"), FallbackUpstream: "/tmp/original-agent", Timeout: "60s"}}
}

func fixtureBinary(platform, version string) []byte {
	if platform != "windows" {
		return []byte(version)
	}
	// Minimal PE32+ headers for installer tests that never execute the fixture.
	const peStart, optional = 0x80, 0x80 + 24
	image := make([]byte, optional+112)
	copy(image, "MZ")
	binary.LittleEndian.PutUint32(image[0x3c:], peStart)
	copy(image[peStart:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(image[peStart+20:], 112)
	binary.LittleEndian.PutUint16(image[optional:], 0x20b)
	binary.LittleEndian.PutUint16(image[optional+68:], 3)
	return append(image, version...)
}
func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func mustPlan(t *testing.T, o Options) *Plan {
	t.Helper()
	p, err := NewPlan(o)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWindowsUpgradeFromPowerShellTask(t *testing.T) {
	o := fixture(t, "windows")
	legacy := mustPlan(t, o)
	delete(legacy.Files, legacy.Manifest.ServiceBinary)
	delete(legacy.Manifest.Files, legacy.Manifest.ServiceBinary)
	legacy.Manifest.ServiceBinary = ""
	legacyScript := []byte("legacy PowerShell task registration")
	legacy.Files[legacy.ServicePath] = legacyScript
	legacy.Manifest.Files[legacy.ServicePath] = digest(legacyScript)
	manager := &fakeManager{}
	ctx := context.Background()
	if err := legacy.Install(ctx, manager); err != nil {
		t.Fatal(err)
	}

	mustWrite(t, o.Source, fixtureBinary("windows", "test-binary-v2"))
	upgrade := mustPlan(t, o)
	manager.failNext = 1
	if err := upgrade.Install(ctx, manager); err == nil {
		t.Fatal("simulated activation failure was ignored")
	}
	if manager.lastBinary != legacy.Binary || !bytes.Equal(mustRead(t, legacy.ServicePath), legacyScript) {
		t.Fatal("failed upgrade did not restore the legacy task")
	}
	if err := upgrade.Install(ctx, manager); err != nil {
		t.Fatal(err)
	}
	installed, err := Existing(o)
	if err != nil {
		t.Fatal(err)
	}
	if installed.serviceBinary() != upgrade.serviceBinary() || installed.serviceBinary() == installed.Binary {
		t.Fatal("the upgraded task does not have its own background executable")
	}
}

func TestInstallRepeatUpgradeUninstallPreservesSSH(t *testing.T) {
	for _, platform := range []string{"darwin", "linux", "windows"} {
		t.Run(platform, func(t *testing.T) {
			o := fixture(t, platform)
			sshPath := filepath.Join(o.Home, ".ssh", "config")
			original := []byte("Host private\r\n    User test") // Deliberately no final newline.
			mustWrite(t, sshPath, original)
			p := mustPlan(t, o)
			if _, err := os.Stat(p.Root); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("planning wrote files")
			}
			manager := &fakeManager{}
			if err := p.Install(context.Background(), manager); err != nil {
				t.Fatal(err)
			}
			backup := p.Manifest.SSHBackup
			if !bytes.Equal(mustRead(t, backup), original) {
				t.Fatal("backup differs")
			}
			if !strings.Contains(string(mustRead(t, sshPath)), "IdentityAgent") {
				t.Fatal("SSH not configured")
			}
			// A second installation neither duplicates the block nor replaces backup.
			p = mustPlan(t, o)
			if err := p.Install(context.Background(), manager); err != nil {
				t.Fatal(err)
			}
			if bytes.Count(mustRead(t, sshPath), []byte(beginSSH)) != 1 {
				t.Fatal("duplicated SSH block")
			}
			if p.Manifest.SSHBackup != backup {
				t.Fatal("backup was replaced")
			}
			// Upgrade to a new content-addressed version; old binary stays tracked.
			mustWrite(t, o.Source, fixtureBinary(platform, "test-binary-v2"))
			p = mustPlan(t, o)
			if err := p.Install(context.Background(), manager); err != nil {
				t.Fatal(err)
			}
			wantFiles := 4
			if platform == "windows" {
				wantFiles += 2 // One background executable per version.
			}
			if len(p.Manifest.Files) != wantFiles {
				t.Fatalf("previous version not tracked: %d", len(p.Manifest.Files))
			}
			// Preserve user changes made after installation, outside the managed block.
			addition := []byte("\nHost later\n    User another\n")
			mustWrite(t, sshPath, append(mustRead(t, sshPath), addition...))
			removal, err := Existing(o)
			if err != nil {
				t.Fatal(err)
			}
			if err := removal.Uninstall(context.Background(), manager); err != nil {
				t.Fatal(err)
			}
			want := append(append([]byte{}, original...), addition...)
			if !bytes.Equal(mustRead(t, sshPath), want) {
				t.Fatalf("SSH edits lost: %q", mustRead(t, sshPath))
			}
			if !bytes.Equal(mustRead(t, backup), original) {
				t.Fatal("uninstall touched backup")
			}
			if _, err := os.Stat(p.ManifestPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("manifest remains")
			}
			for path := range p.Manifest.Files {
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("managed file remains: %s", path)
				}
			}
		})
	}
}

func TestInstallRollbackAndPreviousServiceRecovery(t *testing.T) {
	o := fixture(t, "linux")
	p := mustPlan(t, o)
	manager := &fakeManager{failNext: 1}
	if err := p.Install(context.Background(), manager); err == nil {
		t.Fatal("activation failure hidden")
	}
	for path := range p.Files {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("failed install left %s", path)
		}
	}
	if _, err := os.Stat(p.SSHPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("SSH was not rolled back")
	}
	p = mustPlan(t, o)
	if err := p.Install(context.Background(), manager); err != nil {
		t.Fatal(err)
	}
	oldConfig := mustRead(t, p.ConfigPath)
	oldSSH := mustRead(t, p.SSHPath)
	oldManifest := mustRead(t, p.ManifestPath)
	o.Config.Timeout = "10s"
	mustWrite(t, o.Source, []byte("new-binary"))
	upgrade := mustPlan(t, o)
	manager.failNext = 1
	if err := upgrade.Install(context.Background(), manager); err == nil {
		t.Fatal("upgrade failure hidden")
	}
	if manager.state != "registered" {
		t.Fatal("old service not recovered")
	}
	if manager.lastBinary != p.Binary {
		t.Fatalf("rollback restarted wrong binary: %s", manager.lastBinary)
	}
	if !bytes.Equal(mustRead(t, p.ConfigPath), oldConfig) || !bytes.Equal(mustRead(t, p.SSHPath), oldSSH) || !bytes.Equal(mustRead(t, p.ManifestPath), oldManifest) {
		t.Fatal("old installation not restored")
	}
	if _, err := os.Stat(upgrade.Binary); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed upgrade binary remains")
	}
}

func TestRefusesUnownedAndEditedFiles(t *testing.T) {
	t.Run("unowned", func(t *testing.T) {
		o := fixture(t, "darwin")
		p := mustPlan(t, o)
		mustWrite(t, p.ServicePath, []byte("my launch agent"))
		if _, err := NewPlan(o); err == nil {
			t.Fatal("overwrote unowned file")
		}
	})
	t.Run("modified-config", func(t *testing.T) {
		o := fixture(t, "linux")
		p := mustPlan(t, o)
		manager := &fakeManager{}
		if err := p.Install(context.Background(), manager); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, p.ConfigPath, []byte("user edits"))
		if _, err := Existing(o); err == nil {
			t.Fatal("accepted modified config")
		}
	})
	t.Run("modified-ssh-block", func(t *testing.T) {
		o := fixture(t, "linux")
		p := mustPlan(t, o)
		if err := p.Install(context.Background(), &fakeManager{}); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, p.SSHPath, bytes.ReplaceAll(mustRead(t, p.SSHPath), []byte("IdentityAgent"), []byte("# Changed")))
		if _, err := Existing(o); err == nil {
			t.Fatal("accepted modified managed block")
		}
	})
	t.Run("foreign-service", func(t *testing.T) {
		o := fixture(t, "windows")
		p := mustPlan(t, o)
		if err := p.Install(context.Background(), &fakeManager{state: "registered"}); err == nil {
			t.Fatal("replaced unowned service")
		}
	})
}

func TestNoSSHAndCreatedSSHCleanup(t *testing.T) {
	for _, configure := range []bool{true, false} {
		o := fixture(t, "darwin")
		o.ConfigureSSH = configure
		p := mustPlan(t, o)
		manager := &fakeManager{}
		if err := p.Install(context.Background(), manager); err != nil {
			t.Fatal(err)
		}
		_, err := os.Stat(p.SSHPath)
		if configure && err != nil {
			t.Fatal(err)
		}
		if !configure && !errors.Is(err, os.ErrNotExist) {
			t.Fatal("no-ssh created SSH config")
		}
		removal, err := Existing(o)
		if err != nil {
			t.Fatal(err)
		}
		if err := removal.Uninstall(context.Background(), manager); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(p.SSHPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("created SSH file not removed")
		}
	}
}

func TestResumeInterruptedUninstall(t *testing.T) {
	o := fixture(t, "linux")
	p := mustPlan(t, o)
	manager := &fakeManager{}
	if err := p.Install(context.Background(), manager); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash between deleting a managed file and saving progress.
	m := p.Manifest
	m.Uninstalling = true
	data, _ := json.Marshal(m)
	mustWrite(t, p.ManifestPath, data)
	if err := os.Remove(p.ConfigPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p.SSHPath); err != nil {
		t.Fatal(err)
	}
	removal, err := Existing(o)
	if err != nil {
		t.Fatal(err)
	}
	if err := removal.Uninstall(context.Background(), manager); err != nil {
		t.Fatal(err)
	}
}

func TestServiceEscaping(t *testing.T) {
	o := fixture(t, "darwin")
	o.Home = filepath.Join(o.Home, "space & apostrophe's")
	p := mustPlan(t, o)
	decoder := xml.NewDecoder(bytes.NewReader(p.Files[p.ServicePath]))
	for {
		_, err := decoder.Token()
		if err != nil {
			if err.Error() != "EOF" {
				t.Fatal(err)
			}
			break
		}
	}
	if got := unitArg(`/home/a b/100%/$literal/"quoted"`); !strings.Contains(got, "100%%") || !strings.Contains(got, "$$literal") {
		t.Fatal(got)
	}
	if ps("O'Brien") != "'O''Brien'" {
		t.Fatal("PowerShell quote escaping")
	}
	if encodedScript("ASCII") == "" {
		t.Fatal("empty encoded script")
	}
}

func TestConcurrentInstallationLock(t *testing.T) {
	o := fixture(t, "linux")
	p := mustPlan(t, o)
	release, err := lockRoot(p.Root)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := p.Install(ctx, &fakeManager{}); err == nil {
		t.Fatal("concurrent install acquired lock")
	}
}

func TestStalePreviewDoesNotOverwriteNewFile(t *testing.T) {
	o := fixture(t, "darwin")
	p := mustPlan(t, o)
	mustWrite(t, p.ServicePath, []byte("created after preview"))
	if err := p.Install(context.Background(), &fakeManager{}); err == nil {
		t.Fatal("stale plan overwrote new file")
	}
	if string(mustRead(t, p.ServicePath)) != "created after preview" {
		t.Fatal("file changed")
	}
}
