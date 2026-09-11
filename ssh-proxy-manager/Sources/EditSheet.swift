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
                        VStack(alignment: .leading, spacing: 3) {
                            ForEach(directionNotes, id: \.self) { note in
                                Text("• " + note)
                                    .font(.system(size: 11))
                                    .foregroundStyle(.secondary)
                                    .fixedSize(horizontal: false, vertical: true)
                            }
                        }
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
        .frame(width: 640, height: 680)
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
        VStack(alignment: .leading, spacing: 4) {
            HStack(alignment: .bottom, spacing: 8) {
                captioned("类型", width: 52) {
                    if draft.direction == .forward {
                        Picker("", selection: forward.type) {
                            Text("L").tag("L")
                            Text("D").tag("D")
                        }
                        .labelsHidden()
                    } else {
                        Text("R")
                            .font(.system(size: 12, weight: .semibold))
                    }
                }
                captioned(listenCaption, width: 122) {
                    TextField("127.0.0.1", text: forward.bindHost)
                }
                captioned(listenPortCaption, width: 84) {
                    TextField("0", value: forward.bindPort, format: .number)
                }
                if forward.wrappedValue.isSOCKS {
                    captioned("目标", width: 200) {
                        Text("SOCKS 代理，目标由客户端指定")
                            .font(.system(size: 11))
                            .foregroundStyle(.secondary)
                    }
                    Spacer(minLength: 0)
                } else {
                    Image(systemName: "arrow.right")
                        .font(.system(size: 10))
                        .foregroundStyle(.secondary)
                        .padding(.bottom, 5)
                    captioned(destCaption, width: 122) {
                        TextField("127.0.0.1", text: forward.destHost)
                    }
                    captioned("目标端口", width: 84) {
                        TextField("0", value: forward.destPort, format: .number)
                    }
                }
            }
            Text(preview(for: forward.wrappedValue))
                .font(.system(size: 10.5, design: .monospaced))
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    private var listenCaption: String {
        draft.direction == .reverse ? "远端监听地址" : "本机监听地址"
    }

    private var listenPortCaption: String {
        draft.direction == .reverse ? "远端监听端口" : "本机监听端口"
    }

    private var destCaption: String {
        draft.direction == .reverse ? "本机目标地址" : "远端目标地址"
    }

    private var directionNotes: [String] {
        switch draft.direction {
        case .reverse:
            return [
                "链路：访问者 → 远端 \(remoteHostLabel) 的监听端口 →（ssh 隧道）→ 你这台 Mac 的目标地址:端口。",
                "远端监听地址写 127.0.0.1 时只有远端机器自己能连；要让别的机器连，得填 0.0.0.0 或远端网卡 IP，并且远端 sshd 要开 GatewayPorts。",
                "目标地址是在本机侧连接的：127.0.0.1 就是这台 Mac，也可以写本机能访问到的内网地址。",
                "远端监听端口被占用时 ssh 会立刻退出，内核按退避策略重试，连续失败 5 次后转为「已失败」。",
                "ssh 没有「远端 SOCKS」：-D 只有本地版本，反向只能一条条配固定目标。要在远端用 SOCKS，就先在本机开一个 SOCKS（正向 -D），再用一条反向代理把那个端口暴露到远端。",
                "例：把本机 Chrome 调试端口暴露到远端 → 远端 127.0.0.1:9223 转发到本机 127.0.0.1:9222。",
            ]
        case .forward:
            return [
                "链路：本机监听端口 →（ssh 隧道）→ 远端服务器能访问到的目标地址:端口。",
                "L（本地转发）：本机这个端口固定通向一个目标，目标在建定义时写死；连接由远端 sshd 发起，所以目标名/地址是远端能访问到的。",
                "D（动态转发）：本机这个端口是 SOCKS4/SOCKS5 代理，目标由连上来的程序运行时指定，一个端口覆盖任意目标；客户端必须支持 SOCKS（浏览器、curl --socks5）。",
                "本机监听地址写 127.0.0.1 时只有本机能连；填 0.0.0.0 等于把端口/代理开放给同网段，谨慎。",
                "例：L → 本机 127.0.0.1:8080 通向远端内网 192.168.179.3:8080；D → 本机 127.0.0.1:1080 做 SOCKS 出口。",
            ]
        }
    }

    private var remoteHostLabel: String {
        Target.parse(sshSpec)?.host ?? "远端服务器"
    }

    private func preview(for forward: Forward) -> String {
        switch (draft.direction, forward.isSOCKS) {
        case (.reverse, _):
            return "\(remoteHostLabel) \(forward.bindHost):\(forward.bindPort)  ⇄  本机 \(forward.destHost):\(forward.destPort)"
        case (.forward, true):
            return "本机 \(forward.bindHost):\(forward.bindPort) 提供 SOCKS5 代理"
        default:
            return "本机 \(forward.bindHost):\(forward.bindPort)  ⇄  远端可达的 \(forward.destHost):\(forward.destPort)"
        }
    }

    @ViewBuilder
    private func captioned(_ caption: String, width: CGFloat, @ViewBuilder field: () -> some View) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(caption)
                .font(.system(size: 10))
                .foregroundStyle(.secondary)
            field()
        }
        .frame(width: width, alignment: .leading)
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
