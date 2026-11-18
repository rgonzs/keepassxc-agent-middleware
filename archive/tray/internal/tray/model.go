// Package tray provides independent native desktop controllers for the proxy.
package tray

type Snapshot struct {
	Installed bool   `json:"installed"`
	Proxy     bool   `json:"proxy"`
	Healthy   bool   `json:"healthy"`
	Details   string `json:"details"`
}

func (s Snapshot) Label() string {
	if !s.Installed {
		return "Middleware: sin instalar"
	}
	if !s.Proxy {
		return "Middleware: sin conexión"
	}
	if !s.Healthy {
		return "Middleware: revisar conexiones"
	}
	return "Middleware: disponible"
}

type Handlers struct {
	NativeHelper string
	RuntimeRoot  string
	Snapshot     func() Snapshot
	Control      func(action string) error
	OpenLogs     func() error
}
