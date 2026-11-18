package tray

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

func Run(h Handlers) error {
	if h.NativeHelper == "" {
		return errors.New("macOS tray helper is not configured")
	}
	c := NewController(h)
	ctx, stop, already, err := unixRuntime(h, c)
	if err != nil || already {
		return err
	}
	defer stop()
	cmd := exec.Command(h.NativeHelper)
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start AppKit helper: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	waited := false
	defer func() {
		input.Close()
		if !waited {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				cmd.Process.Kill()
				<-done
			}
		}
	}()
	actions := make(chan string, 16)
	readErrors := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			var message struct {
				Action string `json:"action"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
				readErrors <- err
				return
			}
			select {
			case actions <- message.Action:
			case <-ctx.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil {
			readErrors <- err
		}
	}()
	encoder := json.NewEncoder(input)
	c.Select("refresh")
	if err := encoder.Encode(c.View()); err != nil {
		return err
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-done:
			waited = true
			return err
		case err := <-readErrors:
			return fmt.Errorf("AppKit controller protocol: %w", err)
		case <-ticker.C:
			c.Select("refresh")
		case <-c.Changed():
			if err := encoder.Encode(c.View()); err != nil {
				return err
			}
		case action := <-actions:
			if action == "quit" {
				if c.CanQuit() {
					return nil
				}
			} else {
				c.Select(action)
			}
		}
	}
}
