// Package install manages a per-user installation, never a system/root service.
package install

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"keepassxc-agent-middleware/internal/settings"
)

const Label = "org.keepassxc.agent-middleware"
const beginSSH = "# BEGIN keepassxc-agent managed configuration"
const endSSH = "# END keepassxc-agent managed configuration"

type Options struct {
	Home, OS, Source, UserID string
	Config                   settings.Config
	ConfigureSSH             bool
	ConfigurePath            bool
	Shell, LocalAppData      string
}

type Manifest struct {
	Uninstalling  bool                `json:"uninstalling,omitempty"`
	Version       int                 `json:"version"`
	OS            string              `json:"os"`
	Files         map[string]string   `json:"files"` // Hashes of files owned by this installer.
	SSHBlock      string              `json:"ssh_block,omitempty"`
	SSHBackup     string              `json:"ssh_backup,omitempty"`
	SSHCreated    bool                `json:"ssh_created,omitempty"`
	Binary        string              `json:"binary"`
	TrayEnabled   bool                `json:"tray_enabled,omitempty"`
	NativeHelper  string              `json:"native_helper,omitempty"`
	Launcher      string              `json:"launcher,omitempty"`
	PathEdits     map[string]PathEdit `json:"path_edits,omitempty"`
	UserPathAdded bool                `json:"user_path_added,omitempty"`
}

type Plan struct {
	Options
	job                                                                             string // Optional isolated service identifier used by integration tests.
	Root, Binary, ConfigPath, ServicePath, ManifestPath, SSHPath, LogPath, TaskName string
	TrayServicePath, TrayAutostartPath                                              string
	restoreTray                                                                     bool // Only used to restore a legacy installation after rollback.
	Launcher                                                                        string
	Profiles                                                                        map[string]profileChange
	PathBefore                                                                      *pathValue
	PathAfter                                                                       pathValue
	Files                                                                           map[string][]byte
	Previous                                                                        *Manifest
	Manifest                                                                        Manifest
	SSHBefore, SSHAfter                                                             []byte
	SSHExists                                                                       bool
}

func (p *Plan) serviceID() string {
	if p.job != "" {
		return p.job
	}
	if p.OS == "darwin" {
		return Label
	}
	return "keepassxc-agent.service"
}

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func locations(o Options) (*Plan, error) {
	if o.OS != "darwin" && o.OS != "linux" && o.OS != "windows" {
		return nil, fmt.Errorf("unsupported OS: %s", o.OS)
	}
	if !filepath.IsAbs(o.Home) || o.UserID == "" {
		return nil, errors.New("home and user ID are required")
	}
	p := &Plan{Options: o, Root: filepath.Join(o.Home, ".keepassxc-agent"), Files: map[string][]byte{}}
	p.Launcher = p.cliLocation()
	if !filepath.IsAbs(p.Launcher) {
		return nil, errors.New("CLI location must be absolute")
	}
	p.ConfigPath = filepath.Join(p.Root, "config.json")
	p.ManifestPath = filepath.Join(p.Root, "install.json")
	p.SSHPath = filepath.Join(o.Home, ".ssh", "config")
	p.LogPath = filepath.Join(p.Root, "agent.log")
	suffix := ""
	if o.OS == "windows" {
		suffix = ".exe"
	}
	p.Binary = filepath.Join(p.Root, "bin", "keepassxc-agent"+suffix)
	p.TaskName = "KeePassXCAgentMiddleware-" + o.UserID
	switch o.OS {
	case "darwin":
		p.ServicePath = filepath.Join(o.Home, "Library", "LaunchAgents", Label+".plist")
		p.TrayServicePath = filepath.Join(o.Home, "Library", "LaunchAgents", Label+".tray.plist")
	case "linux":
		p.ServicePath = filepath.Join(o.Home, ".config", "systemd", "user", "keepassxc-agent.service")
		p.TrayServicePath = filepath.Join(o.Home, ".config", "systemd", "user", "keepassxc-agent-tray.service")
		p.TrayAutostartPath = filepath.Join(o.Home, ".config", "autostart", "keepassxc-agent-tray.desktop")
	case "windows":
		p.ServicePath = filepath.Join(p.Root, "task.ps1")
	}
	return p, nil
}

func Existing(o Options) (*Plan, error) {
	p, err := locations(o)
	if err != nil {
		return nil, err
	}
	p.Previous, err = ReadManifest(p.ManifestPath)
	if err != nil {
		return nil, err
	}
	if err = p.validateManifest(p.Previous); err != nil {
		return nil, err
	}
	if err = p.checkRemaining(); err != nil {
		return nil, err
	}
	p.Binary = p.Previous.Binary
	p.ConfigurePath = false
	if err := p.preparePath(); err != nil {
		return nil, err
	}
	p.restoreTray = p.Previous.TrayEnabled
	if p.managesSSH() {
		p.SSHBefore, p.SSHExists, err = readRegular(p.SSHPath)
	}
	if err != nil {
		return nil, err
	}
	p.SSHAfter = append([]byte(nil), p.SSHBefore...)
	if p.Previous.SSHBlock != "" && !(p.Previous.Uninstalling && !bytes.Contains(p.SSHAfter, []byte(beginSSH))) {
		p.SSHAfter, err = removeSSH(p.SSHAfter, p.Previous.SSHBlock)
		if err != nil {
			return nil, err
		}
	}
	return p, nil
}

