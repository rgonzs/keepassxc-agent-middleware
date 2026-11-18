package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Exercise the actual PowerShell/.NET launch with a console-subsystem child.
// A hidden window still has a console handle; CreateNoWindow must prevent it.
func TestWindowsWorkerWithoutConsole(t *testing.T) {
	root := filepath.Join(t.TempDir(), "space and apostrophe's")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "console helper.exe")
	if err := os.WriteFile(child, data, 0700); err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(root, "console.txt")
	t.Setenv("KEEPASSXC_TEST_CONSOLE_RESULT", result)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = powershell(ctx, windowsWorker(child, `-test.run=^TestWindowsWorkerConsoleHelper$ -- "`+result+`"`))
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 23 {
		t.Fatalf("worker did not wait for or propagate child exit: %v", err)
	}
	data, err = os.ReadFile(result)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "0" {
		t.Fatalf("child has a console: %s", data)
	}
}

func TestWindowsRegistersIndependentOwnedTasks(t *testing.T) {
	p := mustPlan(t, fixture(t, "windows"))
	// Execute the real registration script, replacing only scheduler commands.
	// This exercises PowerShell parsing/quoting without registering real tasks.
	mocks := `
$script:tasks = @()
$script:started = @()
function Get-ScheduledTask { param($TaskName, $ErrorAction) return $null }
function New-ScheduledTaskAction { param($Execute, $Argument) return [pscustomobject]@{Execute=$Execute; Argument=$Argument} }
function New-ScheduledTaskTrigger { param([switch]$AtLogOn, $User) return 'trigger' }
function New-ScheduledTaskPrincipal { param($UserId, $LogonType, $RunLevel) return 'principal' }
function New-ScheduledTaskSettingsSet { return 'settings' }
function Register-ScheduledTask {
    param($TaskName, $Description, $Action, $Trigger, $Principal, $Settings, [switch]$Force)
    $script:tasks += [pscustomobject]@{Name=$TaskName; Description=$Description; Argument=$Action.Argument}
}
function Start-ScheduledTask { param($TaskName) $script:started += $TaskName }
function Enable-ScheduledTask { param($TaskName) }
`
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := powershell(ctx, mocks+string(p.serviceFile())+"\n[pscustomobject]@{Tasks=$script:tasks; Started=$script:started} | ConvertTo-Json -Depth 5 -Compress")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Tasks   []struct{ Name, Description, Argument string }
		Started []string
	}
	if err := json.Unmarshal([]byte(output), &got); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	if len(got.Tasks) != 2 || len(got.Started) != 2 {
		t.Fatalf("not both tasks started: %+v", got)
	}
	if got.Tasks[0].Name != p.TaskName || got.Tasks[1].Name != p.TaskName+"-Tray" {
		t.Fatalf("wrong tasks: %+v", got.Tasks)
	}
	for _, task := range got.Tasks {
		if task.Description != "keepassxc-agent:"+p.Root || !strings.Contains(task.Argument, "-WindowStyle Hidden -EncodedCommand") {
			t.Fatalf("task is not owned or hidden: %+v", task)
		}
	}
	// A conflicting tray task must be rejected before modifying the proxy task.
	conflict := `
function Get-ScheduledTask { param($TaskName, $ErrorAction) if ($TaskName.EndsWith('-Tray')) { return [pscustomobject]@{Description='other application'} } }
function Register-ScheduledTask { throw 'registration must not happen' }
`
	_, err = powershell(ctx, conflict+string(p.serviceFile()))
	if err == nil || !strings.Contains(err.Error(), "Refusing to replace an unrelated scheduled task") {
		t.Fatalf("did not protect unrelated tray task: %v", err)
	}
}

func TestWindowsWorkerConsoleHelper(t *testing.T) {
	result := os.Getenv("KEEPASSXC_TEST_CONSOLE_RESULT")
	if result == "" {
		return
	}
	if os.Args[len(os.Args)-1] != result {
		os.Exit(25)
	}
	getConsoleWindow := syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleWindow")
	window, _, _ := getConsoleWindow.Call()
	if err := os.WriteFile(result, []byte(fmt.Sprint(window)), 0600); err != nil {
		os.Exit(24)
	}
	os.Exit(23)
}
