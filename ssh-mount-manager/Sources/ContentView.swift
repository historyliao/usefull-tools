import AppKit
import SwiftUI

enum SheetMode: Identifiable {
    case add
    case edit(MountSpec)

    var id: String {
        switch self {
        case .add: return "add"
        case .edit(let spec): return spec.id.uuidString
        }
    }
}

struct ContentView: View {
    @EnvironmentObject private var store: Store
    @EnvironmentObject private var controller: MountController
    @AppStorage(SettingsKeys.unmountOnQuit) private var unmountOnQuit = true

    @State private var selection: UUID?
    @State private var sheet: SheetMode?
    @State private var pendingDelete: MountSpec?

    var body: some View {
        VStack(spacing: 0) {
            toolbar
            Divider()
            table
            Divider()
            logPanel
            Divider()
            statusBar
        }
        .frame(minWidth: 900, minHeight: 580)
        .sheet(item: $sheet) { mode in
            switch mode {
            case .add:
                EditSheet(original: nil) { spec in selection = spec.id }
                    .environmentObject(store)
            case .edit(let spec):
                EditSheet(original: spec) { saved in
                    selection = saved.id
                    controller.handleSaved(saved, previous: spec)
                }
                    .environmentObject(store)
            }
        }
        .onReceive(NotificationCenter.default.publisher(for: .addMount)) { _ in
            sheet = .add
        }
        .alert("删除挂载", isPresented: deleteBinding) {
            Button("删除", role: .destructive) {
                if let spec = pendingDelete { controller.remove(spec) }
                pendingDelete = nil
            }
            Button("取消", role: .cancel) { pendingDelete = nil }
        } message: {
            Text("将先卸载「\(pendingDelete?.name ?? "")」，再删除该定义。远端文件不受影响。")
        }
    }

    private var selectedSpec: MountSpec? {
        guard let selection else { return nil }
        return store.specs.first { $0.id == selection }
    }

    private var deleteBinding: Binding<Bool> {
        Binding(get: { pendingDelete != nil }, set: { if !$0 { pendingDelete = nil } })
    }

