package tray

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

type testWatcher struct{ registered chan string }

func (w *testWatcher) RegisterStatusNotifierItem(name string) *dbus.Error {
	w.registered <- name
	return nil
}

func TestKDEProtocolLifecycle(t *testing.T) {
	if os.Getenv("KAM_TEST_DBUS") != "1" {
		t.Skip("private D-Bus session not requested")
	}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	watcher := &testWatcher{make(chan string, 8)}
	if err := conn.Export(watcher, "/StatusNotifierWatcher", "org.kde.StatusNotifierWatcher"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.RequestName("org.kde.StatusNotifierWatcher", dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("/tmp", "kam-tray-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	var active atomic.Bool
	active.Store(true)
	var calls atomic.Int32
	done := make(chan error, 1)
	h := Handlers{RuntimeRoot: root, Snapshot: func() Snapshot {
		return Snapshot{Installed: true, Proxy: active.Load(), Healthy: true, Details: "Proxy: disponible\nAgente original: disponible"}
	}, Control: func(action string) error { calls.Add(1); active.Store(action != "stop"); return nil }, OpenLogs: func() error { return nil }}
	go func() { done <- Run(h) }()
	awaitRegister := func() {
		select {
		case name := <-watcher.registered:
			if name != trayBusName {
				t.Fatal(name)
			}
		case err := <-done:
			t.Fatalf("tray exited: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("item was not registered")
		}
	}
	awaitRegister()
	obj := conn.Object(trayBusName, menuPath)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	get := func(id int32) map[string]dbus.Variant {
		var revision uint32
		var node layout
		if err := obj.CallWithContext(ctx, menuInterface+".GetLayout", 0, id, int32(0), []string{}).Store(&revision, &node); err != nil {
			t.Fatal(err)
		}
		return node.Properties
	}
	awaitEnabled := func(id int32, want bool) {
		deadline := time.After(4 * time.Second)
		for {
			if get(id)["enabled"].Value() == want {
				return
			}
			select {
			case <-deadline:
				t.Fatalf("menu %d did not become enabled=%t", id, want)
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	awaitEnabled(4, true)
	if get(3)["enabled"].Value() != false {
		t.Fatal("start was enabled for a running proxy")
	}
	// Store recursively re-infers variant signatures for decoded generic
	// structs. Inspect the wire variants directly, as Plasma receives them.
	reply := conn.Object(trayBusName, itemPath).CallWithContext(ctx, "org.freedesktop.DBus.Properties.GetAll", 0, itemInterface)
	if reply.Err != nil {
		t.Fatal(reply.Err)
	}
	props, ok := reply.Body[0].(map[string]dbus.Variant)
	if !ok {
		t.Fatal("invalid property reply")
	}
	if props["IconPixmap"].Signature().String() != "a(iiay)" || props["ToolTip"].Signature().String() != "(sa(iiay)ss)" || props["Menu"].Value() != menuPath {
		t.Fatalf("invalid SNI property signatures: %s %s", props["IconPixmap"].Signature(), props["ToolTip"].Signature())
	}
	var rev uint32
	var filtered layout
	if err := obj.CallWithContext(ctx, menuInterface+".GetLayout", 0, int32(4), int32(0), []string{"enabled"}).Store(&rev, &filtered); err != nil || len(filtered.Properties) != 1 {
		t.Fatalf("property selection failed: %+v %v", filtered, err)
	}
	if err := obj.CallWithContext(ctx, menuInterface+".Event", 0, int32(4), "clicked", dbus.MakeVariant(int32(0)), uint32(0)).Err; err != nil {
		t.Fatal(err)
	}
	awaitEnabled(3, true)
	if calls.Load() != 1 {
		t.Fatalf("stop was not delivered exactly once: %d", calls.Load())
	}
	// An early second opening must reuse the same private controller.
	if err := Run(h); err != nil {
		t.Fatal(err)
	}
	conn.ReleaseName("org.kde.StatusNotifierWatcher")
	conn.RequestName("org.kde.StatusNotifierWatcher", dbus.NameFlagDoNotQueue)
	awaitRegister()
	if err := obj.CallWithContext(ctx, menuInterface+".Event", dbus.FlagNoReplyExpected, int32(7), "clicked", dbus.MakeVariant(int32(0)), uint32(0)).Err; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("quit did not remove tray")
	}
	if calls.Load() != 1 {
		t.Fatal("quitting the tray controlled the proxy")
	}
	if _, err := os.Stat(filepath.Join(root, "tray.sock")); !os.IsNotExist(err) {
		t.Fatal("tray control socket was left behind")
	}
}
