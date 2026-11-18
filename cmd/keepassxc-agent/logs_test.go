package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"keepassxc-agent-middleware/internal/settings"
)

func TestLogTail(t *testing.T) {
	for _, c := range []struct {
		name, data string
		lines      int
		want       string
	}{
		{"empty", "", 100, ""}, {"terminated", "a\nb\nc\n", 2, "b\nc\n"},
		{"partial", "a\nb\nc", 1, "c"}, {"blank", "a\n\n", 1, "\n"},
		{"all", "a\nb\n", 100, "a\nb\n"}, {"none", "a\nb\n", 0, ""},
		{"long line", strings.Repeat("x", 70000) + "\nlast\n", 1, "last\n"},
		{"utf8", "primero\núltimo 🔑\n", 1, "último 🔑\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "agent.log")
			if err := os.WriteFile(path, []byte(c.data), 0600); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := streamLogs(context.Background(), path, c.lines, false, &out); err != nil {
				t.Fatal(err)
			}
			if out.String() != c.want {
				t.Fatalf("got %q want %q", out.String(), c.want)
			}
		})
	}
}
func TestLogsSelectConfiguredFileAndExplicitOverride(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "agent.log")
	configPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(path, []byte("old\ncurrent\n"), 0600); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(settings.Config{LogFile: path})
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := execute([]string{"logs", "--config", configPath, "-n", "1"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if out.String() != "current\n" {
		t.Fatalf("configured log: %q", out.String())
	}
	// An explicit file remains readable even when the configuration is broken.
	if err := os.WriteFile(configPath, []byte("invalid JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := execute([]string{"logs", "--config", configPath, "--log-file", path, "--lines", "0"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatal("--lines 0 printed history")
	}
	for _, args := range [][]string{{"logs", "--lines", "-1"}, {"logs", "extra"}, {"logs", "--log-file", ""}, {"logs", "--config", configPath}, {"logs", "--lines", "bad"}} {
		if err := execute(args, io.Discard, io.Discard); err == nil {
			t.Fatalf("accepted invalid logs arguments %q", args)
		}
	}
	if err := execute([]string{"logs", "--log-file", filepath.Join(root, "missing")}, io.Discard, io.Discard); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing log error: %v", err)
	}
	if err := execute([]string{"logs", "--help"}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
}
func TestLogFollowReplacementTruncationAndMissingFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "agent.log")
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("initial\n")
	var out bytes.Buffer
	info, offset, err := initialLogs(path, 0, &out)
	if err != nil {
		t.Fatal(err)
	}
	write("initial\nappended\n")
	info, offset, err = nextLogs(path, info, offset, &out)
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != "appended\n" {
		t.Fatalf("append: %q", out.String())
	}
	out.Reset()
	write("z\n")
	info, offset, err = nextLogs(path, info, offset, &out)
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != "z\n" {
		t.Fatalf("truncate: %q", out.String())
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	info, offset, err = nextLogs(path, info, offset, &out)
	if err != nil {
		t.Fatalf("temporary missing file: %v", err)
	}
	write("replacement\n")
	out.Reset()
	_, _, err = nextLogs(path, info, offset, &out)
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != "replacement\n" {
		t.Fatalf("replacement: %q", out.String())
	}
}

type logEvents chan string

func (c logEvents) Write(p []byte) (int, error) { c <- string(p); return len(p), nil }
func TestLogsFollowAndCancel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	if err := os.WriteFile(path, []byte("initial\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(logEvents, 8)
	done := make(chan error, 1)
	go func() { done <- streamLogs(ctx, path, 1, true, events) }()
	await := func(want string) {
		t.Helper()
		select {
		case got := <-events:
			if got != want {
				t.Fatalf("got %q want %q", got, want)
			}
		case err := <-done:
			t.Fatalf("follow exited early: %v", err)
		case <-time.After(3 * time.Second):
			t.Fatal("follow timed out")
		}
	}
	await("initial\n")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString("live\n")
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	await("live\n")
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("follow did not cancel")
	}
}
