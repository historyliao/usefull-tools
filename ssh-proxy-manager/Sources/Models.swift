import Foundation

enum ProxyDirection: String, Codable, CaseIterable, Identifiable {
    case forward
    case reverse

    var id: String { rawValue }

    var label: String {
        switch self {
        case .reverse: return "反向代理"
        case .forward: return "正向代理"
        }
    }

    var shortLabel: String {
        switch self {
        case .reverse: return "反向"
        case .forward: return "正向"
        }
    }

    var explanation: String {
        switch self {
        case .reverse: return "把远端端口映射到本机服务（-R），例如远端 9223 → 本机 9222"
        case .forward: return "把本机端口映射到远端服务（-L）或开本地 SOCKS（-D）"
        }
    }

    var allowedForwardTypes: [String] {
        switch self {
        case .reverse: return ["R"]
        case .forward: return ["L", "D"]
        }
    }

    static func derive(from forwards: [Forward]) -> ProxyDirection {
        forwards.contains { $0.type == "R" } ? .reverse : .forward
    }
}

enum RestartPolicy: String, Codable, CaseIterable, Identifiable {
    case always
    case onFailure = "on-failure"
    case never

    var id: String { rawValue }

    var label: String {
        switch self {
        case .always: return "总是重启"
        case .onFailure: return "失败时重启"
        case .never: return "不重启"
        }
    }
}

struct Backoff: Codable, Hashable {
    var initial: Int = 1
    var max: Int = 60
    var maxAttempts: Int = 5

    enum CodingKeys: String, CodingKey {
        case initial
        case max
        case maxAttempts = "max_attempts"
    }

    init(initial: Int = 1, max: Int = 60, maxAttempts: Int = 5) {
        self.initial = initial
        self.max = max
        self.maxAttempts = maxAttempts
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        initial = try container.decodeIfPresent(Int.self, forKey: .initial) ?? 1
        max = try container.decodeIfPresent(Int.self, forKey: .max) ?? 60
        maxAttempts = try container.decodeIfPresent(Int.self, forKey: .maxAttempts) ?? 5
    }
}

struct Forward: Codable, Hashable, Identifiable {
    var id = UUID()
    var type: String
    var bindHost: String
    var bindPort: Int
    var destHost: String
    var destPort: Int

    enum CodingKeys: String, CodingKey {
        case type
        case bindHost = "bind_host"
        case bindPort = "bind_port"
        case destHost = "dest_host"
        case destPort = "dest_port"
    }

    init(type: String, bindHost: String = "127.0.0.1", bindPort: Int = 0,
         destHost: String = "127.0.0.1", destPort: Int = 0) {
        self.type = type
        self.bindHost = bindHost
        self.bindPort = bindPort
        self.destHost = destHost
        self.destPort = destPort
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        type = try container.decodeIfPresent(String.self, forKey: .type) ?? "R"
        bindHost = try container.decodeIfPresent(String.self, forKey: .bindHost) ?? "127.0.0.1"
        bindPort = try container.decodeIfPresent(Int.self, forKey: .bindPort) ?? 0
        destHost = try container.decodeIfPresent(String.self, forKey: .destHost) ?? "127.0.0.1"
        destPort = try container.decodeIfPresent(Int.self, forKey: .destPort) ?? 0
    }

    var isSOCKS: Bool { type == "D" }

    var spec: String {
        isSOCKS ? "\(bindHost):\(bindPort)" : "\(bindHost):\(bindPort):\(destHost):\(destPort)"
    }

    var display: String { "\(type) \(spec)" }

    var summary: String {
        switch type {
        case "R": return "远端 \(bindPort) → \(destHost):\(destPort)"
        case "L": return "本地 \(bindPort) → \(destHost):\(destPort)"
        default: return "本地 SOCKS \(bindPort)"
        }
    }
}

struct Target: Codable, Hashable {
    var user: String = ""
    var host: String = ""
    var port: Int = 22
    var identity: String = ""
    var extraArgs: [String] = []

    enum CodingKeys: String, CodingKey {
        case user
        case host
        case port
        case identity
        case extraArgs = "extra_args"
    }

    init(user: String = "", host: String = "", port: Int = 22, identity: String = "", extraArgs: [String] = []) {
        self.user = user
        self.host = host
        self.port = port
        self.identity = identity
        self.extraArgs = extraArgs
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        user = try container.decodeIfPresent(String.self, forKey: .user) ?? ""
        host = try container.decodeIfPresent(String.self, forKey: .host) ?? ""
        port = try container.decodeIfPresent(Int.self, forKey: .port) ?? 22
        identity = try container.decodeIfPresent(String.self, forKey: .identity) ?? ""
        extraArgs = try container.decodeIfPresent([String].self, forKey: .extraArgs) ?? []
    }

    var address: String {
        var hostPart = host
        if port != 0 && port != 22 {
            hostPart += ":\(port)"
        }
        return user.isEmpty ? hostPart : "\(user)@\(hostPart)"
    }

    static func parse(_ spec: String) -> Target? {
        let trimmed = spec.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return nil }
        var rest = trimmed
        var user = ""
        var port = 22
        if let at = rest.lastIndex(of: "@") {
            user = String(rest[rest.startIndex..<at])
            rest = String(rest[rest.index(after: at)...])
        }
        if let colon = rest.lastIndex(of: ":") {
            let portText = String(rest[rest.index(after: colon)...])
            guard let parsed = Int(portText), parsed > 0, parsed <= 65535 else { return nil }
            port = parsed
            rest = String(rest[rest.startIndex..<colon])
        }
        guard !rest.isEmpty else { return nil }
        return Target(user: user, host: rest, port: port)
    }
}

