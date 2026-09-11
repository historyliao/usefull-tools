import Foundation

enum CLI {
    static var isInvocation: Bool {
        let args = CommandLine.arguments.dropFirst()
        guard let first = args.first else { return false }
        return first == "--cli"
    }

    static func run() -> Never {
        var args = Array(CommandLine.arguments.dropFirst())
        if args.first == "--cli" { args.removeFirst() }
        let store = Store(baseDir: takeDirectory(from: &args))
        let engine = MountEngine()
        guard let command = args.first else {
            printUsage()
            exit(2)
        }

        switch command {
        case "list":
            list(store: store, engine: engine)
        case "status":
            list(store: store, engine: engine)
        case "add":
            add(Array(args.dropFirst()), store: store)
        case "edit", "update":
            edit(Array(args.dropFirst()), store: store, engine: engine)
        case "remove", "delete":
            remove(Array(args.dropFirst()), store: store, engine: engine)
        case "mount":
            mount(Array(args.dropFirst()), store: store, engine: engine)
        case "unmount":
            unmount(Array(args.dropFirst()), store: store, engine: engine)
        case "force-unmount":
            forceUnmount(Array(args.dropFirst()), store: store, engine: engine)
        default:
            printUsage()
            exit(2)
        }
        exit(0)
    }

    private static func printUsage() {
        print("""
        usage: ssh-mount-manager --cli <command>
          list
          add --name N --target user@host [--port P] --remote /path --mountpoint /local/path [--identity ~/.ssh/id_rsa] [--option o=...]
          edit --name N [--target user@host] [--port P] [--remote /path] [--mountpoint /local/path]
          mount <name> | unmount <name> | remove <name>

        通用参数: --dir <path>（或环境变量 SPM_DIR）指定状态目录，默认 ~/.ssh-mount-manager
        """)
    }

    /// 取 --dir / --dir= / SPM_DIR 指定的状态目录，并从参数里摘掉，便于隔离测试。
    private static func takeDirectory(from args: inout [String]) -> URL? {
        var index = 0
        while index < args.count {
            let arg = args[index]
            if arg == "--dir", index + 1 < args.count {
                let path = args[index + 1]
                args.removeSubrange(index...(index + 1))
                return URL(fileURLWithPath: (path as NSString).expandingTildeInPath)
            }
            if arg.hasPrefix("--dir=") {
                let path = String(arg.dropFirst("--dir=".count))
                args.remove(at: index)
                return URL(fileURLWithPath: (path as NSString).expandingTildeInPath)
            }
            index += 1
        }
        if let env = ProcessInfo.processInfo.environment["SPM_DIR"], !env.isEmpty {
            return URL(fileURLWithPath: (env as NSString).expandingTildeInPath)
        }
        return nil
    }

    private static func list(store: Store, engine: MountEngine) {
        if store.specs.isEmpty {
            print("暂无挂载定义")
            return
        }
        for spec in store.specs {
            let mounted = engine.isMounted(spec)
            let external = mounted && engine.runningProcessID(spec.id) == nil
            let state = mounted ? (external ? "mounted(external)" : "mounted") : "unmounted"
            print("\(spec.name)\t\(spec.displayTarget)\t\(spec.remotePath)\t\(spec.expandedMountPoint)\t\(state)")
        }
    }

    private static func add(_ args: [String], store: Store) {
        let parsed = parseAddArgs(args)
        let name = parsed.name
        let target = parsed.target
        let remote = parsed.remote
        let mountPoint = parsed.mountPoint
        guard let name, let target, let remote, let mountPoint else {
            print("缺少参数: --name --target --remote --mountpoint")
            exit(2)
        }
        if store.spec(named: name) != nil {
            print("同名挂载已存在: \(name)")
            exit(1)
        }
        let spec = MountSpec(name: name, target: target, port: parsed.port, remotePath: remote,
                             mountPoint: mountPoint, identity: parsed.identity, extraOptions: parsed.options)
        store.upsert(spec)
        print("已添加 \(name)")
    }

    private struct ParsedArgs {
        var name: String?
        var target: String?
        var port: Int?
        var remote: String?
        var mountPoint: String?
        var identity: String?
        var options: [String] = []
    }

    private static func parseAddArgs(_ args: [String]) -> ParsedArgs {
        var parsed = ParsedArgs()
        var index = 0
        while index < args.count {
            let key = args[index]
            func value() -> String? {
                guard index + 1 < args.count else { return nil }
                index += 1
                return args[index]
            }
            switch key {
            case "--name": parsed.name = value()
            case "--target": parsed.target = value()
            case "--port": parsed.port = value().flatMap(Int.init)
            case "--remote": parsed.remote = value()
            case "--mountpoint": parsed.mountPoint = value()
            case "--identity": parsed.identity = value()
            case "--option": if let option = value() { parsed.options.append(option) }
            default: break
            }
            index += 1
        }
        return parsed
    }

