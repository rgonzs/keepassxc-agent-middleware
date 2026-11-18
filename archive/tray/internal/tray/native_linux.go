package tray

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
)

const itemInterface = "org.kde.StatusNotifierItem"
const menuInterface = "com.canonical.dbusmenu"
const trayBusName = "org.keepassxc.AgentTray"
const itemPath dbus.ObjectPath = "/StatusNotifierItem"
const menuPath dbus.ObjectPath = "/Menu"

type pixmap struct {
	Width, Height int32
	Pixels        []byte
}
type tooltip struct {
	Icon               string
	Pixmaps            []pixmap
	Title, Description string
}
type layout struct {
	ID         int32
	Properties map[string]dbus.Variant
	Children   []dbus.Variant
}
type itemProperties struct {
	ID         int32
	Properties map[string]dbus.Variant
}
type menuEvent struct {
	ID        int32
	Event     string
	Data      dbus.Variant
	Timestamp uint32
}

type linuxTray struct {
	controller *Controller
	conn       *dbus.Conn
	props      *prop.Properties
	quit       chan struct{}
	revision   atomic.Uint32
}

func Run(h Handlers) error {
	c := NewController(h)
	ctx, stop, already, err := unixRuntime(h, c)
	if err != nil || already {
		return err
	}
	defer stop()
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("connect to KDE session bus: %w", err)
	}
	defer conn.Close()
	reply, err := conn.RequestName(trayBusName, dbus.NameFlagDoNotQueue)
	if err != nil {
		return err
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return nil
	}
	a := &linuxTray{controller: c, conn: conn, quit: make(chan struct{}, 1)}
	if err := a.export(); err != nil {
		return err
	}
	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)
	if err := conn.AddMatchSignal(dbus.WithMatchInterface("org.freedesktop.DBus"), dbus.WithMatchMember("NameOwnerChanged")); err != nil {
		return err
	}
	a.register(ctx)
	c.Select("refresh")
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-a.quit:
			return nil
		case <-conn.Context().Done():
			return errors.New("KDE session bus disconnected")
		case <-ticker.C:
			c.Select("refresh")
		case <-c.Changed():
			a.update()
		case sig := <-signals:
			if sig != nil && len(sig.Body) == 3 {
				name, _ := sig.Body[0].(string)
				owner, _ := sig.Body[2].(string)
				if owner != "" && (name == "org.kde.StatusNotifierWatcher" || name == "org.freedesktop.StatusNotifierWatcher") {
					a.register(ctx)
				}
			}
		}
	}
}

func (a *linuxTray) register(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	for _, service := range []string{"org.kde.StatusNotifierWatcher", "org.freedesktop.StatusNotifierWatcher"} {
		err := a.conn.Object(service, "/StatusNotifierWatcher").CallWithContext(ctx, service+".RegisterStatusNotifierItem", 0, trayBusName).Err
		if err == nil {
			return
		}
	}
	// No watcher yet is normal during login. NameOwnerChanged registers the
	// same item when Plasma starts or its panel is restarted.
}

func iconPixmaps(s Snapshot) []pixmap {
	im := KeyIcon(s)
	data := make([]byte, 32*32*4)
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			c := im.NRGBAAt(x, y)
			i := (y*32 + x) * 4
			data[i], data[i+1], data[i+2], data[i+3] = c.A, c.R, c.G, c.B
		}
	}
	return []pixmap{{32, 32, data}}
}