struct ProxyDefinition: Codable, Hashable, Identifiable {
    var name: String
    var direction: ProxyDirection
    var target: Target
    var forwards: [Forward]
    var restart: RestartPolicy
    var backoff: Backoff
    var autostart: Bool

    enum CodingKeys: String, CodingKey {
        case name
        case direction
        case target
        case forwards
        case restart
        case backoff
        case autostart
    }

    var id: String { name }

    init(name: String, direction: ProxyDirection, target: Target, forwards: [Forward],
         restart: RestartPolicy = .onFailure, backoff: Backoff = Backoff(), autostart: Bool = true) {
        self.name = name
        self.direction = direction
        self.target = target
        self.forwards = forwards
        self.restart = restart
        self.backoff = backoff
        self.autostart = autostart
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        name = try container.decode(String.self, forKey: .name)
        target = try container.decodeIfPresent(Target.self, forKey: .target) ?? Target()
        forwards = try container.decodeIfPresent([Forward].self, forKey: .forwards) ?? []
        restart = try container.decodeIfPresent(RestartPolicy.self, forKey: .restart) ?? .onFailure
        backoff = try container.decodeIfPresent(Backoff.self, forKey: .backoff) ?? Backoff()
        autostart = try container.decodeIfPresent(Bool.self, forKey: .autostart) ?? true
        if let raw = try container.decodeIfPresent(String.self, forKey: .direction),
           let parsed = ProxyDirection(rawValue: raw) {
            direction = parsed
        } else {
            direction = ProxyDirection.derive(from: forwards)
        }
    }

    var forwardSummary: String {
        forwards.map(\.summary).joined(separator: "，")
    }

    var restartSummary: String {
        "\(restart.label) · 退避 \(backoff.initial)s 起，封顶 \(backoff.max)s，最多 \(backoff.maxAttempts) 次"
    }

    func validate() -> String? {
        let trimmedName = name.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmedName.isEmpty { return "名称不能为空" }
        let allowed = CharacterSet(charactersIn: "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-")
        if trimmedName.unicodeScalars.contains(where: { !allowed.contains($0) }) {
            return "名称只允许字母、数字、点、下划线和连字符"
        }
        if target.host.trimmingCharacters(in: .whitespaces).isEmpty { return "SSH 目标不能为空" }
        if forwards.isEmpty { return "至少需要一条转发规则" }
        for (index, forward) in forwards.enumerated() {
            if !direction.allowedForwardTypes.contains(forward.type) {
                return "第 \(index + 1) 条转发类型与方向不符（\(direction.label)只能用 \(direction.allowedForwardTypes.joined(separator: "/"))）"
            }
            if forward.bindPort < 1 || forward.bindPort > 65535 {
                return "第 \(index + 1) 条转发的绑定端口要在 1-65535"
            }
            if !forward.isSOCKS {
                if forward.destHost.trimmingCharacters(in: .whitespaces).isEmpty {
                    return "第 \(index + 1) 条转发缺少目标主机"
                }
                if forward.destPort < 1 || forward.destPort > 65535 {
                    return "第 \(index + 1) 条转发的目标端口要在 1-65535"
                }
            }
        }
        return nil
    }
}

struct ProxyRuntime: Codable, Hashable {
    var state: String = "stopped"
    var pid: Int = 0
    var guardPID: Int = 0
    var restarts: Int = 0
    var lastError: String?
    var startedAt: String?

    enum CodingKeys: String, CodingKey {
        case state
        case pid
        case restarts
        case guardPID = "guard_pid"
        case lastError = "last_error"
        case startedAt = "started_at"
    }

    init() {}

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        state = try container.decodeIfPresent(String.self, forKey: .state) ?? "stopped"
        pid = try container.decodeIfPresent(Int.self, forKey: .pid) ?? 0
        guardPID = try container.decodeIfPresent(Int.self, forKey: .guardPID) ?? 0
        restarts = try container.decodeIfPresent(Int.self, forKey: .restarts) ?? 0
        lastError = try container.decodeIfPresent(String.self, forKey: .lastError)
        startedAt = try container.decodeIfPresent(String.self, forKey: .startedAt)
    }

    var isActive: Bool {
        ["running", "starting", "stopping", "restarting"].contains(state)
    }

    var isTransitioning: Bool {
        ["starting", "stopping", "restarting"].contains(state)
    }

    var label: String {
        switch state {
        case "running": return "运行中"
        case "starting": return "启动中"
        case "stopping": return "停止中"
        case "restarting": return "重启中"
        case "failed": return "已失败"
        default: return "已停止"
        }
    }

    var uptimeSeconds: Int? {
        guard isActive, let startedAt, let date = ISO8601DateFormatter().date(from: startedAt),
              date.timeIntervalSince1970 > 0 else { return nil }
        return max(0, Int(Date().timeIntervalSince(date)))
    }

    var uptimeText: String {
        guard let seconds = uptimeSeconds else { return "-" }
        let hours = seconds / 3600
        let minutes = (seconds % 3600) / 60
        let secs = seconds % 60
        if hours > 0 { return "\(hours)h\(minutes)m" }
        if minutes > 0 { return "\(minutes)m\(secs)s" }
        return "\(secs)s"
    }
}

struct ProxyItem: Identifiable {
    var definition: ProxyDefinition
    var runtime: ProxyRuntime

    var id: String { definition.name }
}
