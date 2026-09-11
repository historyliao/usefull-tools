import Darwin
import Foundation

enum EngineError: LocalizedError {
    case sshfsNotFound
    case mountPointMissing(String)

    var errorDescription: String? {
        switch self {
        case .sshfsNotFound:
            return "找不到 sshfs，请先 brew install gromgit/fuse/sshfs-mac"
        case .mountPointMissing(let path):
            return "挂载点不可用: \(path)"
        }
    }
}

final class MountEngine {
    struct ShellResult {
        let status: Int32
        let output: String
    }

    private let fileManager = FileManager.default
    private var processes: [UUID: Process] = [:]
    private let lock = NSLock()

    func sshfsPath() -> String? {
        let candidates = ["/opt/homebrew/bin/sshfs", "/usr/local/bin/sshfs"]
        for candidate in candidates where fileManager.isExecutableFile(atPath: candidate) {
            return candidate
        }
        let path = ProcessInfo.processInfo.environment["PATH"] ?? ""
        for directory in path.split(separator: ":") {
            let full = String(directory) + "/sshfs"
            if fileManager.isExecutableFile(atPath: full) {
                return full
            }
        }
        return nil
    }

    @discardableResult
    func shell(_ launchPath: String, _ args: [String], timeout: TimeInterval = 8) -> ShellResult {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: launchPath)
        process.arguments = args
        let pipe = Pipe()
        process.standardOutput = pipe
        process.standardError = pipe
        do {
            try process.run()
        } catch {
            return ShellResult(status: -1, output: error.localizedDescription)
        }
        let deadline = Date().addingTimeInterval(timeout)
        while process.isRunning && Date() < deadline {
            usleep(100_000)
        }
        if process.isRunning {
            process.terminate()
            usleep(300_000)
            if process.isRunning {
                kill(process.processIdentifier, SIGKILL)
            }
            return ShellResult(status: -2, output: "命令超时(\(Int(timeout))s): \(launchPath) \(args.joined(separator: " "))")
        }
        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        return ShellResult(status: process.terminationStatus, output: String(data: data, encoding: .utf8) ?? "")
    }

    /// 用内核接口读取挂载表：不 spawn 进程，也不会因为某个网络卷无响应而卡死。
    func mountPoints() -> Set<String> {
        var buffer: UnsafeMutablePointer<statfs>?
        let count = getmntinfo(&buffer, MNT_NOWAIT)
        guard count > 0, let buffer else { return [] }
        var points = Set<String>()
        for index in 0..<Int(count) {
            var entry = buffer[index]
            let fileSystemType = withUnsafePointer(to: &entry.f_fstypename) { pointer in
                pointer.withMemoryRebound(to: CChar.self, capacity: Int(MNAMELEN)) { String(cString: $0) }
            }
            guard fileSystemType.hasPrefix("macfuse") else { continue }
            let mountOn = withUnsafePointer(to: &entry.f_mntonname) { pointer in
                pointer.withMemoryRebound(to: CChar.self, capacity: Int(MAXPATHLEN)) { String(cString: $0) }
            }
            points.insert(mountOn)
        }
        return points
    }

    func isMounted(_ spec: MountSpec) -> Bool {
        isMounted(spec, in: mountPoints())
    }

    /// 挂载点目录当前是否已存在（判断目录是"我们建的"还是"用户原有的"）。
    func mountPointExists(_ spec: MountSpec) -> Bool {
        var isDirectory: ObjCBool = false
        let path = spec.expandedMountPoint
        return fileManager.fileExists(atPath: path, isDirectory: &isDirectory) && isDirectory.boolValue
    }

    /// 路径本身是否是一个挂载点（与父目录的 fsid 不同即视为挂载点）。
    /// 用途：挂载点位于 /tmp 这类软链路径时，挂载表里记的是 /private/tmp，字符串比较会漏判。
    func isMountPoint(_ path: String) -> Bool {
        var child = statfs()
        var parent = statfs()
        guard statfs(path, &child) == 0 else { return false }
        let parentPath = (path as NSString).deletingLastPathComponent
        guard !parentPath.isEmpty, statfs(parentPath, &parent) == 0 else { return false }
        return child.f_fsid.val.0 != parent.f_fsid.val.0 || child.f_fsid.val.1 != parent.f_fsid.val.1
    }

    func isMounted(_ spec: MountSpec, in points: Set<String>) -> Bool {
        let path = spec.expandedMountPoint
        if points.contains(path) { return true }
        for (short, long) in [("/tmp/", "/private/tmp/"), ("/var/", "/private/var/")] where path.hasPrefix(short) {
            if points.contains(long + path.dropFirst(short.count)) { return true }
        }
        return false
    }

    func runningProcessID(_ id: UUID) -> Int32? {
        lock.lock()
        defer { lock.unlock() }
        guard let process = processes[id], process.isRunning else { return nil }
        return process.processIdentifier
    }

    func mount(_ spec: MountSpec, logURL: URL) throws {
        guard let sshfs = sshfsPath() else { throw EngineError.sshfsNotFound }
        let mountPoint = spec.expandedMountPoint
        // 挂载点目录由 macFUSE 自动创建（含缺失的父目录，已实测）；这里只拦"同名文件"这种明显错误
        var isDirectory: ObjCBool = false
        if fileManager.fileExists(atPath: mountPoint, isDirectory: &isDirectory), !isDirectory.boolValue {
            throw EngineError.mountPointMissing("\(mountPoint) 已存在且不是目录")
        }
        try fileManager.createDirectory(at: logURL.deletingLastPathComponent(), withIntermediateDirectories: true)
        if !fileManager.fileExists(atPath: logURL.path) {
            fileManager.createFile(atPath: logURL.path, contents: nil)
        }
        let handle = try FileHandle(forWritingTo: logURL)
        handle.seekToEndOfFile()
        let header = "\n# \(Self.timestamp()) mount \(spec.source) -> \(mountPoint)\n"
        handle.write(Data(header.utf8))

        let process = Process()
        process.executableURL = URL(fileURLWithPath: sshfs)
        process.arguments = spec.sshfsArguments(sshfsPath: sshfs)
        process.standardOutput = handle
        process.standardError = handle
        process.terminationHandler = { [weak self] finished in
            try? handle.close()
            self?.forget(spec.id)
            let note = "\n# \(Self.timestamp()) exit code \(finished.terminationStatus)\n"
            if let extra = try? FileHandle(forWritingTo: logURL) {
                extra.seekToEndOfFile()
                extra.write(Data(note.utf8))
                try? extra.close()
            }
        }
        try process.run()

        lock.lock()
        processes[spec.id] = process
        lock.unlock()
    }

    @discardableResult
    func unmount(_ spec: MountSpec) -> String? {
        let mountPoint = spec.expandedMountPoint
        lock.lock()
        let process = processes.removeValue(forKey: spec.id)
        lock.unlock()

        if let process, process.isRunning {
            process.terminate()
            let deadline = Date().addingTimeInterval(6)
            while process.isRunning && Date() < deadline {
                usleep(200_000)
            }
            if process.isRunning {
                kill(process.processIdentifier, SIGKILL)
            }
        }

        // 必须用软链感知的判断：/tmp 在挂载表里写作 /private/tmp，直接比字符串会漏判
        guard isMounted(spec, in: mountPoints()) || isMountPoint(mountPoint) else { return nil }

        let direct = shell("/sbin/umount", [mountPoint])
        if direct.status == 0 { return nil }
        let forced = shell("/usr/sbin/diskutil", ["unmount", "force", mountPoint])
        if forced.status == 0 { return nil }
        return "umount 失败: \(direct.output.trimmingCharacters(in: .whitespacesAndNewlines)) / \(forced.output.trimmingCharacters(in: .whitespacesAndNewlines))"
    }

    /// 卷卡死时的自愈路径：先杀掉对应的 sshfs 进程，再走 umount / diskutil force。
    @discardableResult
    func forceUnmount(_ spec: MountSpec) -> String? {
        let pattern = "sshfs.*\(spec.expandedMountPoint)"
        let output = shell("/usr/bin/pgrep", ["-f", pattern], timeout: 5).output
        for line in output.split(separator: "\n") {
            if let pid = Int32(line.trimmingCharacters(in: .whitespaces)), pid > 0 {
                kill(pid, SIGKILL)
            }
        }
        usleep(500_000)
        return unmount(spec)
    }

    /// 定义（远端路径/端口/私钥/选项/挂载点等）变更后重挂：先用旧定义卸载，再用新定义挂载。
    func remount(_ updated: MountSpec, previous: MountSpec, logURL: URL) throws {
        _ = unmount(previous)
        try mount(updated, logURL: logURL)
    }

    /// 删除定义后清理本地挂载点：仅在"目录存在、是空目录、且当前没有挂载"时删除。
    /// 非空目录一律保留（避免误删用户自己的内容），并返回说明。
    @discardableResult
    func removeMountPointIfEmpty(_ spec: MountSpec, createdByApp: Bool) -> String? {
        let path = spec.expandedMountPoint
        var isDirectory: ObjCBool = false
        guard fileManager.fileExists(atPath: path, isDirectory: &isDirectory), isDirectory.boolValue else {
            return nil
        }
        if isMounted(spec, in: mountPoints()) || isMountPoint(path) {
            return "本地挂载点仍在挂载中，未处理: \(path)"
        }
        let contents = (try? fileManager.contentsOfDirectory(atPath: path)) ?? []
        guard contents.isEmpty else {
            return "本地挂载点非空，已保留: \(path)"
        }
        // 只清理本 App 挂载时创建的目录；用户自己准备的目录一律保留
        guard createdByApp else {
            return "本地挂载点不是本 App 创建的，已保留: \(path)"
        }
        do {
            try fileManager.removeItem(atPath: path)
            return nil
        } catch {
            return "本地挂载点删除失败: \(error.localizedDescription)"
        }
    }

    func logTail(_ spec: MountSpec, logURL: URL, lines: Int = 30) -> [String] {
        guard let handle = try? FileHandle(forReadingFrom: logURL) else { return [] }
        defer { try? handle.close() }
        let size = (try? handle.seekToEnd()) ?? 0
        let window: UInt64 = 64 * 1024
        let offset = size > window ? size - window : 0
        try? handle.seek(toOffset: offset)
        guard let data = try? handle.readToEnd(), let text = String(data: data, encoding: .utf8) else { return [] }
        let all = text.split(separator: "\n", omittingEmptySubsequences: false).map(String.init)
        return Array(all.suffix(lines))
    }

    private func forget(_ id: UUID) {
        lock.lock()
        processes[id] = nil
        lock.unlock()
    }

    static func timestamp() -> String {
        let formatter = DateFormatter()
        formatter.dateFormat = "yyyy-MM-dd HH:mm:ss"
        return formatter.string(from: Date())
    }
}
