package install

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const beginPath = "# BEGIN keepassxc-agent user PATH"
const endPath = "# END keepassxc-agent user PATH"

type PathEdit struct {
	Block   string `json:"block"`
	Created bool   `json:"created,omitempty"`
}
type profileChange struct {
	Before, After []byte
	Exists        bool
	Mode          os.FileMode
}
type pathValue struct {
	Value  string
	Type   uint32
	Exists bool
}

func (p *Plan) cliLocation() string {
	if p.OS == "windows" {
		base := p.LocalAppData
		if base == "" {
			base = filepath.Join(p.Home, "AppData", "Local")
		}
		return filepath.Join(base, "Programs", "keepassxc-agent", "keepassxc-agent.cmd")
	}
	return filepath.Join(p.Home, ".local", "bin", "keepassxc-agent")
}
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }

func (p *Plan) prepareLauncher() error {
	if !p.ConfigurePath {
		return nil
	}
	if (p.OS == "windows" && strings.Contains(filepath.Dir(p.Launcher), ";")) || (p.OS != "windows" && strings.Contains(filepath.Dir(p.Launcher), ":")) {
		return errors.New("CLI directory contains the PATH separator; use --no-path")
	}
	if strings.ContainsAny(p.Binary, "\r\n\x00") {
		return errors.New("CLI path cannot contain control characters")
	}
	if p.OS == "windows" {
		if strings.Contains(p.Binary, `"`) {
			return errors.New("invalid Windows executable path")
		}
		p.Files[p.Launcher] = []byte("@echo off\r\nsetlocal DisableDelayedExpansion\r\n\"" + strings.ReplaceAll(p.Binary, "%", "%%") + "\" %*\r\nexit /b %errorlevel%\r\n")
	} else {
		p.Files[p.Launcher] = []byte("#!/bin/sh\nexec " + shellQuote(p.Binary) + " \"$@\"\n")
	}
	return nil
}

