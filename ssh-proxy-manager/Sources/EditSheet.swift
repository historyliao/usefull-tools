import SwiftUI

struct EditSheet: View {
    @State private var draft: ProxyDefinition
    @State private var sshSpec: String
    @State private var extraArgs: String
    @State private var errorText: String?

    let isNew: Bool
    let onSave: (ProxyDefinition) -> Void
    let onCancel: () -> Void

    init(definition: ProxyDefinition, isNew: Bool,
         onSave: @escaping (ProxyDefinition) -> Void,
         onCancel: @escaping () -> Void) {
        _draft = State(initialValue: definition)
        _sshSpec = State(initialValue: definition.target.address)
        _extraArgs = State(initialValue: definition.target.extraArgs.joined(separator: " "))
        self.isNew = isNew
        self.onSave = onSave
        self.onCancel = onCancel
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text(isNew ? "新建代理" : "编辑 \(draft.name)")
                    .font(.system(size: 15, weight: .bold))
                if !isNew {
                    Text("运行中的改动需要重启后生效")
                        .font(.system(size: 11))
                        .foregroundStyle(.secondary)
                }
                Spacer()
            }
            .padding(.horizontal, 18)
            .padding(.top, 16)

            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    section("类型") {
                        Picker("", selection: $draft.direction) {
                            ForEach(ProxyDirection.allCases) { direction in
                                Text(direction.label).tag(direction)
                            }
                        }
                        .pickerStyle(.segmented)
                        .labelsHidden()
                        .onChange(of: draft.direction) { _, newValue in
                            normalizeForwards(for: newValue)
                        }
                        Text(draft.direction.explanation)
                            .font(.system(size: 11))
                            .foregroundStyle(.secondary)
                    }

                    section("基础") {
                        labeled("名称") {
                            TextField("例如 chrome-9223", text: $draft.name)
                                .disabled(!isNew)
                                .opacity(isNew ? 1 : 0.6)
                        }
                        labeled("SSH 目标") {
                            TextField("hhdev 或 lyy@host:12880", text: $sshSpec)
                        }
                        labeled("私钥") {
                            TextField("可留空，例如 ~/.ssh/id_rsa", text: $draft.target.identity)
                        }
                    }

                    section("转发") {
                        ForEach($draft.forwards) { $forward in
                            forwardRow(forward: $forward)
                        }
                        HStack(spacing: 8) {
                            Button {
                                draft.forwards.append(defaultForward())
                            } label: {
                                Label("添加一条", systemImage: "plus")
                            }
                            Button {
                                if draft.forwards.count > 1 { draft.forwards.removeLast() }
                            } label: {
                                Label("删除末条", systemImage: "minus")
                            }
                            .disabled(draft.forwards.count <= 1)
                        }
                        .font(.system(size: 12))
                        Text(draft.direction == .reverse
                             ? "远端 bind 地址默认 127.0.0.1；要让远端听 0.0.0.0，需要远端 sshd 开 GatewayPorts。"
                             : "正向往外连：把本机端口指向远端服务；D 类型只填本地端口，走 SOCKS。")
                            .font(.system(size: 11))
                            .foregroundStyle(.secondary)
                    }

                    section("行为") {
                        labeled("重启策略") {
                            Picker("", selection: $draft.restart) {
                                ForEach(RestartPolicy.allCases) { policy in
                                    Text(policy.label).tag(policy)
                                }
                            }
                            .labelsHidden()
                            .frame(width: 160)
                        }
                        labeled("自动启动") {
                            Toggle("内核启动时自动拉起", isOn: $draft.autostart)
                                .toggleStyle(.checkbox)
                        }
                        labeled("额外参数") {
                            TextField("透传给 ssh，空格分隔", text: $extraArgs)
                        }
                    }
                }
                .padding(18)
            }

            if let errorText {
                Text("✗ \(errorText)")
                    .font(.system(size: 12))
                    .foregroundStyle(.red)
                    .padding(.horizontal, 18)
                    .padding(.bottom, 6)
            }

            Divider()
            HStack {
                Spacer()
                Button("取消") { onCancel() }
                    .keyboardShortcut(.cancelAction)
                Button(isNew ? "创建" : "保存") { commit() }
                    .keyboardShortcut(.defaultAction)
            }
            .padding(14)
        }
        .frame(width: 620, height: 620)
    }

    private func commit() {
        guard let target = Target.parse(sshSpec) else {
            errorText = "SSH 目标格式不对，应为 hhdev 或 user@host:port"
            return
        }
        var definition = draft
        definition.name = draft.name.trimmingCharacters(in: .whitespacesAndNewlines)
        definition.target = Target(
            user: target.user,
            host: target.host,
            port: target.port,
            identity: draft.target.identity.trimmingCharacters(in: .whitespacesAndNewlines),
            extraArgs: extraArgs.split(separator: " ").map(String.init).filter { !$0.isEmpty }
        )
        if let error = definition.validate() {
            errorText = error
            return
        }
        onSave(definition)
    }

    private func normalizeForwards(for direction: ProxyDirection) {
        for index in draft.forwards.indices where !direction.allowedForwardTypes.contains(draft.forwards[index].type) {
            draft.forwards[index].type = direction.allowedForwardTypes[0]
        }
    }

    private func defaultForward() -> Forward {
        switch draft.direction {
        case .reverse:
            return Forward(type: "R", bindPort: 0, destHost: "127.0.0.1", destPort: 0)
        case .forward:
            return Forward(type: "L", bindPort: 0, destHost: "127.0.0.1", destPort: 0)
        }
    }

    @ViewBuilder
    private func forwardRow(forward: Binding<Forward>) -> some View {
        HStack(spacing: 8) {
            if draft.direction == .forward {
                Picker("", selection: forward.type) {
                    Text("L").tag("L")
                    Text("D").tag("D")
                }
                .labelsHidden()
                .frame(width: 52)
            } else {
                Text("R")
                    .font(.system(size: 12, weight: .semibold))
                    .frame(width: 52)
            }

            TextField(draft.direction == .reverse ? "远端地址" : "本机地址", text: forward.bindHost)
                .frame(width: 110)
            TextField(draft.direction == .reverse ? "远端端口" : "本机端口", value: forward.bindPort, format: .number)
                .frame(width: 78)

            if forward.wrappedValue.isSOCKS {
                Text("SOCKS，无需目标")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                Spacer()
            } else {
                Image(systemName: "arrow.right")
                    .font(.system(size: 10))
                    .foregroundStyle(.secondary)
                TextField("目标主机", text: forward.destHost)
                TextField("目标端口", value: forward.destPort, format: .number)
                    .frame(width: 78)
            }
        }
    }

    @ViewBuilder
    private func section(_ title: String, @ViewBuilder content: () -> some View) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(title)
                .font(.system(size: 12, weight: .semibold))
                .foregroundStyle(.secondary)
            content()
        }
    }

    @ViewBuilder
    private func labeled(_ label: String, @ViewBuilder content: () -> some View) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Text(label)
                .font(.system(size: 12))
                .foregroundStyle(.secondary)
                .frame(width: 68, alignment: .trailing)
            content()
        }
    }
}
