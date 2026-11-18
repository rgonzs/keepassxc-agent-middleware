package main

import (
	"bytes"
	"testing"
)

func TestCLIValidation(t *testing.T) {
	for _, args := range [][]string{
		{"run", "--listen", "same", "--upstream", "same"},
		{"run", "--upstream", "original", "--timeout", "0s"},
		{"run", "--upstream", "original", "--fingerprint", "invalid"},
		{"run", "--upstream", "original", "extra"},
		{"unknown"},
		{"tray"},
		{"stop", "unexpected"},
		{"start", "--listen", "other"},
		{"restart", "extra"},
	} {
		if err := execute(args, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestPathsAndHelp(t *testing.T) {
	for _, args := range [][]string{{"paths"}, {"help"}, {"run", "--help"}} {
		var out bytes.Buffer
		if err := execute(args, &out, &out); err != nil {
			t.Fatal(err)
		}
		if out.Len() == 0 {
			t.Fatal("no output")
		}
	}
}
