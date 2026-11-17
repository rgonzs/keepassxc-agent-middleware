// Package tray provides the independent Windows notification-area controller.
package tray

type Snapshot struct {
	Installed bool
	Proxy     bool
	Healthy   bool
	Details   string
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
	Snapshot func() Snapshot
	Control  func(action string) error
	OpenLogs func() error
}
