import AppKit
import Foundation

struct Snapshot: Decodable {
    var installed: Bool
    var proxy: Bool
    var healthy: Bool
    var details: String
    var label: String {
        if !installed { return "Middleware: sin instalar" }
        if !proxy { return "Middleware: sin conexión" }
        return healthy ? "Middleware: disponible" : "Middleware: revisar conexiones"
    }
}

struct View: Decodable {
    var snapshot: Snapshot
    var busy: Bool
    var controlling: Bool
    var activity: String
    var error: String
    var errorID: UInt64
}

final class TrayDelegate: NSObject, NSApplicationDelegate {
    private var item: NSStatusItem!
    private var header: NSMenuItem!
    private var details: NSMenuItem!
    private var controls: [String: NSMenuItem] = [:]
    private var view: View?
    private var shownError: UInt64 = 0

    func applicationDidFinishLaunching(_ notification: Notification) {
        item = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
        if let button = item.button {
            let image = NSImage(systemSymbolName: "key.fill", accessibilityDescription: "KeePassXC Agent")
            image?.isTemplate = true
            button.image = image
            button.toolTip = "KeePassXC Agent — Comprobando conexiones…"
            button.setAccessibilityLabel("KeePassXC Agent")
        }
        let menu = NSMenu()
        menu.autoenablesItems = false
        header = NSMenuItem(title: "Comprobando conexiones…", action: nil, keyEquivalent: "")
        header.isEnabled = false
        menu.addItem(header)
        details = NSMenuItem(title: "Ver estado…", action: #selector(showDetails), keyEquivalent: "")
        details.target = self
        details.isEnabled = false
        menu.addItem(details)
        add("refresh", "Actualizar estado", to: menu)
        menu.addItem(.separator())
        add("start", "Iniciar middleware", to: menu)
        add("stop", "Detener middleware", to: menu)
        add("restart", "Reiniciar middleware", to: menu)
        menu.addItem(.separator())
        add("logs", "Abrir logs", to: menu)
        menu.addItem(.separator())
        add("quit", "Salir de la barra (SSH sigue activo)", to: menu)
        item.menu = menu
        if let view = view { apply(view) }
    }

    private func add(_ action: String, _ label: String, to menu: NSMenu) {
        let entry = NSMenuItem(title: label, action: #selector(selectAction(_:)), keyEquivalent: "")
        entry.target = self
        entry.representedObject = action
        entry.isEnabled = action == "quit"
        controls[action] = entry
        menu.addItem(entry)
    }

    func apply(_ next: View) {
        view = next
        guard item != nil else { return }
        let snapshot = next.snapshot
        let label = next.busy ? next.activity : snapshot.label
        header.title = label
        item.button?.toolTip = "KeePassXC Agent — " + label
        details.isEnabled = !snapshot.details.isEmpty
        controls["refresh"]?.isEnabled = !next.busy
        controls["start"]?.isEnabled = !next.busy && snapshot.installed && !snapshot.proxy
        controls["stop"]?.isEnabled = !next.busy && snapshot.installed && snapshot.proxy
        controls["restart"]?.isEnabled = !next.busy && snapshot.installed
        controls["logs"]?.isEnabled = !next.busy
        controls["quit"]?.isEnabled = !next.controlling
        if !next.error.isEmpty && next.errorID != shownError {
            shownError = next.errorID
            showAlert(next.error, warning: true)
        }
    }

    @objc private func showDetails() {
        guard let view = view else { return }
        showAlert(view.snapshot.details + "\n\nLa conectividad no indica si la bóveda está desbloqueada.", warning: false)
    }

    private func showAlert(_ text: String, warning: Bool) {
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.messageText = "KeePassXC Agent"
        alert.informativeText = text
        alert.alertStyle = warning ? .warning : .informational
        alert.addButton(withTitle: "Cerrar")
        alert.runModal()
    }

    @objc private func selectAction(_ entry: NSMenuItem) {
        guard let action = entry.representedObject as? String else { return }
        if let data = try? JSONSerialization.data(withJSONObject: ["action": action]) {
            FileHandle.standardOutput.write(data)
            FileHandle.standardOutput.write(Data([10]))
        }
    }
}

let app = NSApplication.shared
app.setActivationPolicy(.accessory)
let delegate = TrayDelegate()
app.delegate = delegate
Thread.detachNewThread {
    while let line = readLine() {
        guard let data = line.data(using: .utf8), let view = try? JSONDecoder().decode(View.self, from: data) else { continue }
        DispatchQueue.main.async { delegate.apply(view) }
    }
    // The Go controller owns this UI's lifetime. Losing its private pipe must
    // never leave a stale menu-bar process behind or terminate the SSH proxy.
    DispatchQueue.main.async { NSApp.terminate(nil) }
}
app.run()
