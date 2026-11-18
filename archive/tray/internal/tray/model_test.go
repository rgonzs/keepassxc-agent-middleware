package tray

import (
	"strings"
	"testing"
)

func TestConnectivityLabels(t *testing.T) {
	for _, tt := range []struct {
		state Snapshot
		want  string
	}{
		{Snapshot{}, "sin instalar"},
		{Snapshot{Installed: true}, "sin conexión"},
		{Snapshot{Installed: true, Proxy: true}, "revisar conexiones"},
		{Snapshot{Installed: true, Proxy: true, Healthy: true}, "disponible"},
	} {
		if !strings.Contains(tt.state.Label(), tt.want) {
			t.Errorf("%+v: %s", tt.state, tt.state.Label())
		}
	}
}
