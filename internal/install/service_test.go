package install

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestCommandSeparatesDiagnosticsFromState(t *testing.T) {
	for _, state := range []string{"absent", "registered", "failed"} {
		t.Run(state, func(t *testing.T) {
			t.Setenv("KEEPASSXC_TEST_COMMAND_STATE", state)
			output, err := command(context.Background(), os.Args[0], "-test.run=^TestServiceCommandHelper$")
			if state == "failed" {
				if err == nil || !strings.Contains(err.Error(), "access denied") || !strings.Contains(output, "access denied") || !strings.Contains(output, "partial output") {
					t.Fatalf("missing failure diagnostics: output=%q err=%v", output, err)
				}
				return
			}
			if err != nil || output != state {
				t.Fatalf("diagnostics contaminated state: output=%q err=%v", output, err)
			}
		})
	}
}

func TestServiceCommandHelper(t *testing.T) {
	state := os.Getenv("KEEPASSXC_TEST_COMMAND_STATE")
	if state == "" {
		return
	}
	if state == "failed" {
		fmt.Fprintln(os.Stdout, "partial output")
		fmt.Fprintln(os.Stderr, "access denied")
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "#< CLIXML\n<Objs><S S=\"warning\">Preparing modules for first use.</S></Objs>")
	fmt.Fprintln(os.Stdout, state)
	os.Exit(0)
}
