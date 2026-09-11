import SwiftUI

/// SSH 目标管理：单独建立目标，代理里可直接下拉复用。
struct TargetsSheet: View {
    @EnvironmentObject private var state: AppState
    @Environment(\.dismiss) private var dismiss

    @State private var editing: SSHTarget?
    @State private var isCreating = false
    @State private var pendingDelete: SSHTarget?

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 8) {
                Text("SSH 目标")
                    .font(.system(size: 15, weight: .bold))
                Text("定义一次，代理里可直接复用")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                Spacer()
                Button {
                    isCreating = true
                } label: {
                    Label("新建目标", systemImage: "plus")
                }
            }
            .padding(16)

            Divider()

            if state.targets.isEmpty {
                VStack(spacing: 6) {
                    Text("还没有 SSH 目标")
                        .foregroundStyle(.secondary)
                    Text("新建后，代理编辑页的「SSH 目标」下拉里就能直接选")
                        .font(.system(size: 11))
                        .foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                Table(state.targets) {
                    TableColumn("名称") { target in
                        Text(target.name).fontWeight(.medium)
                    }
                    .width(min: 120, ideal: 160)
                    TableColumn("SSH") { target in
                        Text(target.address)
                    }
                    .width(min: 180, ideal: 220)
                    TableColumn("私钥") { target in
                        Text(target.identity.isEmpty ? "-" : target.identity)
                    }
                    .width(min: 140, ideal: 200)
                    TableColumn("操作") { target in
                        HStack(spacing: 12) {
                            Button("编辑") { editing = target }
                                .buttonStyle(.link)
                            Button("删除") { pendingDelete = target }
                                .buttonStyle(.link)
                                .foregroundStyle(.red)
                        }
                    }
                    .width(min: 110, ideal: 130)
                }
            }

            Divider()
            HStack {
                Spacer()
                Button("关闭") { dismiss() }
                    .keyboardShortcut(.cancelAction)
            }
            .padding(12)
        }
        .frame(width: 760, height: 460)
        .sheet(isPresented: $isCreating) {
            TargetEditSheet(target: nil)
                .environmentObject(state)
        }
        .sheet(item: $editing) { target in
            TargetEditSheet(target: target)
                .environmentObject(state)
        }
        .alert("删除 SSH 目标", isPresented: deleteBinding) {
            Button("删除", role: .destructive) {
                if let target = pendingDelete { state.deleteTarget(target.name) }
                pendingDelete = nil
            }
            Button("取消", role: .cancel) { pendingDelete = nil }
        } message: {
            Text("仍被代理引用时会被拒绝，需要先把引用它的代理改成别的目标。")
        }
    }

    private var deleteBinding: Binding<Bool> {
        Binding(get: { pendingDelete != nil }, set: { if !$0 { pendingDelete = nil } })
    }
}

struct TargetEditSheet: View {
    @EnvironmentObject private var state: AppState
    @Environment(\.dismiss) private var dismiss

    let target: SSHTarget?

    @State private var name: String
    @State private var sshSpec: String
    @State private var identity: String
    @State private var extraArgs: String
    @State private var errorText: String?

    init(target: SSHTarget?) {
        self.target = target
        _name = State(initialValue: target?.name ?? "")
        _sshSpec = State(initialValue: target?.address ?? "")
        _identity = State(initialValue: target?.identity ?? "")
        _extraArgs = State(initialValue: target?.extraArgs.joined(separator: " ") ?? "")
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text(target == nil ? "新建 SSH 目标" : "编辑 \(target?.name ?? "")")
                .font(.system(size: 15, weight: .bold))

            field("名称", hint: "代理里下拉显示的名字，如 hhdev") {
                TextField("hhdev", text: $name)
            }
            field("SSH", hint: "user@host 或 user@host:port，也可用 ~/.ssh/config 别名") {
                TextField("lyy@host:12880", text: $sshSpec)
            }
            field("私钥", hint: "可留空，例如 ~/.ssh/id_rsa") {
                TextField("~/.ssh/id_rsa", text: $identity)
            }
            field("额外参数", hint: "透传给 ssh，空格分隔，可留空") {
                TextField("", text: $extraArgs)
            }

            if let errorText {
                Text("✗ \(errorText)")
                    .font(.system(size: 12))
                    .foregroundStyle(.red)
            }

            HStack {
                Spacer()
                Button("取消") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button(target == nil ? "创建" : "保存") { save() }
                    .keyboardShortcut(.defaultAction)
            }
        }
        .padding(18)
        .frame(width: 520)
    }

    private func field(_ title: String, hint: String, @ViewBuilder content: () -> some View) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            Text(title)
                .font(.system(size: 12, weight: .medium))
            content()
                .textFieldStyle(.roundedBorder)
            Text(hint)
                .font(.system(size: 10.5))
                .foregroundStyle(.secondary)
        }
    }

    private func save() {
        let trimmedName = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedName.isEmpty else {
            errorText = "名称不能为空"
            return
        }
        let allowed = CharacterSet(charactersIn: "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-")
        if trimmedName.unicodeScalars.contains(where: { !allowed.contains($0) }) {
            errorText = "名称只允许字母、数字、点、下划线和连字符"
            return
        }
        guard let parsed = Target.parse(sshSpec) else {
            errorText = "SSH 格式不对，应为 hhdev 或 user@host:port"
            return
        }
        let spec = SSHTarget(
            name: trimmedName,
            user: parsed.user,
            host: parsed.host,
            port: parsed.port,
            identity: identity.trimmingCharacters(in: .whitespacesAndNewlines),
            extraArgs: extraArgs.split(separator: " ").map(String.init).filter { !$0.isEmpty }
        )
        state.saveTarget(spec, isNew: target == nil)
        dismiss()
    }
}