    private static func edit(_ args: [String], store: Store, engine: MountEngine) {
        let parsed = parseAddArgs(args)
        guard let name = parsed.name, let existing = store.spec(named: name) else {
            print("找不到挂载定义（--name 必填）")
            exit(1)
        }
        let updated = MountSpec(
            id: existing.id,
            name: name,
            target: parsed.target ?? existing.target,
            port: parsed.port ?? existing.port,
            remotePath: parsed.remote ?? existing.remotePath,
            mountPoint: parsed.mountPoint ?? existing.mountPoint,
            identity: parsed.identity ?? existing.identity,
            extraOptions: parsed.options.isEmpty ? existing.extraOptions : parsed.options
        )
        store.upsert(updated)

        let active = engine.isMounted(existing) || engine.runningProcessID(existing.id) != nil
        guard active else {
            store.appendEvent("edit \(name) 已保存（未运行，未重挂）")
            print("已更新定义 \(name)（当前未挂载，未重挂）")
            return
        }
        do {
            try engine.remount(updated, previous: existing, logURL: store.logPath(for: updated))
        } catch {
            store.appendEvent("edit \(name) 重挂失败: \(error.localizedDescription)")
            print("重挂失败: \(error.localizedDescription)")
            exit(1)
        }
        let deadline = Date().addingTimeInterval(8)
        while Date() < deadline {
            if engine.isMounted(updated) {
                store.appendEvent("edit \(name) 已按新定义重挂")
                print("已按新定义重挂 \(name) → \(updated.source)")
                return
            }
            usleep(300_000)
        }
        print("重挂超时，日志:")
        engine.logTail(updated, logURL: store.logPath(for: updated)).forEach { print($0) }
        exit(1)
    }

    private static func remove(_ args: [String], store: Store, engine: MountEngine) {
        guard let name = args.first, let spec = store.spec(named: name) else {
            print("找不到挂载定义")
            exit(1)
        }
        if let error = engine.unmount(spec) {
            print("卸载失败: \(error)")
            exit(1)
        }
        store.remove(id: spec.id)
        let createdByApp = store.wasMountPointCreated(spec.expandedMountPoint)
        if let note = engine.removeMountPointIfEmpty(spec, createdByApp: createdByApp) {
            print("已删除 \(name)（\(note)）")
        } else {
            store.forgetMountPoint(spec.expandedMountPoint)
            print("已删除 \(name)，本地挂载点已清理")
        }
    }

    private static func mount(_ args: [String], store: Store, engine: MountEngine) {
        guard let name = args.first, let spec = store.spec(named: name) else {
            print("找不到挂载定义")
            exit(1)
        }
        if engine.isMounted(spec) {
            print("\(name) 已挂载")
            return
        }
        do {
            let existedBefore = engine.mountPointExists(spec)
            try engine.mount(spec, logURL: store.logPath(for: spec))
            if !existedBefore {
                store.noteMountPointCreated(spec.expandedMountPoint)
            }
        } catch {
            store.appendEvent("mount \(name) 失败: \(error.localizedDescription)")
            print("挂载失败: \(error.localizedDescription)")
            exit(1)
        }
        let deadline = Date().addingTimeInterval(8)
        while Date() < deadline {
            if engine.isMounted(spec) {
                store.appendEvent("mount \(name) 完成")
                print("已挂载 \(spec.expandedMountPoint)")
                return
            }
            usleep(300_000)
        }
        print("挂载超时，日志:")
        engine.logTail(spec, logURL: store.logPath(for: spec)).forEach { print($0) }
        exit(1)
    }

    private static func unmount(_ args: [String], store: Store, engine: MountEngine) {
        guard let name = args.first, let spec = store.spec(named: name) else {
            print("找不到挂载定义")
            exit(1)
        }
        if let error = engine.unmount(spec) {
            store.appendEvent("unmount \(name) 失败: \(error)")
            print("卸载失败: \(error)")
            exit(1)
        }
        store.appendEvent("unmount \(name) 完成")
        print("已卸载 \(spec.expandedMountPoint)")
    }

    private static func forceUnmount(_ args: [String], store: Store, engine: MountEngine) {
        guard let name = args.first, let spec = store.spec(named: name) else {
            print("找不到挂载定义")
            exit(1)
        }
        if let error = engine.forceUnmount(spec) {
            store.appendEvent("force-unmount \(name) 失败: \(error)")
            print("强制清理失败: \(error)")
            exit(1)
        }
        store.appendEvent("force-unmount \(name) 完成")
        print("已强制清理 \(spec.expandedMountPoint)")
    }
}
