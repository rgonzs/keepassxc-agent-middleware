package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"keepassxc-agent-middleware/internal/settings"
)

func runLogs(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("logs", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", settings.DefaultPath(), "configuration containing log_file")
	logPath := flags.String("log-file", "", "read this log instead of the configured file")
	lines := 100
	follow := false
	flags.IntVar(&lines, "lines", 100, "number of recent lines (0 skips existing content)")
	flags.IntVar(&lines, "n", 100, "alias for --lines")
	flags.BoolVar(&follow, "follow", false, "follow new output until Ctrl+C, including replacement or truncation")
	flags.BoolVar(&follow, "f", false, "alias for --follow")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: keepassxc-agent logs [--lines N] [--follow] [--config PATH | --log-file PATH]")
	}
	if lines < 0 {
		return errors.New("--lines must be nonnegative")
	}
	explicitFile, explicitConfig := false, false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "log-file" {
			explicitFile = true
		}
		if f.Name == "config" {
			explicitConfig = true
		}
	})
	if explicitFile && *logPath == "" {
		return errors.New("--log-file cannot be empty")
	}
	if !explicitFile {
		config, err := settings.Load(*configPath)
		switch {
		case errors.Is(err, os.ErrNotExist) && !explicitConfig:
			*logPath = filepath.Join(settings.Root(), "agent.log")
		case err != nil:
			return err
		case config.LogFile == "":
			return errors.New("configuration has no log_file; use logs --log-file PATH (run normally writes to stderr)")
		default:
			*logPath = config.LogFile
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := streamLogs(ctx, *logPath, lines, follow, stdout); err != nil {
		return fmt.Errorf("read logs %s: %w", *logPath, err)
	}
	return nil
}

// Find a tail without reading the complete history into memory. The last
// newline terminates the last line; it does not count as a new empty line.
func tailStart(file *os.File, size int64, lines int) (int64, error) {
	if lines == 0 {
		return size, nil
	}
	buffer := make([]byte, 32*1024)
	remaining := lines
	for end := size; end > 0; {
		start := end - int64(len(buffer))
		if start < 0 {
			start = 0
		}
		chunk := buffer[:int(end-start)]
		if _, err := file.ReadAt(chunk, start); err != nil {
			return 0, err
		}
		for i := len(chunk) - 1; i >= 0; i-- {
			if chunk[i] == '\n' && start+int64(i) != size-1 {
				remaining--
				if remaining == 0 {
					return start + int64(i) + 1, nil
				}
			}
		}
		end = start
	}
	return 0, nil
}
func openLog(path string) (*os.File, os.FileInfo, error) {
	// Reject devices/directories/FIFOs before opening them for a normal log read.
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, errors.New("log must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	info, err = file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, nil, errors.New("log must be a regular file")
	}
	return file, info, nil
}
func initialLogs(path string, lines int, out io.Writer) (os.FileInfo, int64, error) {
	file, info, err := openLog(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	start, err := tailStart(file, info.Size(), lines)
	if err != nil {
		return nil, 0, err
	}
	n, err := io.Copy(out, io.NewSectionReader(file, start, info.Size()-start))
	return info, start + n, err
}

// Reopen on each poll, so following a Windows log does not keep a handle open
// that would prevent an external log rotation. Follow the filename, not inode.
func nextLogs(path string, previous os.FileInfo, offset int64, out io.Writer) (os.FileInfo, int64, error) {
	file, info, err := openLog(path)
	if errors.Is(err, os.ErrNotExist) {
		return previous, offset, nil
	}
	if err != nil {
		return previous, offset, err
	}
	defer file.Close()
	if !os.SameFile(previous, info) || info.Size() < offset {
		offset = 0
	}
	n, err := io.Copy(out, io.NewSectionReader(file, offset, info.Size()-offset))
	return info, offset + n, err
}
func streamLogs(ctx context.Context, path string, lines int, follow bool, out io.Writer) error {
	info, offset, err := initialLogs(path, lines, out)
	if err != nil {
		return err
	}
	if !follow {
		return nil
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			info, offset, err = nextLogs(path, info, offset, out)
			if err != nil {
				return err
			}
		}
	}
}
