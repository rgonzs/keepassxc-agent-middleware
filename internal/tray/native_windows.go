package tray

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32           = windows.NewLazySystemDLL("user32.dll")
	shell32          = windows.NewLazySystemDLL("shell32.dll")
	registerClass    = user32.NewProc("RegisterClassExW")
	unregisterClass  = user32.NewProc("UnregisterClassW")
	createWindow     = user32.NewProc("CreateWindowExW")
	defWindow        = user32.NewProc("DefWindowProcW")
	destroyWindow    = user32.NewProc("DestroyWindow")
	getMessage       = user32.NewProc("GetMessageW")
	translateMessage = user32.NewProc("TranslateMessage")
	dispatchMessage  = user32.NewProc("DispatchMessageW")
	postMessage      = user32.NewProc("PostMessageW")
	postQuit         = user32.NewProc("PostQuitMessage")
	setTimer         = user32.NewProc("SetTimer")
	killTimer        = user32.NewProc("KillTimer")
	registerMessage  = user32.NewProc("RegisterWindowMessageW")
	notifyIcon       = shell32.NewProc("Shell_NotifyIconW")
	createIcon       = user32.NewProc("CreateIconFromResourceEx")
	destroyIcon      = user32.NewProc("DestroyIcon")
	createMenu       = user32.NewProc("CreatePopupMenu")
	destroyMenu      = user32.NewProc("DestroyMenu")
	appendMenu       = user32.NewProc("AppendMenuW")
	trackMenu        = user32.NewProc("TrackPopupMenu")
	getCursor        = user32.NewProc("GetCursorPos")
	setForeground    = user32.NewProc("SetForegroundWindow")
	messageBox       = user32.NewProc("MessageBoxW")
)

const (
	wmTray     = 0x8001
	wmResult   = 0x8002
	cmdDetails = 1
	cmdRefresh = 2
	cmdStart   = 3
	cmdStop    = 4
	cmdRestart = 5
	cmdLogs    = 6
	cmdExit    = 7
)

type point struct{ X, Y int32 }
type message struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Point          point
	Private        uint32
}
type windowClass struct {
	Size, Style                        uint32
	Callback                           uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	MenuName, ClassName                *uint16
	SmallIcon                          uintptr
}
type iconData struct {
	Size                uint32
	Window              uintptr
	ID, Flags, Callback uint32
	Icon                uintptr
	Tip                 [128]uint16
	State, StateMask    uint32
	Info                [256]uint16
	Version             uint32
	InfoTitle           [64]uint16
	InfoFlags           uint32
	GUID                windows.GUID
	BalloonIcon         uintptr
}
type result struct {
	Snapshot Snapshot
	Err      error
}
type application struct {
	handlers       Handlers
	window         uintptr
	data           iconData
	snapshot       Snapshot
	busy           bool
	activity       string
	controlling    bool
	results        chan result
	taskbarCreated uint32
}

func utf16(s string) *uint16 { p, _ := windows.UTF16PtrFromString(s); return p }