func (p *Plan) allowedProfile(path string) bool {
	for _, rel := range []string{".profile", ".bashrc", ".bash_profile", ".bash_login", ".zprofile", ".zshrc", ".config/fish/config.fish"} {
		if path == filepath.Join(p.Home, rel) {
			return true
		}
	}
	return false
}
func (p *Plan) profileTargets() ([]string, error) {
	shell := filepath.Base(p.Shell)
	if shell == "." || shell == "" {
		if p.OS == "darwin" {
			shell = "zsh"
		} else {
			shell = "bash"
		}
	}
	rels := []string{".profile"}
	switch shell {
	case "zsh":
		rels = append(rels, ".zprofile", ".zshrc")
	case "bash":
		rels = append(rels, ".bashrc")
		for _, name := range []string{".bash_profile", ".bash_login"} {
			if _, err := os.Lstat(filepath.Join(p.Home, name)); err == nil {
				rels = append(rels, name)
				break
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		}
	case "fish":
		rels = append(rels, ".config/fish/config.fish")
	case "sh", "dash", "ksh":
	default:
		return nil, fmt.Errorf("unsupported shell %q for automatic PATH; use --no-path and add %s manually", shell, filepath.Dir(p.Launcher))
	}
	paths := []string{}
	for _, rel := range rels {
		paths = append(paths, filepath.Join(p.Home, rel))
	}
	return paths, nil
}
func (p *Plan) preparePath() error {
	p.Profiles = map[string]profileChange{}
	p.Manifest.PathEdits = map[string]PathEdit{}
	if p.OS == "windows" {
		return p.prepareWindowsPath()
	}
	targets := map[string]bool{}
	if p.ConfigurePath {
		paths, err := p.profileTargets()
		if err != nil {
			return err
		}
		for _, path := range paths {
			targets[path] = true
		}
	}
	if p.Previous != nil {
		for path := range p.Previous.PathEdits {
			if !p.allowedProfile(path) {
				return errors.New("invalid PATH profile")
			}
			if _, exists := targets[path]; !exists {
				targets[path] = false
			}
		}
	}
	for path, enabled := range targets {
		before, exists, err := readRegular(path)
		if err != nil {
			return err
		}
		mode := os.FileMode(0600)
		if exists {
			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			mode = info.Mode().Perm()
		}
		after := append([]byte(nil), before...)
		created := !exists
		if p.Previous != nil {
			if old, ok := p.Previous.PathEdits[path]; ok {
				created = old.Created
				// Retrying an uninstall after the exact block has already been removed.
				if !(p.Previous.Uninstalling && !bytes.Contains(after, []byte(beginPath))) {
					if bytes.Count(after, []byte(old.Block)) != 1 {
						return fmt.Errorf("managed PATH block changed in %s; preserve edits before retrying", path)
					}
					after = bytes.Replace(after, []byte(old.Block), nil, 1)
				}
			}
		}
		if bytes.Contains(after, []byte(beginPath)) || bytes.Contains(after, []byte(endPath)) {
			return fmt.Errorf("unrecognized PATH block in %s", path)
		}
		if enabled {
			dir := shellQuote(filepath.Dir(p.Launcher))
			body := "case \":${PATH-}:\" in\n  *:" + dir + ":*) ;;\n  *) export PATH=" + dir + "\"${PATH:+:$PATH}\" ;;\nesac\n"
			if strings.HasSuffix(path, "config.fish") {
				body = "if not contains -- " + dir + " $PATH\n    set -gx PATH " + dir + " $PATH\nend\n"
			}
			sep := ""
			if len(after) > 0 && after[len(after)-1] != '\n' {
				sep = "\n"
			}
			block := sep + beginPath + "\n" + body + endPath + "\n"
			after = append(after, block...)
			p.Manifest.PathEdits[path] = PathEdit{Block: block, Created: created}
		}
		p.Profiles[path] = profileChange{before, after, exists, mode}
	}
	return nil
}
func (p *Plan) checkProfiles() error {
	for path, c := range p.Profiles {
		now, exists, err := readRegular(path)
		if err != nil {
			return err
		}
		if exists != c.Exists || !bytes.Equal(now, c.Before) {
			return fmt.Errorf("shell profile changed during installation: %s", path)
		}
	}
	return nil
}
func (p *Plan) writeProfiles(tx *transaction) error {
	for path, c := range p.Profiles {
		if bytes.Equal(c.Before, c.After) {
			continue
		}
		created := !c.Exists
		if p.Previous != nil {
			if old, ok := p.Previous.PathEdits[path]; ok {
				created = old.Created
			}
		}
		if len(c.After) == 0 && created {
			if err := tx.remove(path); err != nil {
				return err
			}
		} else if err := tx.write(path, c.After, c.Mode); err != nil {
			return err
		}
	}
	return nil
}

func windowsPathKey(s string) string {
	// User PATH frequently uses %LOCALAPPDATA% or %USERPROFILE%. Compare their
	// expanded values while preserving the original registry text and type.
	for offset := 0; offset < len(s); {
		first := strings.IndexByte(s[offset:], '%')
		if first < 0 {
			break
		}
		first += offset
		last := strings.IndexByte(s[first+1:], '%')
		if last < 0 {
			break
		}
		last += first + 1
		name := s[first+1 : last]
		replacement := ""
		found := false
		for _, env := range os.Environ() {
			key, value, ok := strings.Cut(env, "=")
			if ok && strings.EqualFold(key, name) {
				replacement = value
				found = true
				break
			}
		}
		if found {
			s = s[:first] + replacement + s[last+1:]
			offset = first + len(replacement)
		} else {
			offset = last + 1
		}
	}
	return strings.ToLower(strings.TrimRight(strings.ReplaceAll(strings.Trim(strings.TrimSpace(s), `"`), "/", `\`), `\`))
}

// Track only the entry added by this installer. Never restore a stale complete
// PATH during uninstall: preserve unrelated edits made since installation.
func editWindowsPath(value, dir string, add, owned bool) (string, bool) {
	parts := strings.Split(value, ";")
	found := false
	for _, part := range parts {
		if windowsPathKey(part) == windowsPathKey(dir) {
			found = true
		}
	}
	if add {
		if found {
			return value, owned
		}
		if value != "" && !strings.HasSuffix(value, ";") {
			value += ";"
		}
		return value + dir, true
	}
	if !owned || !found {
		return value, false
	}
	kept := []string{}
	removed := false
	for _, part := range parts {
		if !removed && windowsPathKey(part) == windowsPathKey(dir) {
			removed = true
			continue
		}
		kept = append(kept, part)
	}
	return strings.Join(kept, ";"), false
}
func (p *Plan) prepareWindowsPath() error {
	owned := p.Previous != nil && p.Previous.UserPathAdded
	if !p.ConfigurePath && !owned {
		return nil
	}
	before, err := readUserPath()
	if err != nil {
		return err
	}
	after, added := editWindowsPath(before.Value, filepath.Dir(p.Launcher), p.ConfigurePath, owned)
	p.PathBefore = &before
	p.PathAfter = before
	p.PathAfter.Value = after
	if after != "" {
		p.PathAfter.Exists = true
		if !before.Exists {
			p.PathAfter.Type = 2
		}
	}
	p.Manifest.UserPathAdded = added
	return nil
}
func (p *Plan) applyUserPath() (func() error, error) {
	return p.applyUserPathUsing(readUserPath, writeUserPath)
}
func (p *Plan) applyUserPathUsing(read func() (pathValue, error), write func(pathValue) error) (func() error, error) {
	if p.PathBefore == nil || *p.PathBefore == p.PathAfter {
		return func() error { return nil }, nil
	}
	now, err := read()
	if err != nil {
		return nil, err
	}
	if now != *p.PathBefore {
		return nil, errors.New("user PATH changed during installation; retry")
	}
	if err := write(p.PathAfter); err != nil {
		return nil, err
	}
	return func() error {
		now, err := read()
		if err != nil {
			return err
		}
		if now != p.PathAfter {
			return errors.New("cannot roll back PATH after an external edit")
		}
		return write(*p.PathBefore)
	}, nil
}
