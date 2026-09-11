import AppKit
import SwiftUI

@main
struct MountManagerApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    @StateObject private var store = AppState.store
    @StateObject private var controller = AppState.controller

    init() {
        if CLI.isInvocation {
            CLI.run()
        }
    }

    var body: some Scene {
        WindowGroup(id: "main") {
            ContentView()
                .environmentObject(store)
                .environmentObject(controller)
        }
        .commands {
            CommandGroup(replacing: .newItem) {
                Button("添加挂载…") {
                    NotificationCenter.default.post(name: .addMount, object: nil)
                }
                .keyboardShortcut("n", modifiers: .command)
            }
        }

        MenuBarExtra("SSH 挂载", systemImage: "externaldrive.connected.to.line.below") {
            MenuBarView()
                .environmentObject(store)
                .environmentObject(controller)
        }
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        false
    }

    func applicationWillTerminate(_ notification: Notification) {
        AppState.controller.shutdown()
    }
}
