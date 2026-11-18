package tray

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func awaitController(t *testing.T, c *Controller) View {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		v := c.View()
		if !v.Busy {
			return v
		}
		select {
		case <-c.Changed():
		case <-deadline:
			t.Fatal("controller remained busy")
		}
	}
}

func TestControllerGuardsLifecycleAndQuit(t *testing.T) {
	blocked := make(chan struct{})
	var calls atomic.Int32
	c := NewController(Handlers{Snapshot: func() Snapshot { return Snapshot{Installed: true, Proxy: true, Healthy: true} }, Control: func(string) error { calls.Add(1); <-blocked; return errors.New("service unavailable") }, OpenLogs: func() error { return nil }})
	if c.Select("stop") {
		t.Fatal("stop accepted before installation was known")
	}
	c.Select("refresh")
	awaitController(t, c)
	if c.Select("start") {
		t.Fatal("duplicate start accepted")
	}
	if !c.Select("stop") {
		t.Fatal("valid stop rejected")
	}
	if c.CanQuit() || c.Select("restart") {
		t.Fatal("in-flight lifecycle action could be interrupted or duplicated")
	}
	close(blocked)
	v := awaitController(t, c)
	if calls.Load() != 1 || v.Error == "" || v.ErrorID != 1 || !c.CanQuit() {
		t.Fatalf("action result lost: %+v, calls=%d", v, calls.Load())
	}
}