func NewPlan(o Options) (*Plan, error) {
	p, err := locations(o)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(o.Source)
	if err != nil {
		return nil, err
	}
	suffix := ""
	if o.OS == "windows" {
		suffix = ".exe"
	}
	p.Binary = filepath.Join(p.Root, "bin", "keepassxc-agent-"+digest(data)[:16]+suffix)
	p.Previous, err = ReadManifest(p.ManifestPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if p.Previous != nil {
		if p.Previous.Uninstalling {
			return nil, errors.New("an interrupted uninstall must be completed before installing again")
		}
		if err := p.validateManifest(p.Previous); err != nil {
			return nil, err
		}
		if err := checkFiles(p.Previous.Files); err != nil {
			return nil, err
		}
	}
	if p.Config.Listen == "" {
		return nil, errors.New("proxy endpoint is required")
	}
	p.Config.LogFile = p.LogPath
	config, err := json.MarshalIndent(p.Config, "", "  ")
	if err != nil {
		return nil, err
	}
	p.Files[p.Binary] = data
	p.Files[p.ConfigPath] = append(config, '\n')
	p.Files[p.ServicePath] = p.serviceFile()
	if err := p.prepareLauncher(); err != nil {
		return nil, err
	}
	// No untracked file at an installer-owned destination may be overwritten.
	for path := range p.Files {
		if _, err := os.Lstat(path); err == nil {
			if p.Previous == nil || p.Previous.Files[path] == "" {
				return nil, fmt.Errorf("refusing to replace unowned file: %s", path)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	if p.managesSSH() {
		p.SSHBefore, p.SSHExists, err = readRegular(p.SSHPath)
	}
	if err != nil {
		return nil, err
	}
	p.SSHAfter = append([]byte(nil), p.SSHBefore...)
	p.Manifest = Manifest{Version: 1, OS: o.OS, Files: map[string]string{}, Binary: p.Binary}
	p.Manifest.TrayEnabled = false
	if p.ConfigurePath {
		p.Manifest.Launcher = p.Launcher
	}
	if err := p.preparePath(); err != nil {
		return nil, err
	}
	if p.Previous != nil {
		p.Manifest.SSHBackup = p.Previous.SSHBackup
		p.Manifest.SSHCreated = p.Previous.SSHCreated
		if p.Previous.SSHBlock != "" {
			p.SSHAfter, err = removeSSH(p.SSHAfter, p.Previous.SSHBlock)
			if err != nil {
				return nil, err
			}
		}
	}
	if bytes.Contains(p.SSHAfter, []byte(beginSSH)) || bytes.Contains(p.SSHAfter, []byte(endSSH)) {
		return nil, errors.New("SSH configuration contains an unrecognized managed block; restore or remove that block manually")
	}
	if o.ConfigureSSH {
		path := p.Config.Listen
		if o.OS == "windows" {
			path = strings.ReplaceAll(path, `\`, "/")
		}
		// OpenSSH expands % and ${...} inside IdentityAgent paths. Reject paths
		// that cannot be represented literally rather than producing a wrong socket.
		if strings.ContainsAny(path, "\r\n\"%") || strings.Contains(path, "${") {
			return nil, errors.New("proxy path contains unsupported SSH configuration characters")
		}
		separator := ""
		if len(p.SSHAfter) > 0 && p.SSHAfter[len(p.SSHAfter)-1] != '\n' {
			separator = "\n"
		}
		p.Manifest.SSHBlock = separator + beginSSH + "\nHost *\n    IdentityAgent \"" + path + "\"\n" + endSSH + "\n"
		p.SSHAfter = append(p.SSHAfter, []byte(p.Manifest.SSHBlock)...)
		if p.Previous == nil {
			p.Manifest.SSHCreated = !p.SSHExists
		}
	}
	if !bytes.Equal(p.SSHBefore, p.SSHAfter) && p.SSHExists && p.Manifest.SSHBackup == "" {
		p.Manifest.SSHBackup = p.SSHPath + ".keepassxc-agent-backup-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	}
	if p.Previous != nil {
		for path, hash := range p.Previous.Files {
			if filepath.Dir(path) == filepath.Join(p.Root, "bin") {
				p.Manifest.Files[path] = hash
			}
		}
	}
	for path, data := range p.Files {
		p.Manifest.Files[path] = digest(data)
	}
	return p, nil
}

func ReadManifest(path string) (*Manifest, error) {
	data, exists, err := readRegular(path)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, os.ErrNotExist
	}
	var m Manifest
	if err = json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("invalid install manifest: %w", err)
	}
	return &m, nil
}

func (p *Plan) validateManifest(m *Manifest) error {
	if m.Version != 1 || m.OS != p.OS {
		return errors.New("unsupported installation manifest")
	}
	if m.Launcher != "" && (m.Launcher != p.Launcher || (!m.Uninstalling && m.Files[m.Launcher] == "")) {
		return errors.New("invalid managed CLI launcher")
	}
	if m.UserPathAdded && m.OS != "windows" {
		return errors.New("invalid registry PATH ownership")
	}
	for path, edit := range m.PathEdits {
		if m.OS == "windows" || !p.allowedProfile(path) || strings.Count(edit.Block, beginPath) != 1 || strings.Count(edit.Block, endPath) != 1 {
			return errors.New("invalid managed PATH profile")
		}
	}
	binDir := filepath.Join(p.Root, "bin")
	if filepath.Dir(m.Binary) != binDir || !strings.HasPrefix(filepath.Base(m.Binary), "keepassxc-agent-") {
		return errors.New("invalid installed binary path")
	}
	for path := range m.Files {
		if path == p.Launcher || path == p.ConfigPath || path == p.ServicePath || (p.TrayServicePath != "" && path == p.TrayServicePath) || (p.TrayAutostartPath != "" && path == p.TrayAutostartPath) {
			continue
		}
		hash := m.Files[path]
		suffix := ""
		if p.OS == "windows" {
			suffix = ".exe"
		}
		valid := len(hash) == 64 && filepath.Dir(path) == binDir
		if valid {
			valid = filepath.Base(path) == "keepassxc-agent-"+hash[:16]+suffix || (m.OS == "darwin" && filepath.Base(path) == "keepassxc-agent-tray-"+hash[:16])
		}
		if !valid {
			return fmt.Errorf("unrecognized managed path: %s", path)
		}
	}
	if !m.Uninstalling && (m.Files[m.Binary] == "" || m.Files[p.ConfigPath] == "" || m.Files[p.ServicePath] == "") {
		return errors.New("installation manifest is incomplete")
	}
	if m.NativeHelper != "" && (m.OS != "darwin" || filepath.Dir(m.NativeHelper) != binDir || !strings.HasPrefix(filepath.Base(m.NativeHelper), "keepassxc-agent-tray-") || (!m.Uninstalling && m.Files[m.NativeHelper] == "")) {
		return errors.New("invalid managed native tray executable")
	}
	if !m.Uninstalling && m.TrayEnabled && m.OS != "windows" {
		if m.Files[p.TrayServicePath] == "" || (m.OS == "linux" && m.Files[p.TrayAutostartPath] == "") || (m.OS == "darwin" && m.NativeHelper == "") {
			return errors.New("tray installation is incomplete")
		}
	}
	return nil
}

func checkFiles(files map[string]string) error {
	for path, hash := range files {
		data, exists, err := readRegular(path)
		if err != nil {
			return err
		}
		if !exists || digest(data) != hash {
			return fmt.Errorf("managed file was changed or removed; preserve your edits before reinstalling/uninstalling: %s", path)
		}
	}
	return nil
}

func readRegular(path string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("refusing non-regular file (including symlinks): %s", path)
	}
	data, err := os.ReadFile(path)
	return data, true, err
}

func removeSSH(data []byte, block string) ([]byte, error) {
	if bytes.Count(data, []byte(block)) != 1 {
		return nil, errors.New("managed SSH block was changed or removed; restore it before uninstalling or reinstalling")
	}
	return bytes.Replace(data, []byte(block), nil, 1), nil
}

func (p *Plan) Summary() string {
	cli := p.Manifest.Launcher
	if cli == "" {
		cli = "PATH integration disabled"
	}
	ssh := "SSH config unchanged"
	if !bytes.Equal(p.SSHBefore, p.SSHAfter) {
		ssh = "SSH config: " + p.SSHPath
	}
	summary := fmt.Sprintf("Binary: %s\nConfiguration: %s\nService: %s\nCLI: %s\n%s\nSSH backup: %s\nLogs: %s\nProxy: %s\n", p.Binary, p.ConfigPath, p.ServicePath, cli, ssh, p.Manifest.SSHBackup, p.LogPath, p.Config.Listen)
	paths := []string{}
	for path, c := range p.Profiles {
		if !bytes.Equal(c.Before, c.After) {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		summary += "PATH profile: " + path + "\n"
	}
	if p.PathBefore != nil && *p.PathBefore != p.PathAfter {
		summary += "User PATH: update entry for " + filepath.Dir(p.Launcher) + "\n"
	}
	return summary
}

func Location(o Options) (*Plan, error) { return locations(o) }

// Missing files are expected only after an interrupted, recorded uninstall.
func (p *Plan) checkRemaining() error {
	if !p.Previous.Uninstalling {
		return checkFiles(p.Previous.Files)
	}
	for path, hash := range p.Previous.Files {
		data, exists, err := readRegular(path)
		if err != nil {
			return err
		}
		if !exists {
			delete(p.Previous.Files, path)
			continue
		}
		if digest(data) != hash {
			return fmt.Errorf("managed file was changed: %s", path)
		}
	}
	return nil
}

func (p *Plan) managesSSH() bool {
	return p.ConfigureSSH || (p.Previous != nil && p.Previous.SSHBlock != "")
}
