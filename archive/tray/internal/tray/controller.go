package tray

import (
	"fmt"
	"sync"
)

type View struct {
	Snapshot    Snapshot `json:"snapshot"`
	Busy        bool     `json:"busy"`
	Controlling bool     `json:"controlling"`
	Activity    string   `json:"activity"`
	Error       string   `json:"error"`
	ErrorID     uint64   `json:"errorID"`
}

type Controller struct {
	mu       sync.Mutex
	view     View
	handlers Handlers
	changed  chan struct{}
}

func NewController(h Handlers) *Controller {
	return &Controller{handlers: h, changed: make(chan struct{}, 1)}
}
func (c *Controller) Changed() <-chan struct{} { return c.changed }
func (c *Controller) View() View               { c.mu.Lock(); defer c.mu.Unlock(); return c.view }
func (c *Controller) notify() {
	select {
	case c.changed <- struct{}{}:
	default:
	}
}

// Select rejects disabled/stale menu actions before starting asynchronous work.
// UI callbacks never block on endpoint probes or service-manager commands.
func (c *Controller) Select(action string) bool {
	c.mu.Lock()
	if c.view.Busy {
		c.mu.Unlock()
		return false
	}
	s := c.view.Snapshot
	if (action == "start" && (!s.Installed || s.Proxy)) || (action == "stop" && (!s.Installed || !s.Proxy)) || (action == "restart" && !s.Installed) {
		c.mu.Unlock()
		return false
	}
	activity := "Comprobando conexiones…"
	control := false
	switch action {
	case "refresh":
	case "logs":
		activity = "Abriendo logs…"
	case "start":
		activity, control = "Iniciando middleware…", true
	case "stop":
		activity, control = "Deteniendo middleware…", true
	case "restart":
		activity, control = "Reiniciando middleware…", true
	default:
		c.mu.Unlock()
		return false
	}
	c.view.Busy, c.view.Controlling, c.view.Activity, c.view.Error = true, control, activity, ""
	c.mu.Unlock()
	c.notify()
	go func() {
		var err error
		if control {
			err = c.handlers.Control(action)
		} else if action == "logs" {
			err = c.handlers.OpenLogs()
		}
		snapshot := c.handlers.Snapshot()
		c.mu.Lock()
		c.view.Snapshot, c.view.Busy, c.view.Controlling = snapshot, false, false
		if err != nil {
			c.view.Error = fmt.Sprintf("No se pudo completar la acción: %v", err)
			c.view.ErrorID++
		}
		c.mu.Unlock()
		c.notify()
	}()
	return true
}

func (c *Controller) CanQuit() bool { c.mu.Lock(); defer c.mu.Unlock(); return !c.view.Controlling }
