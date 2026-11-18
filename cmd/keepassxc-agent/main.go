package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"keepassxc-agent-middleware/internal/browser"
	"keepassxc-agent-middleware/internal/instances"
	"keepassxc-agent-middleware/internal/lifecycle"
	"keepassxc-agent-middleware/internal/proxy"
	"keepassxc-agent-middleware/internal/settings"
	"keepassxc-agent-middleware/internal/transport"
)

func main() {
	if err := execute(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func execute(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		args = []string{"help"}
	}
	command := args[0]
	if command == "help" || command == "-h" || command == "--help" {
		fmt.Fprintln(stdout, `KeePassXC SSH agent middleware

Usage: keepassxc-agent <run|status|paths|instances|logs|install|uninstall|start|stop|restart> [options]

  run     Listen for SSH agent requests; unlock KeePassXC only when needed.
  status  Check proxy, original SSH agent and KeePassXC; never trigger unlock.
  paths   Print resolved endpoints as JSON (uses installed config if present).
  instances List running proxies of the current user (supports --json).
  logs    Show recent log lines; --follow streams new output, --lines N selects the tail.
  install Install and start a per-user login service; configure SSH with a backup.
  uninstall Stop and remove the managed service and SSH block; preserve other edits.
  start / stop / restart
          Control the installed proxy without removing its login configuration.

Installer options: --dry-run, --no-ssh, --no-path (install only), --listen, --upstream,
                   --browser, --fingerprint, --timeout. Use install --help.

Options:
  --config PATH       Runtime configuration (defaults to ~/.keepassxc-agent/config.json)
  --listen PATH       Proxy socket / Windows named pipe (must differ from upstream)
  --upstream PATH     Original SSH agent (SSH_AUTH_SOCK on Unix, OpenSSH pipe on Windows)
  --browser PATH      KeePassXC browser socket / named pipe
  --fingerprint HASH  Expected SHA256 public-key fingerprint (optional)
  --timeout 60s       Maximum wait for unlock and key loading
  --log-file PATH     Append logs to a file instead of stderr
  --verbose          Enable diagnostic logs (never key or message contents)

Keep KeePassXC connected to the original agent. Point SSH IdentityAgent at
--listen. See README.md for per-platform login services.`)
		return nil
	}
	if command == "instances" || command == "instancias" {
		return runInstances(args[1:], stdout, stderr)
	}
	if command == "logs" {
		return runLogs(args[1:], stdout, stderr)
	}
	if command == "install" || command == "uninstall" {
		return installation(command, args[1:], stdout, stderr)
	}
	if command == "start" || command == "stop" || command == "restart" {
		if len(args) != 1 {
			return fmt.Errorf("usage: keepassxc-agent %s", command)
		}
		if err := controlInstalled(command); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Proxy action completed: %s.\n", command)
		return nil
	}
	if command != "run" && command != "status" && command != "paths" {
		return fmt.Errorf("unknown command %q; use help", command)
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", settings.DefaultPath(), "runtime configuration")
	listen := flags.String("listen", transport.DefaultListen(), "proxy endpoint")
	upstream := flags.String("upstream", transport.DefaultUpstream(), "original SSH agent")
	browserPath := flags.String("browser", browser.DefaultEndpoint(), "KeePassXC endpoint")
	expected := flags.String("fingerprint", "", "expected SHA256 fingerprint")
	timeout := flags.Duration("timeout", 60*time.Second, "unlock timeout")
	logFile := flags.String("log-file", "", "append log file")
	verbose := flags.Bool("verbose", false, "diagnostic logging")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if err := applyConfig(flags, *configPath, listen, upstream, browserPath, expected, timeout, logFile); err != nil {
		return err
	}
	if command == "paths" {
		return json.NewEncoder(stdout).Encode(map[string]string{"listen": *listen, "upstream": *upstream, "browser": *browserPath})
	}
	if *listen == "" || *upstream == "" || *browserPath == "" {
		return errors.New("listen, upstream and browser endpoints must be set; supply --upstream if SSH_AUTH_SOCK is unset")
	}
	if transport.SameEndpoint(*listen, *upstream) || transport.SameEndpoint(*listen, *browserPath) {
		return errors.New("proxy endpoint must differ from upstream and browser endpoints")
	}
	if *timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	if *expected != "" {
		sum, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(*expected, "SHA256:"))
		if !strings.HasPrefix(*expected, "SHA256:") || err != nil || len(sum) != 32 {
			return errors.New("fingerprint must be an OpenSSH SHA256 fingerprint (ssh-add -l)")
		}
	}
	if command == "status" {
		if err := installedStatus(stdout); err != nil {
			fmt.Fprintf(stdout, "service: %v\n", err)
		}
		failed := false
		for _, item := range []struct{ name, path string }{{"proxy", *listen}, {"keepassxc", *browserPath}} {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			conn, err := transport.Dial(ctx, item.path)
			cancel()
			if err != nil {
				failed = true
				fmt.Fprintf(stdout, "%s: unavailable (%v)\n", item.name, err)
			} else {
				conn.Close()
				fmt.Fprintf(stdout, "%s: reachable (%s)\n", item.name, item.path)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		count, err := proxy.Probe(ctx, *upstream)
		if err != nil {
			failed = true
			fmt.Fprintf(stdout, "upstream: unavailable (%v)\n", err)
		} else {
			fmt.Fprintf(stdout, "upstream: %d loaded key(s)\n", count)
		}
		if failed {
			return errors.New("one or more endpoints are unavailable")
		}
		return nil
	}
	logOutput := stderr
	if *logFile != "" {
		file, err := os.OpenFile(*logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		defer file.Close()
		logOutput = file
	}
	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(logOutput, &slog.HandlerOptions{Level: level}))
	signals, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	ctx, cancel, err := lifecycle.Context(signals)
	if err != nil {
		return fmt.Errorf("register stop handler: %w", err)
	}
	defer cancel()
	listener, err := transport.Listen(*listen)
	if err != nil {
		return fmt.Errorf("listen (an existing socket is never overwritten): %w", err)
	}
	defer listener.Close()
	unregister, err := instances.Register(settings.Root(), instances.Candidate{Endpoint: *listen, Config: *configPath, LogFile: *logFile})
	if err != nil {
		return fmt.Errorf("register running instance: %w", err)
	}
	defer unregister()
	server := proxy.Server{Upstream: *upstream, Fingerprint: *expected, Unlocker: browser.Client{Endpoint: *browserPath}, Timeout: *timeout, Logger: logger}
	logger.Info("proxy listening", "listen", *listen, "upstream", *upstream)
	return server.Serve(ctx, listener)
}