func (a *linuxTray) export() error {
	notifierMethods := map[string]any{"Activate": a.Activate, "SecondaryActivate": a.SecondaryActivate, "ContextMenu": a.ContextMenu, "Scroll": a.Scroll}
	menuMethods := map[string]any{"GetLayout": a.GetLayout, "GetGroupProperties": a.GetGroupProperties, "GetProperty": a.GetProperty, "Event": a.Event, "EventGroup": a.EventGroup, "AboutToShow": a.AboutToShow, "AboutToShowGroup": a.AboutToShowGroup}
	if err := a.conn.ExportMethodTable(notifierMethods, itemPath, itemInterface); err != nil {
		return err
	}
	if err := a.conn.ExportMethodTable(menuMethods, menuPath, menuInterface); err != nil {
		return err
	}
	props := map[string]*prop.Prop{}
	values := map[string]any{"Category": "ApplicationStatus", "Id": "keepassxc-agent", "Title": "KeePassXC Agent", "Status": "Active", "WindowId": uint32(0), "IconName": "", "IconPixmap": iconPixmaps(Snapshot{}), "AttentionIconName": "", "AttentionIconPixmap": []pixmap{}, "OverlayIconName": "", "OverlayIconPixmap": []pixmap{}, "ToolTip": tooltip{"", iconPixmaps(Snapshot{}), "KeePassXC Agent", "Comprobando conexiones…"}, "ItemIsMenu": true, "Menu": menuPath}
	for name, value := range values {
		props[name] = &prop.Prop{Value: value, Emit: prop.EmitTrue}
	}
	var err error
	a.props, err = prop.Export(a.conn, itemPath, map[string]map[string]*prop.Prop{itemInterface: props})
	if err != nil {
		return err
	}
	menuProps, err := prop.Export(a.conn, menuPath, map[string]map[string]*prop.Prop{menuInterface: {"Version": {Value: uint32(4), Emit: prop.EmitConst}, "TextDirection": {Value: "ltr", Emit: prop.EmitConst}, "Status": {Value: "normal", Emit: prop.EmitConst}, "IconThemePath": {Value: []string{}, Emit: prop.EmitConst}}})
	if err != nil {
		return err
	}
	for _, spec := range []struct {
		path  dbus.ObjectPath
		iface string
	}{{itemPath, itemInterface}, {menuPath, menuInterface}} {
		node := &introspect.Node{Name: string(spec.path), Interfaces: []introspect.Interface{{Name: spec.iface, Methods: introspect.Methods(a)}, prop.IntrospectData}}
		allowed := menuMethods
		if spec.iface == itemInterface {
			allowed = notifierMethods
		}
		methods := []introspect.Method{}
		for _, m := range node.Interfaces[0].Methods {
			if _, ok := allowed[m.Name]; ok {
				methods = append(methods, m)
			}
		}
		node.Interfaces[0].Methods = methods
		if spec.iface == itemInterface {
			for name, p := range props {
				node.Interfaces[0].Properties = append(node.Interfaces[0].Properties, introspect.Property{Name: name, Type: dbus.SignatureOf(p.Value).String(), Access: "read"})
			}
			node.Interfaces[0].Signals = []introspect.Signal{{Name: "NewIcon"}, {Name: "NewToolTip"}}
		} else {
			node.Interfaces[0].Properties = menuProps.Introspection(menuInterface)
			node.Interfaces[0].Signals = []introspect.Signal{{Name: "LayoutUpdated", Args: []introspect.Arg{{Name: "revision", Type: "u"}, {Name: "parent", Type: "i"}}}}
		}
		if err := a.conn.Export(introspect.NewIntrospectable(node), spec.path, "org.freedesktop.DBus.Introspectable"); err != nil {
			return err
		}
	}
	return nil
}

func (a *linuxTray) update() {
	v := a.controller.View()
	label := v.Snapshot.Label()
	if v.Busy {
		label = v.Activity
	}
	a.props.SetMust(itemInterface, "IconPixmap", iconPixmaps(v.Snapshot))
	a.props.SetMust(itemInterface, "ToolTip", tooltip{"", iconPixmaps(v.Snapshot), "KeePassXC Agent", label})
	a.conn.Emit(itemPath, itemInterface+".NewIcon")
	a.conn.Emit(itemPath, itemInterface+".NewToolTip")
	a.conn.Emit(menuPath, menuInterface+".LayoutUpdated", a.revision.Add(1), int32(0))
}

func entry(id int32, label string, enabled bool) layout {
	return layout{id, map[string]dbus.Variant{"label": dbus.MakeVariant(label), "enabled": dbus.MakeVariant(enabled), "visible": dbus.MakeVariant(true)}, []dbus.Variant{}}
}
func (a *linuxTray) tree() layout {
	v := a.controller.View()
	label := v.Snapshot.Label()
	if v.Busy {
		label = v.Activity
	}
	items := []layout{entry(10, label, false), entry(1, "Ver estado", true), entry(2, "Actualizar estado", !v.Busy), entry(3, "Iniciar middleware", !v.Busy && v.Snapshot.Installed && !v.Snapshot.Proxy), entry(4, "Detener middleware", !v.Busy && v.Snapshot.Installed && v.Snapshot.Proxy), entry(5, "Reiniciar middleware", !v.Busy && v.Snapshot.Installed), entry(6, "Abrir logs", !v.Busy), entry(7, "Salir de la bandeja (SSH sigue activo)", !v.Controlling)}
	items[1].Properties["children-display"] = dbus.MakeVariant("submenu")
	details := v.Snapshot.Details + "\nLa conectividad no indica si la bóveda está desbloqueada."
	if v.Error != "" {
		details = v.Error + "\n" + details
	}
	for i, line := range strings.Split(details, "\n") {
		if i > 30 {
			break
		}
		if line == "" {
			continue
		}
		runes := []rune(line)
		if len(runes) > 100 {
			line = string(runes[:99]) + "…"
		}
		items[1].Children = append(items[1].Children, dbus.MakeVariant(entry(int32(100+i), line, false)))
	}
	root := entry(0, "", true)
	for i, item := range items {
		if i == 3 || i == 6 || i == 7 {
			sep := entry(int32(20+i), "", false)
			sep.Properties["type"] = dbus.MakeVariant("separator")
			root.Children = append(root.Children, dbus.MakeVariant(sep))
		}
		root.Children = append(root.Children, dbus.MakeVariant(item))
	}
	return root
}

