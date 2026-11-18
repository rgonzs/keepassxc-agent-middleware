//go:build !windows

package install

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnixCLIForwardsArgumentsAndExitCode(t *testing.T) {
	o := fixture(t, "linux")
	o.ConfigurePath = true
	o.Home = filepath.Join(o.Home, "space and apostrophe's")
	// This script stands in for the installed executable, never for a user's agent.
	mustWrite(t, o.Source, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\nexit 23\n"))
	p := mustPlan(t, o)
	if err := p.Install(context.Background(), &fakeManager{}); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(p.Launcher, "one two", "$literal;value", "it's literal").CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 23 {
		t.Fatalf("exit not forwarded: %v", err)
	}
	if string(output) != "one two\n$literal;value\nit's literal\n" {
		t.Fatalf("arguments changed: %q", output)
	}
	// Evaluate the real profile twice, using a shell that cannot touch dotfiles.
	cmd := exec.Command("/bin/sh", "-c", `. "$1"; . "$1"; printf '%s' "$PATH"`, "sh", filepath.Join(o.Home, ".profile"))
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(output), filepath.Dir(p.Launcher)) != 1 {
		t.Fatalf("duplicated PATH: %s", output)
	}
}
func TestUserPathRefusesProfileSymlink(t *testing.T) {
	o := fixture(t, "linux")
	o.ConfigurePath = true
	target := filepath.Join(t.TempDir(), "dotfile")
	mustWrite(t, target, []byte("untouched"))
	if err := os.Symlink(target, filepath.Join(o.Home, ".profile")); err != nil {
		t.Fatal(err)
	}
	if _, err := NewPlan(o); err == nil {
		t.Fatal("profile symlink accepted")
	}
	if string(mustRead(t, target)) != "untouched" {
		t.Fatal("symlink target changed")
	}
}
