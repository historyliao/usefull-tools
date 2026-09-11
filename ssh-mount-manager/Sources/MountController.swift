import AppKit
import Combine
import Foundation

final class MountController: ObservableObject {
    @Published private(set) var statuses: [UUID: MountStatus] = [:]
    @Published private(set) var logs: [UUID: [String]] = [:]
    @Published var message: String = ""

    let store: Store
    let engine = MountEngine()

    private let queue = DispatchQueue(label: "ssh-mount-manager.engine", qos: .userInitiated)
    private var timer: Timer?
    private var failures: [UUID: String] = [:]
    private var refreshing = false

    init(store: Store) {
        self.store = store
        refresh()
        let ticker = Timer.scheduledTimer(withTimeInterval: 2, repeats: true) { [weak self] _ in
            self?.refresh()
        }
        RunLoop.main.add(ticker, forMode: .common)
        timer = ticker
    }

    func status(of spec: MountSpec) -> MountStatus {
        statuses[spec.id] ?? .unmounted
    }

    func refresh() {
        guard !refreshing else { return }
        refreshing = true
        let specs = store.specs
        let knownFailures = failures
        queue.async { [weak self] in
            guard let self else { return }
            let points = self.engine.mountPoints()
            var updated: [UUID: MountStatus] = [:]
            for spec in specs {
                if self.engine.isMounted(spec, in: points) {
                    updated[spec.id] = .mounted(external: self.engine.runningProcessID(spec.id) == nil)
                } else if self.engine.runningProcessID(spec.id) != nil {
                    updated[spec.id] = .mounting
                } else if let failure = knownFailures[spec.id] {
                    updated[spec.id] = .failed(failure)
                } else {
                    updated[spec.id] = .unmounted
                }
            }
            let knownIDs = Set(specs.map(\.id))
            DispatchQueue.main.async {
                self.statuses = updated
                self.logs = self.logs.filter { knownIDs.contains($0.key) }
                self.failures = self.failures.filter { knownIDs.contains($0.key) }
                self.refreshing = false
            }
        }
    }

    func mount(_ spec: MountSpec) {
        store.appendEvent("mount \(spec.name) 请求")
        failures[spec.id] = nil
        statuses[spec.id] = .mounting
        message = "\(spec.name) 挂载中…"
        let logURL = store.logPath(for: spec)
        queue.async { [weak self] in
            guard let self else { return }
            do {
                try self.engine.mount(spec, logURL: logURL)
            } catch {
                DispatchQueue.main.async {
                    self.store.appendEvent("mount \(spec.name) 失败: \(error.localizedDescription)")
                    self.failures[spec.id] = error.localizedDescription
                    self.statuses[spec.id] = .failed(error.localizedDescription)
                    self.message = "\(spec.name) 挂载失败: \(error.localizedDescription)"
                    self.logs[spec.id] = self.engine.logTail(spec, logURL: logURL)
                }
                return
            }
            Thread.sleep(forTimeInterval: 0.8)
            DispatchQueue.main.async {
                self.store.appendEvent("mount \(spec.name) 已发起，状态 \(self.status(of: spec).label)")
                self.refresh()
                self.loadLog(spec)
            }
        }
    }

    func unmount(_ spec: MountSpec) {
        store.appendEvent("unmount \(spec.name) 请求")
        message = "\(spec.name) 卸载中…"
        queue.async { [weak self] in
            guard let self else { return }
            let error = self.engine.unmount(spec)
            DispatchQueue.main.async {
                if let error {
                    self.store.appendEvent("unmount \(spec.name) 失败: \(error)")
                    self.failures[spec.id] = error
                    self.statuses[spec.id] = .failed(error)
                    self.message = "\(spec.name) 卸载失败: \(error)"
                } else {
                    self.store.appendEvent("unmount \(spec.name) 完成")
                    self.failures[spec.id] = nil
                    self.statuses[spec.id] = .unmounted
                    self.message = "\(spec.name) 已卸载"
                }
                self.refresh()
                self.loadLog(spec)
            }
        }
    }

    func toggle(_ spec: MountSpec) {
        if status(of: spec).isMounted || engine.runningProcessID(spec.id) != nil {
            unmount(spec)
        } else {
            mount(spec)
        }
    }

