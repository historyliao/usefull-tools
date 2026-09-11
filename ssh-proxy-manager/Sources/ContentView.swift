import SwiftUI

struct ContentView: View {
    @EnvironmentObject private var state: AppState
    @State private var pendingDelete: ProxyItem?
    @State private var confirmingDelete = false

    private var editorPresented: Binding<Bool> {
        Binding(
            get: { state.editing != nil },
            set: { if !$0 { state.editing = nil } }
        )
    }

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            HSplitView {
                sidebar
                    .frame(minWidth: 340, idealWidth: 400)
                detail
                    .frame(minWidth: 460)
            }
        }
        .frame(minWidth: 960, minHeight: 600)
        .sheet(isPresented: editorPresented) {
            if let definition = state.editing {
                EditSheet(
                    definition: definition,
                    isNew: state.isCreating,
                    onSave: { state.save($0, isNew: state.isCreating) },
                    onCancel: { state.editing = nil }
                )
            }
        }
        .sheet(isPresented: $state.managingTargets) {
            TargetsSheet()
                .environmentObject(state)
        }
        .alert("删除代理", isPresented: $confirmingDelete, presenting: pendingDelete) { item in
            Button("删除", role: .destructive) {
                state.remove(item.definition.name)
                pendingDelete = nil
            }
            Button("取消", role: .cancel) { pendingDelete = nil }
        } message: { item in
            Text("将先停止「\(item.definition.name)」再删除定义，日志文件会保留。")
        }
        .onReceive(NotificationCenter.default.publisher(for: .newProxy)) { _ in
            state.openEditor(for: nil)
        }
    }

    private var header: some View {
        HStack(spacing: 12) {
            Circle()
                .fill(state.kernelReady ? Color.green : Color.red)
                .frame(width: 9, height: 9)
            VStack(alignment: .leading, spacing: 2) {
                Text(state.kernelDescription)
                    .font(.system(size: 13, weight: .semibold))
                Text("运行中 \(state.runningCount) / \(state.items.count) · \(Kernel.shared.stateDir.path)")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
            }
            Spacer()
            if !state.status.isEmpty {
                Text(state.status)
                    .font(.system(size: 11))
                    .foregroundStyle(state.statusIsError ? Color.red : Color.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .frame(maxWidth: 320, alignment: .trailing)
            }
            if !state.kernelReady {
                Button("启动内核") { state.bootKernel() }
            }
            Button("全部启动") { state.startAll() }
                .disabled(!state.kernelReady || state.items.isEmpty)
            Button("全部停止") { state.stopAll() }
                .disabled(!state.kernelReady || state.runningCount == 0)
            Button {
                state.managingTargets = true
            } label: {
                Label("SSH 目标", systemImage: "server.rack")
            }
            .disabled(!state.kernelReady)
            Button {
                state.openEditor(for: nil)
            } label: {
                Label("新建代理", systemImage: "plus")
            }
            .disabled(!state.kernelReady)
            .keyboardShortcut("n", modifiers: .command)
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 10)
    }

    private var sidebar: some View {
        List(selection: $state.selection) {
            if !state.reverseItems.isEmpty {
                Section("反向代理 · 远端端口 → 本机服务") {
                    ForEach(state.reverseItems) { item in
                        ProxyRow(item: item)
                            .tag(item.definition.name)
                    }
                }
            }
            if !state.forwardItems.isEmpty {
                Section("正向代理 · 本地端口 → 远端服务") {
                    ForEach(state.forwardItems) { item in
                        ProxyRow(item: item)
                            .tag(item.definition.name)
                    }
                }
            }
            if state.items.isEmpty {
                Section {
                    VStack(alignment: .leading, spacing: 6) {
                        Text("还没有代理")
                            .font(.headline)
                        Text("点右上角「新建代理」，反向代理用于把远端端口映射到本机服务（例如远端 9223 → 本机 9222）。")
                            .font(.system(size: 11))
                            .foregroundStyle(.secondary)
                    }
                    .padding(.vertical, 6)
                }
            }
        }
        .listStyle(.sidebar)
    }

    @ViewBuilder
    private var detail: some View {
        if let item = state.selectedItem {
            ProxyDetail(
                item: item,
                logs: state.logs,
                busy: state.busy.contains(item.definition.name),
                actions: DetailActions(
                    start: { state.start(item.definition.name) },
                    stop: { state.stop(item.definition.name) },
                    restart: { state.restart(item.definition.name) },
                    edit: { state.openEditor(for: item) },
                    remove: {
                        pendingDelete = item
                        confirmingDelete = true
                    }
                )
            )
        } else {
            VStack(spacing: 8) {
                Image(systemName: "arrow.left.arrow.right.circle")
                    .font(.system(size: 40))
                    .foregroundStyle(.tertiary)
                Text("选择左侧的代理查看详情与日志")
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }
}

struct DetailActions {
    var start: () -> Void
    var stop: () -> Void
    var restart: () -> Void
    var edit: () -> Void
    var remove: () -> Void
}

struct ProxyRow: View {
    @EnvironmentObject private var state: AppState
    let item: ProxyItem

    var body: some View {
        HStack(spacing: 10) {
            Circle()
                .fill(statusColor)
                .frame(width: 8, height: 8)
            VStack(alignment: .leading, spacing: 2) {
                Text(item.definition.name)
                    .font(.system(size: 13, weight: .semibold))
                Text("\(state.displayTarget(item.definition)) · \(item.definition.forwardSummary)")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
            Spacer(minLength: 8)
            Text(item.runtime.label)
                .font(.system(size: 11))
                .foregroundStyle(statusColor)
            Button {
                item.runtime.isActive
                    ? state.stop(item.definition.name)
                    : state.start(item.definition.name)
            } label: {
                Image(systemName: item.runtime.isActive ? "stop.circle" : "play.circle")
                    .font(.system(size: 15))
            }
            .buttonStyle(.borderless)
            .disabled(state.busy.contains(item.definition.name) || item.runtime.isTransitioning)
            .help(item.runtime.isActive ? "停止" : "启动")
        }
        .padding(.vertical, 3)
    }

    private var statusColor: Color {
        switch item.runtime.state {
        case "running": return .green
        case "starting", "restarting": return .orange
        case "stopping": return .yellow
        case "failed": return .red
        default: return .secondary
        }
    }
}

struct ProxyDetail: View {
    @EnvironmentObject private var state: AppState

    let item: ProxyItem
    let logs: [String]
    let busy: Bool
    let actions: DetailActions

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            VStack(alignment: .leading, spacing: 8) {
                HStack(spacing: 8) {
                    Text(item.definition.name)
                        .font(.system(size: 17, weight: .bold))
                    Text(item.definition.direction.label)
                        .font(.system(size: 11, weight: .semibold))
                        .padding(.horizontal, 7)
                        .padding(.vertical, 2)
                        .background(item.definition.direction == .reverse ? Color.blue.opacity(0.15) : Color.green.opacity(0.15))
                        .foregroundStyle(item.definition.direction == .reverse ? Color.blue : Color.green)
                        .clipShape(Capsule())
                    Text(item.runtime.label)
                        .font(.system(size: 11, weight: .semibold))
                        .foregroundStyle(stateColor)
                    Spacer()
                    if busy {
                        ProgressView().controlSize(.small)
                    }
                }
                Text(item.definition.direction.explanation)
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)

                Grid(alignment: .leading, horizontalSpacing: 14, verticalSpacing: 5) {
                    row("SSH 目标", state.displayTarget(item.definition))
                    let identity = state.displayIdentity(item.definition)
                    if !identity.isEmpty {
                        row("私钥", identity)
                    }
                    row("转发", item.definition.forwards.map(\.display).joined(separator: "\n"))
                    row("重启策略", item.definition.restartSummary)
                    row("自动启动", item.definition.autostart ? "是" : "否")
                    row("PID", item.runtime.pid > 0 ? "\(item.runtime.pid)" : "-")
                    row("运行时长", item.runtime.uptimeText)
                    row("重启次数", "\(item.runtime.restarts)")
                    if let error = item.runtime.lastError, !error.isEmpty {
                        row("最近错误", error, isError: true)
                    }
                }
                .font(.system(size: 12))

                HStack(spacing: 8) {
                    if item.runtime.isActive {
                        Button("停止") { actions.stop() }
                            .disabled(busy || item.runtime.isTransitioning)
                    } else {
                        Button("启动") { actions.start() }
                            .disabled(busy || item.runtime.isTransitioning)
                    }
                    Button("重启") { actions.restart() }
                    Button("编辑") { actions.edit() }
                    Spacer()
                    Button("删除") { actions.remove() }
                        .foregroundStyle(.red)
                }
                .disabled(busy)
            }
            .padding(16)

            Divider()

            VStack(alignment: .leading, spacing: 6) {
                HStack {
                    Text("日志")
                        .font(.system(size: 12, weight: .semibold))
                    Text("~/.ssh-proxy-manager/logs/\(item.definition.name).log")
                        .font(.system(size: 10))
                        .foregroundStyle(.secondary)
                    Spacer()
                }
                LogPane(lines: logs)
            }
            .padding(12)
        }
    }

    private var stateColor: Color {
        switch item.runtime.state {
        case "running": return .green
        case "starting", "restarting": return .orange
        case "stopping": return .yellow
        case "failed": return .red
        default: return .secondary
        }
    }

    @ViewBuilder
    private func row(_ label: String, _ value: String, isError: Bool = false) -> some View {
        GridRow(alignment: .top) {
            Text(label)
                .foregroundStyle(.secondary)
                .gridColumnAlignment(.leading)
            Text(value)
                .foregroundStyle(isError ? Color.red : Color.primary)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
        }
    }
}

