package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"keepassxc-agent-middleware/internal/instances"
)

func TestInstancesOutput(t *testing.T) {
	records := []instances.Record{{PID: 123, Endpoint: "default-endpoint", Binary: "proxy", LogFile: "current.log", Started: time.Now()}, {PID: 456, Endpoint: "other\nendpoint", Binary: "proxy"}}
	var out bytes.Buffer
	if err := printInstances(records, "default-endpoint", false, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"123", "456", "default", "additional", "current.log", `other\u000aendpoint`} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q: %s", want, out.String())
		}
	}
	out.Reset()
	if err := printInstances(records, "default-endpoint", true, &out); err != nil {
		t.Fatal(err)
	}
	var views []instanceView
	if err := json.Unmarshal(out.Bytes(), &views); err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 || !views[0].Default || views[1].Default || views[1].Listen != "other\nendpoint" {
		t.Fatalf("bad JSON: %+v", views)
	}
	if strings.Contains(out.String(), "identity") {
		t.Fatal("internal identity leaked into output")
	}
	out.Reset()
	if err := printInstances(nil, "default", true, &out); err != nil || out.String() != "[]\n" {
		t.Fatalf("empty JSON: %q %v", out.String(), err)
	}
	for _, args := range [][]string{{"instances", "extra"}, {"instances", "--unknown"}} {
		if err := execute(args, io.Discard, io.Discard); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if err := execute([]string{"instances", "--help"}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
}
