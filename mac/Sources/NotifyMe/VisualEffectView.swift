import SwiftUI
import AppKit

/// Bridges NSVisualEffectView so SwiftUI content can sit on a real macOS
/// vibrancy/blur background, which SwiftUI has no native equivalent for.
///
/// `.behindWindow` blending blurs through to whatever's on the desktop
/// behind the window — the actual "translucent widget" look — as opposed
/// to `.withinWindow`, which only blurs this window's own content and
/// looks opaque against the desktop. Getting `.behindWindow` right on a
/// *resizable* window (as opposed to the original fixed-size widget)
/// needs one extra setting: by default AppKit optimizes a live resize by
/// stretching the view's last-rendered bitmap instead of re-rendering,
/// which is exactly what showed up as tearing/stale-desktop artifacts
/// while dragging the window's edge. `layerContentsRedrawPolicy =
/// .duringViewResize` turns that optimization off for this view, so it
/// re-composites continuously through the resize instead of stretching a
/// stale frame.
struct VisualEffectView: NSViewRepresentable {
    var material: NSVisualEffectView.Material = .hudWindow
    var blendingMode: NSVisualEffectView.BlendingMode = .behindWindow

    func makeNSView(context: Context) -> NSVisualEffectView {
        let view = NSVisualEffectView()
        view.material = material
        view.blendingMode = blendingMode
        view.state = .active
        view.wantsLayer = true
        view.layerContentsRedrawPolicy = .duringViewResize
        return view
    }

    func updateNSView(_ nsView: NSVisualEffectView, context: Context) {
        nsView.material = material
        nsView.blendingMode = blendingMode
    }
}

/// Makes the hosting NSWindow's chrome transparent so `.behindWindow`
/// vibrancy above actually shows the desktop through it, without
/// otherwise fighting SwiftUI's own window management (still resizable,
/// movable, closable via the normal traffic lights from
/// `.windowStyle(.hiddenTitleBar)` in NotifyMeApp.swift). Attached as an
/// invisible background view so it can reach `NSView.window` once SwiftUI
/// has created it.
struct WindowConfigurator: NSViewRepresentable {
    func makeCoordinator() -> Coordinator { Coordinator() }

    func makeNSView(context: Context) -> NSView {
        let view = NSView()
        DispatchQueue.main.async {
            guard let window = view.window else { return }
            window.isOpaque = false
            window.backgroundColor = .clear
            window.hasShadow = true
            // SwiftUI's intrinsic content size makes AppKit's default zoom
            // stop around the ideal 960×640 layout. Use the screen's usable
            // area as the standard frame so a title-bar double-click behaves
            // like the expected near-maximize toggle.
            window.delegate = context.coordinator
        }
        return view
    }

    func updateNSView(_ nsView: NSView, context: Context) {}

    final class Coordinator: NSObject, NSWindowDelegate {
        func windowWillUseStandardFrame(_ window: NSWindow, defaultFrame: NSRect) -> NSRect {
            window.screen?.visibleFrame ?? defaultFrame
        }
    }
}