struct LogPane: View {
    let lines: [String]

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                VStack(alignment: .leading, spacing: 1) {
                    if lines.isEmpty {
                        Text("暂无日志")
                            .foregroundStyle(.secondary)
                            .font(.system(size: 11, design: .monospaced))
                    }
                    ForEach(Array(lines.enumerated()), id: \.offset) { index, line in
                        Text(line)
                            .font(.system(size: 11, design: .monospaced))
                            .textSelection(.enabled)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .id(index)
                    }
                }
                .padding(8)
            }
            .background(Color(nsColor: .textBackgroundColor).opacity(0.5))
            .clipShape(RoundedRectangle(cornerRadius: 6))
            .onChange(of: lines.count) { _, _ in
                if let last = lines.indices.last {
                    withAnimation(.linear(duration: 0.1)) {
                        proxy.scrollTo(last, anchor: .bottom)
                    }
                }
            }
        }
    }
}

struct MenuBarView: View {
    @EnvironmentObject private var state: AppState

    var body: some View {
        Text(state.kernelDescription)
        Divider()
        ForEach(state.items) { item in
            Button {
                item.runtime.isActive
                    ? state.stop(item.definition.name)
                    : state.start(item.definition.name)
            } label: {
                Text("\(item.runtime.isActive ? "停止" : "启动") \(item.definition.name)")
            }
        }
        Divider()
        Button("显示窗口") {
            NSApp.activate(ignoringOtherApps: true)
            NSApp.windows.first?.makeKeyAndOrderFront(nil)
        }
        Button("退出（会关闭所有隧道）") {
            NSApp.terminate(nil)
        }
    }
}