    private var toolbar: some View {
        HStack(spacing: 10) {
            Button {
                sheet = .add
            } label: {
                Label("添加", systemImage: "plus")
            }

            Button {
                guard let spec = selectedSpec else { return }
                controller.toggle(spec)
            } label: {
                Label(isMountedSelected ? "卸载" : "挂载",
                      systemImage: isMountedSelected ? "eject" : "bolt.horizontal.circle")
            }
            .disabled(selectedSpec == nil)

            Button {
                guard let spec = selectedSpec else { return }
                sheet = .edit(spec)
            } label: {
                Label("编辑", systemImage: "square.and.pencil")
            }
            .disabled(selectedSpec == nil)

            Button {
                pendingDelete = selectedSpec
            } label: {
                Label("删除", systemImage: "trash")
            }
            .disabled(selectedSpec == nil)

            Divider().frame(height: 18)

            Button {
                guard let spec = selectedSpec else { return }
                controller.openInFinder(spec)
            } label: {
                Label("在 Finder 打开", systemImage: "folder")
            }
            .disabled(selectedSpec == nil || !isMountedSelected)

            Button {
                guard let spec = selectedSpec else { return }
                controller.revealLog(spec)
            } label: {
                Label("日志文件", systemImage: "doc.text.magnifyingglass")
            }
            .disabled(selectedSpec == nil)

            Button {
                guard let spec = selectedSpec else { return }
                controller.forceUnmount(spec)
            } label: {
                Label("强制清理", systemImage: "exclamationmark.triangle")
            }
            .disabled(selectedSpec == nil || !isMountedSelected)
            .help("卷卡死时使用：杀掉对应 sshfs 进程并强制卸载")

            Spacer()

            Toggle("退出时卸载全部", isOn: $unmountOnQuit)
                .toggleStyle(.checkbox)

            Button {
                controller.refresh()
                if let spec = selectedSpec { controller.loadLog(spec) }
            } label: {
                Label("刷新", systemImage: "arrow.clockwise")
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }

    private var isMountedSelected: Bool {
        guard let spec = selectedSpec else { return false }
        return controller.status(of: spec).isMounted
    }

    private var table: some View {
        Table(store.specs, selection: $selection) {
            TableColumn("名称") { spec in
                Text(spec.name).fontWeight(.medium)
            }
            .width(min: 120, ideal: 160)

            TableColumn("SSH 目标") { spec in
                Text(spec.displayTarget)
            }
            .width(min: 140, ideal: 200)

            TableColumn("远端路径") { spec in
                Text(spec.remotePath)
            }
            .width(min: 160, ideal: 240)

            TableColumn("本地挂载点") { spec in
                Text(spec.expandedMountPoint)
            }
            .width(min: 160, ideal: 240)

            TableColumn("状态") { spec in
                statusView(spec)
            }
            .width(min: 120, ideal: 150)

            TableColumn("操作") { spec in
                Button(controller.status(of: spec).isMounted ? "卸载" : "挂载") {
                    controller.toggle(spec)
                }
                .buttonStyle(.borderless)
                .foregroundStyle(.tint)
            }
            .width(min: 70, ideal: 80)
        }
    }

    private func statusView(_ spec: MountSpec) -> some View {
        let status = controller.status(of: spec)
        let color: Color
        switch status {
        case .mounted: color = .green
        case .mounting: color = .orange
        case .failed: color = .red
        case .unmounted: color = .secondary
        }
        return HStack(spacing: 6) {
            Circle().fill(color).frame(width: 8, height: 8)
            Text(status.label).lineLimit(1).truncationMode(.middle)
        }
    }

    private var logPanel: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text("日志").font(.caption).foregroundStyle(.secondary)
                if let spec = selectedSpec {
                    Text(spec.name).font(.caption).fontWeight(.medium)
                }
                Spacer()
                if let spec = selectedSpec {
                    Button("打开日志目录") { controller.revealLog(spec) }
                        .buttonStyle(.link)
                        .font(.caption)
                }
            }
            ScrollView {
                VStack(alignment: .leading, spacing: 2) {
                    let lines = selection.flatMap { controller.logs[$0] } ?? []
                    if lines.isEmpty {
                        Text("选中一条挂载后，这里显示 sshfs 最近输出")
                            .font(.system(.caption, design: .monospaced))
                            .foregroundStyle(.secondary)
                    } else {
                        ForEach(Array(lines.enumerated()), id: \.offset) { _, line in
                            Text(line.isEmpty ? " " : line)
                                .font(.system(.caption, design: .monospaced))
                                .frame(maxWidth: .infinity, alignment: .leading)
                        }
                    }
                }
                .padding(6)
            }
            .frame(height: 130)
            .background(Color(nsColor: .textBackgroundColor))
            .clipShape(RoundedRectangle(cornerRadius: 6))
        }
        .padding(10)
    }

    private var statusBar: some View {
        HStack {
            Text(controller.message.isEmpty ? "就绪" : controller.message)
                .font(.caption)
                .foregroundStyle(.secondary)
            Spacer()
            Text("共 \(store.specs.count) 条，已挂载 \(store.specs.filter { controller.status(of: $0).isMounted }.count) 条")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 6)
    }
}

extension Notification.Name {
    static let addMount = Notification.Name("addMount")
}

struct MenuBarView: View {
    @EnvironmentObject private var store: Store
    @EnvironmentObject private var controller: MountController
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        if store.specs.isEmpty {
            Text("还没有挂载定义")
        }
        ForEach(store.specs) { spec in
            Button("\(controller.status(of: spec).isMounted ? "卸载" : "挂载") \(spec.name)") {
                controller.toggle(spec)
            }
        }
        Divider()
        Button("打开主窗口") {
            openWindow(id: "main")
            NSApp.activate(ignoringOtherApps: true)
        }
        Button("刷新状态") { controller.refresh() }
        Divider()
        Button("退出") { NSApp.terminate(nil) }
    }
}
