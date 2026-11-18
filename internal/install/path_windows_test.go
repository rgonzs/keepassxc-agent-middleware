package install

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestWindowsCLIShimForwardsArgumentsAndExit(t *testing.T) {
	o := fixture(t, "windows")
	o.ConfigurePath = true
	o.Home = filepath.Join(o.Home, "space and apostrophe's")
	var err error
	o.Source, err = os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p := mustPlan(t, o)
	if err := p.Install(context.Background(), &fakeManager{}); err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(t.TempDir(), "args.json")
	t.Setenv("KAM_CLI_SHIM_RESULT", result)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	output, err := powershell(ctx, "& "+ps(p.Launcher)+" "+ps("-test.run=^TestWindowsCLIShimHelper$")+" -- "+ps("one two")+" "+ps("it's literal")+"; exit $LASTEXITCODE")
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 23 {
		t.Fatalf("shim exit not forwarded: %v: %s", err, output)
	}
	var args []string
	if err := json.Unmarshal(mustRead(t, result), &args); err != nil {
		t.Fatal(err)
	}
	if len(args) != 2 || args[0] != "one two" || args[1] != "it's literal" {
		t.Fatalf("shim changed arguments: %q", args)
	}
}
func TestWindowsCLIShimHelper(t *testing.T) {
	result := os.Getenv("KAM_CLI_SHIM_RESULT")
	if result == "" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			data, _ := json.Marshal(os.Args[i+1:])
			if err := os.WriteFile(result, data, 0600); err != nil {
				os.Exit(9)
			}
			os.Exit(23)
		}
	}
	os.Exit(8)
}
