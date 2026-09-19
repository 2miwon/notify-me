import SwiftUI
import AppKit

extension Notification.Name {
    static let refreshPostings = Notification.Name("notifyMe.refreshPostings")
}

/// `swift run` launches this executable without an app bundle, so
/// LaunchServices registers it as background-only and no window appears
/// unless we explicitly opt into a regular, frontmost app. A real .app
/// bundle (e.g. built via Xcode) would not need this.
final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.regular)
        NSApp.activate(ignoringOtherApps: true)
    }
}

@main
struct NotifyMeApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) var appDelegate

    var body: some Scene {
        WindowGroup {
            ContentView()
        }
        .windowStyle(.hiddenTitleBar)
        .windowResizability(.contentMinSize)
        .defaultSize(width: 960, height: 640)
        .commands {
            CommandMenu("View") {
                Button("Refresh") {
                    NotificationCenter.default.post(name: .refreshPostings, object: nil)
                }
                .keyboardShortcut("r", modifiers: .command)
            }
        }
    }
}
