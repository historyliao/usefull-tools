import SwiftUI

struct EditSheet: View {
    private let original: MountSpec?
    private let onSaved: (MountSpec) -> Void

    @EnvironmentObject private var store: Store
    @Environment(\.dismiss) private var dismiss

    @State private var name: String
    @State private var target: String
    @State private var port: String
    @State private var remotePath: String
    @State private var mountPoint: String
    @State private var identity: String
    @State private var options: String
    @State private var error: String = ""

    init(original: MountSpec?, onSaved: @escaping (MountSpec) -> Void) {
        self.original = original
        self.onSaved = onSaved
        _name = State(initialValue: original?.name ?? "")
        _target = State(initialValue: original?.target ?? "")
        _port = State(initialValue: original?.port.map(String.init) ?? "")
        _remotePath = State(initialValue: original?.remotePath ?? "")
        _mountPoint = State(initialValue: original?.mountPoint ?? "~/mnt/")
        _identity = State(initialValue: original?.identity ?? "~/.ssh/id_rsa")
        _options = State(initialValue: original?.extraOptions.joined(separator: ", ") ?? "")
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(original == nil ? "添加挂载" : "编辑挂载")
                .font(.headline)

            Grid(alignment: .leading, horizontalSpacing: 10, verticalSpacing: 8) {
                row("名称", binding: $name, hint: "这条挂载的标识，如 taishan-docs；用于日志文件名与菜单栏显示")
                row("SSH 目标", binding: $target, hint: "user@host 或 ~/.ssh/config 别名，如 lyy@hhdev")
                row("端口", binding: $port, hint: "留空用默认 22；hhdev 这类要填 12880")
                row("远端路径", binding: $remotePath, hint: "/data/lyy/taishan/docs")
                row("本地挂载点", binding: $mountPoint, hint: "~/mnt/remote-docs")
                row("私钥", binding: $identity, hint: "可留空，如 ~/.ssh/id_rsa")
                row("额外选项", binding: $options, hint: "可留空；多个用逗号分隔")
            }

            Text("固定参数：-f、-o reconnect、-o noappledouble、-o dcache_timeout=5")
                .font(.caption)
                .foregroundStyle(.secondary)

            Text("该挂载正在运行时，保存后会自动按新参数重挂（先用旧定义卸载）。")
                .font(.caption)
                .foregroundStyle(.secondary)

            if !error.isEmpty {
                Text(error).font(.caption).foregroundStyle(.red)
            }

            HStack {
                Spacer()
                Button("取消") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button("保存") { save() }
                    .keyboardShortcut(.defaultAction)
                    .disabled(name.isEmpty || target.isEmpty || remotePath.isEmpty || mountPoint.isEmpty)
            }
        }
        .padding(20)
        .frame(width: 560)
    }

    private func row(_ label: String, binding: Binding<String>, hint: String) -> some View {
        GridRow {
            Text(label).frame(width: 80, alignment: .trailing)
            VStack(alignment: .leading, spacing: 2) {
                TextField("", text: binding)
                    .textFieldStyle(.roundedBorder)
                Text(hint).font(.caption2).foregroundStyle(.secondary)
            }
        }
    }

    private func save() {
        let trimmedName = name.trimmingCharacters(in: .whitespaces)
        if let existing = store.spec(named: trimmedName), existing.id != original?.id {
            error = "已存在同名挂载：\(trimmedName)"
            return
        }
        if !port.isEmpty, let value = Int(port), value <= 0 || value > 65535 {
            error = "端口不合法"
            return
        }
        let trimmedIdentity = identity.trimmingCharacters(in: .whitespaces)
        let spec = MountSpec(
            id: original?.id ?? UUID(),
            name: trimmedName,
            target: target.trimmingCharacters(in: .whitespaces),
            port: Int(port),
            remotePath: remotePath.trimmingCharacters(in: .whitespaces),
            mountPoint: mountPoint.trimmingCharacters(in: .whitespaces),
            identity: trimmedIdentity.isEmpty ? nil : trimmedIdentity,
            extraOptions: options
                .split(separator: ",")
                .map { $0.trimmingCharacters(in: .whitespaces) }
                .filter { !$0.isEmpty }
        )
        store.upsert(spec)
        onSaved(spec)
        dismiss()
    }
}