    func forceUnmount(_ spec: MountSpec) {
        store.appendEvent("force-unmount \(spec.name) 请求")
        message = "\(spec.name) 强制清理中…"
        queue.async { [weak self] in
            guard let self else { return }
            let error = self.engine.forceUnmount(spec)
            DispatchQueue.main.async {
                self.store.appendEvent("force-unmount \(spec.name) 结果: \(error ?? "已清理")")
                self.failures[spec.id] = error
                self.statuses[spec.id] = error.map { MountStatus.failed($0) } ?? .unmounted
                self.message = error.map { "\(spec.name) 强制清理后仍有问题: \($0)" } ?? "\(spec.name) 已强制清理"
                self.refresh()
            }
        }
    }

    func handleSaved(_ updated: MountSpec, previous: MountSpec?) {
        guard let previous else {
            store.appendEvent("save \(updated.name) 新建定义")
            return
        }
        store.appendEvent("save \(updated.name) 编辑定义")
        let active = engine.isMounted(previous) || engine.runningProcessID(previous.id) != nil
        guard active else { return }
        let relevantChange = previous.mountPoint != updated.mountPoint
            || previous.source != updated.source
            || previous.port != updated.port
            || previous.identity != updated.identity
            || previous.extraOptions != updated.extraOptions
            || previous.name != updated.name
        guard relevantChange else { return }

        message = "\(updated.name) 定义已更新，正在按新定义重挂…"
        let logURL = store.logPath(for: updated)
        queue.async { [weak self] in
            guard let self else { return }
            do {
                try self.engine.remount(updated, previous: previous, logURL: logURL)
            } catch {
                DispatchQueue.main.async {
                    self.failures[updated.id] = error.localizedDescription
                    self.statuses[updated.id] = .failed(error.localizedDescription)
                    self.store.appendEvent("save \(updated.name) 重挂失败: \(error.localizedDescription)")
                    self.message = "\(updated.name) 重挂失败: \(error.localizedDescription)"
                    self.refresh()
                }
                return
            }
            Thread.sleep(forTimeInterval: 0.8)
            DispatchQueue.main.async {
                self.store.appendEvent("save \(updated.name) 已按新定义重挂")
                self.message = "\(updated.name) 已按新定义重挂"
                self.refresh()
                self.loadLog(updated)
            }
        }
    }

    /// 退出时同步清理，必须走同步路径，否则进程先退出了。
    func shutdown() {
        guard UserDefaults.standard.object(forKey: SettingsKeys.unmountOnQuit) as? Bool ?? true else { return }
        for spec in store.specs where engine.isMounted(spec) || engine.runningProcessID(spec.id) != nil {
            _ = engine.unmount(spec)
        }
    }

    func remove(_ spec: MountSpec) {
        let wasMounted = status(of: spec).isMounted || engine.runningProcessID(spec.id) != nil
        store.remove(id: spec.id)
        statuses[spec.id] = nil
        logs[spec.id] = nil
        failures[spec.id] = nil
        message = "\(spec.name) 已删除"
        queue.async { [weak self] in
            guard let self else { return }
            if wasMounted {
                _ = self.engine.unmount(spec)
            }
            let note = self.engine.removeMountPointIfEmpty(spec)
            DispatchQueue.main.async {
                self.store.appendEvent("delete \(spec.name)\(note.map { "（\($0)）" } ?? "，已清理本地空挂载点")")
                self.message = note.map { "\(spec.name) 已删除：\($0)" } ?? "\(spec.name) 已删除（本地挂载点已清理）"
                self.refresh()
            }
        }
    }

    func loadLog(_ spec: MountSpec) {
        logs[spec.id] = engine.logTail(spec, logURL: store.logPath(for: spec))
    }

    func openInFinder(_ spec: MountSpec) {
        NSWorkspace.shared.open(URL(fileURLWithPath: spec.expandedMountPoint))
    }

    func revealLog(_ spec: MountSpec) {
        NSWorkspace.shared.activateFileViewerSelecting([store.logPath(for: spec)])
    }

    func unmountedCount() -> Int {
        store.specs.filter { !status(of: $0).isMounted }.count
    }
}

enum SettingsKeys {
    static let unmountOnQuit = "unmountOnQuit"
}

final class AppState {
    static let store = Store()
    static let controller = MountController(store: store)
}