// Run owns one UI thread and a per-user/session mutex. The proxy is never a
// child of this process; exiting the tray cannot terminate the SSH listener.
func Run(handlers Handlers) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	token, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	mutex, err := windows.CreateMutex(nil, false, utf16(`Local\KeePassXCAgentTray-`+token.User.Sid.String()))
	if mutex != 0 {
		defer windows.CloseHandle(mutex)
	}
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return nil
	}
	if err != nil {
		return err
	}
	var instance windows.Handle
	if err := windows.GetModuleHandleEx(0, nil, &instance); err != nil {
		return err
	}
	a := &application{handlers: handlers, results: make(chan result, 1)}
	taskbar, _, _ := registerMessage.Call(uintptr(unsafe.Pointer(utf16("TaskbarCreated"))))
	a.taskbarCreated = uint32(taskbar)
	class := windowClass{Size: uint32(unsafe.Sizeof(windowClass{})), Instance: uintptr(instance), ClassName: utf16("KeePassXCAgentTrayWindow")}
	class.Callback = syscall.NewCallback(a.windowProc)
	if ok, _, err := registerClass.Call(uintptr(unsafe.Pointer(&class))); ok == 0 {
		return fmt.Errorf("register tray window: %w", err)
	}
	defer unregisterClass.Call(uintptr(unsafe.Pointer(class.ClassName)), uintptr(instance))
	hwnd, _, err := createWindow.Call(0, uintptr(unsafe.Pointer(class.ClassName)), uintptr(unsafe.Pointer(utf16("KeePassXC Agent"))), 0, 0, 0, 0, 0, 0, 0, uintptr(instance), 0)
	if hwnd == 0 {
		return fmt.Errorf("create tray window: %w", err)
	}
	a.window = hwnd
	defer destroyWindow.Call(hwnd)
	a.data = iconData{
		Size: uint32(unsafe.Sizeof(iconData{})), Window: hwnd, ID: 1, Callback: wmTray,
	}
	defer func() {
		notifyIcon.Call(2, uintptr(unsafe.Pointer(&a.data))) // NIM_DELETE
		if a.data.Icon != 0 {
			destroyIcon.Call(a.data.Icon)
		}
	}()
	if err := a.updateIcon(true); err != nil {
		return err
	}
	if timer, _, err := setTimer.Call(hwnd, 1, 15000, 0); timer == 0 {
		return fmt.Errorf("start status timer: %w", err)
	}
	defer killTimer.Call(hwnd, 1)
	a.refresh("")
	var msg message
	for {
		value, _, err := getMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(value) == -1 {
			return fmt.Errorf("tray message loop: %w", err)
		}
		if value == 0 {
			return nil
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		dispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func (a *application) updateIcon(add bool) error {
	bits := iconDIB(a.snapshot)
	icon, _, err := createIcon.Call(uintptr(unsafe.Pointer(&bits[0])), uintptr(len(bits)), 1, 0x30000, 32, 32, 0)
	if icon == 0 {
		return fmt.Errorf("create tray icon: %w", err)
	}
	old := a.data.Icon
	a.data.Icon = icon
	// Use HWND/ID: a fixed GUID would bind the icon to a binary path, which
	// changes on every content-addressed upgrade of this unsigned application.
	a.data.Flags = 1 | 2 | 4 | 128 // MESSAGE | ICON | TIP | SHOWTIP
	clear(a.data.Tip[:])
	label := a.snapshot.Label()
	if a.snapshot.Details == "" {
		label = "Comprobando conexiones…"
	}
	copy(a.data.Tip[:], syscall.StringToUTF16("KeePassXC Agent — "+label))
	op := uintptr(1) // NIM_MODIFY
	if add {
		op = 0
	}
	ok, _, err := notifyIcon.Call(op, uintptr(unsafe.Pointer(&a.data)))
	if old != 0 {
		destroyIcon.Call(old)
	}
	if ok == 0 {
		return fmt.Errorf("show tray icon: %w", err)
	}
	if add {
		a.data.Version = 4
		notifyIcon.Call(4, uintptr(unsafe.Pointer(&a.data))) // NIM_SETVERSION
	}
	return nil
}

func (a *application) refresh(action string) {
	if a.busy {
		return
	}
	a.busy = true
	a.activity = "Comprobando conexiones…"
	a.controlling = action == "start" || action == "stop" || action == "restart"
	switch action {
	case "start":
		a.activity = "Iniciando middleware…"
	case "stop":
		a.activity = "Deteniendo middleware…"
	case "restart":
		a.activity = "Reiniciando middleware…"
	case "logs":
		a.activity = "Abriendo logs…"
	}
	go func() {
		var err error
		if action == "logs" {
			err = a.handlers.OpenLogs()
		} else if action != "" {
			err = a.handlers.Control(action)
		}
		a.results <- result{Snapshot: a.handlers.Snapshot(), Err: err}
		postMessage.Call(a.window, wmResult, 0, 0)
	}()
}

func (a *application) windowProc(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
	switch msg {
	case wmTray:
		switch uint16(lparam) {
		case 0x7b, 0x400, 0x401: // WM_CONTEXTMENU, NIN_SELECT, NIN_KEYSELECT
			a.menu()
		}
		return 0
	case wmResult:
		var r result
		select {
		case r = <-a.results:
		default:
			return 0
		}
		a.snapshot, a.busy = r.Snapshot, false
		a.controlling = false
		if err := a.updateIcon(false); err != nil {
			a.show(err.Error(), true)
		}
		if r.Err != nil {
			a.show("No se pudo completar la acción.\n\n"+r.Err.Error(), true)
		}
		return 0
	case 0x113: // WM_TIMER
		a.refresh("")
		return 0
	case 0x11: // WM_QUERYENDSESSION: allow logoff/shutdown
		return 1
	case 0x16: // WM_ENDSESSION
		if wparam != 0 {
			destroyWindow.Call(hwnd)
		}
		return 0
	case 0x10: // WM_CLOSE
		destroyWindow.Call(hwnd)
		return 0
	case 0x2: // WM_DESTROY
		postQuit.Call(0)
		return 0
	}
	if a.taskbarCreated != 0 && msg == a.taskbarCreated {
		_ = a.updateIcon(true) // restore after Explorer restarts
		return 0
	}
	value, _, _ := defWindow.Call(hwnd, uintptr(msg), wparam, lparam)
	return value
}

func (a *application) menu() {
	menu, _, _ := createMenu.Call()
	if menu == 0 {
		return
	}
	defer destroyMenu.Call(menu)
	item := func(id uintptr, label string, enabled bool) {
		flags := uintptr(0)
		if !enabled {
			flags = 1
		} // MF_GRAYED
		appendMenu.Call(menu, flags, id, uintptr(unsafe.Pointer(utf16(label))))
	}
	separator := func() { appendMenu.Call(menu, 0x800, 0, 0) }
	label := a.snapshot.Label()
	if a.busy {
		label = a.activity
	}
	item(0, label, false)
	item(cmdDetails, "Ver &estado…", a.snapshot.Details != "")
	item(cmdRefresh, "&Actualizar estado", !a.busy)
	separator()
	item(cmdStart, "&Iniciar middleware", !a.busy && a.snapshot.Installed && !a.snapshot.Proxy)
	item(cmdStop, "&Detener middleware", !a.busy && a.snapshot.Installed && a.snapshot.Proxy)
	item(cmdRestart, "&Reiniciar middleware", !a.busy && a.snapshot.Installed)
	separator()
	item(cmdLogs, "Abrir &logs", !a.busy)
	separator()
	item(cmdExit, "&Salir de la bandeja (SSH sigue activo)", !a.controlling)
	var cursor point
	getCursor.Call(uintptr(unsafe.Pointer(&cursor)))
	setForeground.Call(a.window)
	selected, _, _ := trackMenu.Call(menu, 0x100|0x2, uintptr(cursor.X), uintptr(cursor.Y), 0, a.window, 0) // RETURNCMD | RIGHTBUTTON
	postMessage.Call(a.window, 0, 0, 0)
	notifyIcon.Call(3, uintptr(unsafe.Pointer(&a.data))) // NIM_SETFOCUS
	switch selected {
	case cmdDetails:
		a.show(a.snapshot.Details+"\n\nLa conectividad no indica si la bóveda está desbloqueada.", false)
	case cmdRefresh:
		a.refresh("")
	case cmdStart:
		a.refresh("start")
	case cmdStop:
		a.refresh("stop")
	case cmdRestart:
		a.refresh("restart")
	case cmdLogs:
		a.refresh("logs")
	case cmdExit:
		destroyWindow.Call(a.window)
	}
}

func (a *application) show(text string, failure bool) {
	flags := uintptr(0x40)
	if failure {
		flags = 0x10
	}
	messageBox.Call(a.window, uintptr(unsafe.Pointer(utf16(text))), uintptr(unsafe.Pointer(utf16("KeePassXC Agent"))), flags)
}
