import Foundation

struct MountSpec: Codable, Identifiable, Hashable {
    var id: UUID = UUID()
    var name: String
    var target: String
    var port: Int?
    var remotePath: String
    var mountPoint: String
    var identity: String?
    var extraOptions: [String] = []

    var displayTarget: String {
        guard let port, port != 22 else { return target }
        return "\(target):\(port)"
    }

    var source: String {
        "\(target):\(remotePath)"
    }

    var expandedMountPoint: String {
        (mountPoint as NSString).expandingTildeInPath
    }

    func sshfsArguments(sshfsPath: String) -> [String] {
        var args = [
            source,
            expandedMountPoint,
            "-f",
            "-o", "reconnect",
            "-o", "volname=\(name)",
            "-o", "noappledouble",
            "-o", "dcache_timeout=5",
        ]
        if let port {
            args += ["-p", String(port)]
        }
        if let identity, !identity.isEmpty {
            // sshfs 不认 -i，必须用 -o IdentityFile=
            args += ["-o", "IdentityFile=\((identity as NSString).expandingTildeInPath)"]
        }
        for option in extraOptions where !option.isEmpty {
            args += ["-o", option]
        }
        return args
    }
}

enum MountStatus: Equatable {
    case unmounted
    case mounting
    case mounted(external: Bool)
    case failed(String)

    var isMounted: Bool {
        if case .mounted = self { return true }
        return false
    }

    var label: String {
        switch self {
        case .unmounted: return "未挂载"
        case .mounting: return "挂载中…"
        case .mounted(let external): return external ? "已挂载(外部)" : "已挂载"
        case .failed(let reason): return "失败: \(reason)"
        }
    }
}

struct MountRecord: Identifiable, Hashable {
    var id: UUID
    var name: String
    var source: String
    var mountPoint: String
    var processID: Int32?
}
