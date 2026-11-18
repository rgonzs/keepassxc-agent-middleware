// Package instances records running proxies for read-only discovery. Records
// never authorize service control or process termination.
package instances

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"keepassxc-agent-middleware/internal/transport"
)

type Record struct {
	Version  int       `json:"version"`
	PID      int       `json:"pid"`
	Identity string    `json:"identity"`
	Endpoint string    `json:"listen"`
	Binary   string    `json:"binary"`
	Config   string    `json:"config,omitempty"`
	LogFile  string    `json:"log_file,omitempty"`
	Started  time.Time `json:"started,omitempty"`
}

type Candidate struct{ Endpoint, Binary, Config, LogFile string }

func Register(root string, c Candidate) (func(), error) {
	for _, dir := range []string{root, filepath.Join(root, "instances")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
		if err := privateDirectory(dir); err != nil {
			return nil, err
		}
	}
	identity, err := processIdentity(os.Getpid())
	if err != nil {
		return nil, err
	}
	binary, err := os.Executable()
	if err != nil {
		return nil, err
	}
	config := c.Config
	if config != "" {
		config, err = filepath.Abs(config)
		if err != nil {
			return nil, err
		}
	}
	logFile := c.LogFile
	if logFile != "" {
		logFile, err = filepath.Abs(logFile)
		if err != nil {
			return nil, err
		}
	}
	endpoint := c.Endpoint
	if runtime.GOOS != "windows" {
		endpoint, err = filepath.Abs(endpoint)
		if err != nil {
			return nil, err
		}
	}
	if !localEndpoint(endpoint) {
		return nil, errors.New("instance endpoint must be local and absolute")
	}
	record := Record{1, os.Getpid(), identity, endpoint, binary, config, logFile, time.Now().UTC()}
	file, err := os.CreateTemp(filepath.Join(root, "instances"), ".pending-*")
	if err != nil {
		return nil, err
	}
	pending := file.Name()
	defer os.Remove(pending)
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return nil, err
	}
	err = json.NewEncoder(file).Encode(record)
	closeErr := file.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	final := filepath.Join(filepath.Dir(pending), fmt.Sprintf("%d-%s.json", record.PID, strings.TrimPrefix(filepath.Base(pending), ".pending-")))
	if err := os.Rename(pending, final); err != nil {
		return nil, err
	}
	return func() { _ = os.Remove(final) }, nil
}

func readRecord(path string) (Record, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Record{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > 64*1024 {
		return Record{}, errors.New("invalid instance record")
	}
	file, err := os.Open(path)
	if err != nil {
		return Record{}, err
	}
	defer file.Close()
	var record Record
	err = json.NewDecoder(io.LimitReader(file, 64*1024)).Decode(&record)
	if err != nil {
		return Record{}, err
	}
	if record.Version != 1 || record.PID <= 0 || record.Identity == "" || !localEndpoint(record.Endpoint) {
		return Record{}, errors.New("invalid instance metadata")
	}
	return record, nil
}

func endpointPID(ctx context.Context, endpoint string) (int, error) {
	probe, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	conn, err := transport.Dial(probe, endpoint)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	return peerPID(conn)
}

// List verifies kernel process identity (including creation time) and the actual
// socket/pipe server PID. It sends no SSH request and never contacts KeePassXC.
// A fallback discovers the installed proxy from older versions that predate
// registration; arbitrary older custom endpoints require a proxy restart.
func List(ctx context.Context, root string, fallback *Candidate) ([]Record, error) {
	result := []Record{}
	dir := filepath.Join(root, "instances")
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if err := privateDirectory(root); err != nil {
			return nil, err
		}
		if err := privateDirectory(dir); err != nil {
			return nil, err
		}
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		record, err := readRecord(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		identity, err := processIdentity(record.PID)
		if err != nil || identity != record.Identity {
			continue
		}
		pid, err := endpointPID(ctx, record.Endpoint)
		if err != nil || pid != record.PID {
			continue
		}
		// Recheck after connecting: a reused PID must not validate a stale record.
		identity, err = processIdentity(pid)
		if err != nil || identity != record.Identity {
			continue
		}
		key := fmt.Sprintf("%d:%s", pid, record.Endpoint)
		if !seen[key] {
			result = append(result, record)
			seen[key] = true
		}
	}
	if fallback != nil && localEndpoint(fallback.Endpoint) {
		found := false
		for _, r := range result {
			if transport.SameEndpoint(r.Endpoint, fallback.Endpoint) {
				found = true
				break
			}
		}
		if !found {
			if record, err := discover(ctx, *fallback); err == nil {
				result = append(result, record)
			}
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].PID == result[j].PID {
			return result[i].Endpoint < result[j].Endpoint
		}
		return result[i].PID < result[j].PID
	})
	return result, nil
}
func discover(ctx context.Context, c Candidate) (Record, error) {
	pid, err := endpointPID(ctx, c.Endpoint)
	if err != nil {
		return Record{}, err
	}
	identity, err := processIdentity(pid)
	if err != nil {
		return Record{}, err
	}
	image, err := processImage(ctx, pid)
	if err != nil {
		return Record{}, err
	}
	expected, err := os.Stat(c.Binary)
	if err != nil {
		return Record{}, err
	}
	actual, err := os.Stat(image)
	if err != nil || !os.SameFile(expected, actual) {
		return Record{}, errors.New("endpoint is not served by the expected executable")
	}
	again, err := processIdentity(pid)
	if err != nil || again != identity {
		return Record{}, errors.New("process changed during discovery")
	}
	return Record{Version: 1, PID: pid, Identity: identity, Endpoint: c.Endpoint, Binary: image, Config: c.Config, LogFile: c.LogFile}, nil
}
