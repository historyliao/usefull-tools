import Foundation

/// 托管 `spm run` 内核进程。
///
/// 关键点：内核的 stdin 是本进程持有的一根管道。界面进程以任何方式消失
/// （正常退出、崩溃、被 kill -9）时管道关闭，内核读到 EOF 就会收走全部隧道，
/// 与 TUI 模式下 manager 退出即断链的语义保持一致。
final class Kernel {
    static let shared = Kernel()

    private(set) var ownsKernel = false
    private(set) var attachedToExternal = false
    private var process: Process?
    private var stdinPipe: Pipe?

    var stateDir: URL {
        if let dir = ProcessInfo.processInfo.environment["SPM_DIR"], !dir.isEmpty {
            return URL(fileURLWithPath: (dir as NSString).expandingTildeInPath)
        }
        return FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent(".ssh-proxy-manager")
    }

    var socketPath: String { stateDir.appendingPathComponent("control.sock").path }
    var kernelLogPath: String { stateDir.appendingPathComponent("kernel.log").path }
    var client: ControlClient { ControlClient(socketPath: socketPath) }

    func ping() -> ControlResponse? {
        try? client.call(.ping())
    }

    /// 已有内核就附着，没有就拉起一个由本进程托管的内核。
    func ensureRunning() throws {
        if ping() != nil {
            attachedToExternal = !ownsKernel
            return
        }
        try spawn()
        let deadline = Date().addingTimeInterval(20)
        while Date() < deadline {
            if ping() != nil {
                attachedToExternal = false
                return
            }
            if let process, !process.isRunning {
                throw ControlError.transport("内核启动后立刻退出，详见 \(kernelLogPath)")
            }
            Thread.sleep(forTimeInterval: 0.2)
        }
        throw ControlError.transport("内核启动超时（20 秒内控制通道未就绪）")
    }

    func shutdown() {
        guard ownsKernel else { return }
        try? stdinPipe?.fileHandleForWriting.close()
        let deadline = Date().addingTimeInterval(10)
        while Date() < deadline {
            if let process, process.isRunning {
                Thread.sleep(forTimeInterval: 0.1)
                continue
            }
            break
        }
        if let process, process.isRunning {
            process.terminate()
        }
        ownsKernel = false
    }

    private func spawn() throws {
        guard let binary = Kernel.locateBinary() else {
            throw ControlError.transport("找不到 spm 可执行文件（期望随 App 一起打包，或用 SPM_BIN 指定）")
        }
        try FileManager.default.createDirectory(at: stateDir, withIntermediateDirectories: true)
        if !FileManager.default.fileExists(atPath: kernelLogPath) {
            FileManager.default.createFile(atPath: kernelLogPath, contents: nil)
        }
        let logHandle = try FileHandle(forWritingTo: URL(fileURLWithPath: kernelLogPath))
        try logHandle.seekToEnd()

        let stdinPipe = Pipe()
        let process = Process()
        process.executableURL = URL(fileURLWithPath: binary)
        process.arguments = ["run", "--watch-stdin", "--dir", stateDir.path]
        process.standardInput = stdinPipe
        process.standardOutput = logHandle
        process.standardError = logHandle
        try process.run()

        self.process = process
        self.stdinPipe = stdinPipe
        self.ownsKernel = true
    }

    static func locateBinary() -> String? {
        var candidates: [String] = []
        if let override = ProcessInfo.processInfo.environment["SPM_BIN"], !override.isEmpty {
            candidates.append((override as NSString).expandingTildeInPath)
        }
        if let executable = Bundle.main.executableURL {
            candidates.append(executable.deletingLastPathComponent().appendingPathComponent("spm").path)
        }
        let home = FileManager.default.homeDirectoryForCurrentUser
        candidates.append(contentsOf: [
            "/usr/local/bin/spm",
            "/opt/homebrew/bin/spm",
            home.appendingPathComponent("bin/spm").path,
            home.appendingPathComponent(".local/bin/spm").path,
        ])
        for candidate in candidates where FileManager.default.isExecutableFile(atPath: candidate) {
            return candidate
        }
        return nil
    }
}