func findLayout(root layout, id int32) (layout, bool) {
	if root.ID == id {
		return root, true
	}
	for _, child := range root.Children {
		if node, ok := child.Value().(layout); ok {
			if found, yes := findLayout(node, id); yes {
				return found, true
			}
		}
	}
	return layout{}, false
}

func (a *linuxTray) GetLayout(id, depth int32, names []string) (uint32, layout, *dbus.Error) {
	root, ok := findLayout(a.tree(), id)
	if !ok {
		return 0, layout{}, dbus.MakeFailedError(errors.New("unknown menu item"))
	}
	var prune func(layout, int32) layout
	prune = func(node layout, d int32) layout {
		node.Properties = selectedProperties(node.Properties, names)
		if d == 0 {
			node.Children = []dbus.Variant{}
		} else {
			for i, c := range node.Children {
				if child, ok := c.Value().(layout); ok {
					node.Children[i] = dbus.MakeVariant(prune(child, d-1))
				}
			}
		}
		return node
	}
	return a.revision.Load(), prune(root, depth), nil
}
func (a *linuxTray) GetGroupProperties(ids []int32, names []string) ([]itemProperties, *dbus.Error) {
	result := []itemProperties{}
	root := a.tree()
	if len(ids) == 0 {
		var walk func(layout)
		walk = func(n layout) {
			ids = append(ids, n.ID)
			for _, v := range n.Children {
				if child, ok := v.Value().(layout); ok {
					walk(child)
				}
			}
		}
		walk(root)
	}
	for _, id := range ids {
		if node, ok := findLayout(root, id); ok {
			result = append(result, itemProperties{id, selectedProperties(node.Properties, names)})
		}
	}
	return result, nil
}

func selectedProperties(all map[string]dbus.Variant, names []string) map[string]dbus.Variant {
	if len(names) == 0 {
		return all
	}
	result := map[string]dbus.Variant{}
	for _, name := range names {
		if v, ok := all[name]; ok {
			result[name] = v
		}
	}
	return result
}
func (a *linuxTray) GetProperty(id int32, name string) (dbus.Variant, *dbus.Error) {
	if node, ok := findLayout(a.tree(), id); ok {
		if value, yes := node.Properties[name]; yes {
			return value, nil
		}
	}
	return dbus.Variant{}, dbus.MakeFailedError(errors.New("unknown menu property"))
}
func (a *linuxTray) Event(id int32, event string, data dbus.Variant, timestamp uint32) *dbus.Error {
	if event != "clicked" {
		return nil
	}
	if id == 7 {
		if a.controller.CanQuit() {
			select {
			case a.quit <- struct{}{}:
			default:
			}
		}
		return nil
	}
	actions := map[int32]string{2: "refresh", 3: "start", 4: "stop", 5: "restart", 6: "logs"}
	if action, ok := actions[id]; ok {
		a.controller.Select(action)
	}
	return nil
}
func (a *linuxTray) EventGroup(events []menuEvent) ([]int32, *dbus.Error) {
	for _, e := range events {
		a.Event(e.ID, e.Event, e.Data, e.Timestamp)
	}
	return []int32{}, nil
}
func (a *linuxTray) AboutToShow(id int32) (bool, *dbus.Error) { return false, nil }
func (a *linuxTray) AboutToShowGroup(ids []int32) ([]int32, []int32, *dbus.Error) {
	return []int32{}, []int32{}, nil
}
func (a *linuxTray) Activate(x, y int32) *dbus.Error                    { return nil }
func (a *linuxTray) SecondaryActivate(x, y int32) *dbus.Error           { return nil }
func (a *linuxTray) ContextMenu(x, y int32) *dbus.Error                 { return nil }
func (a *linuxTray) Scroll(delta int32, orientation string) *dbus.Error { return nil }
