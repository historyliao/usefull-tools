import Foundation

final class AppState: ObservableObject {
    static let shared = AppState()

    @Published private(set) var items: [ProxyItem] = []
    @Published private(set) var targets: [SSHTarget] = []
    @Published var selection: String?
    @Published private(set) var logs: [String] = []
    @Published private(set) var kernelReady = false
    @Published private(set) var kernelPid: Int?
    @Published private(set) var attachedToExternal = false
    @Published private(set) var busy: Set<String> = []
    @Published private(set) var status = ""
    @Published private(set) var statusIsError = false
    @Published var editing: ProxyDefinition?
    @Published var isCreating = false
    @Published var managingTargets = false

    private let kernel = Kernel.shared
    private let work = DispatchQueue(label: "spm.appstate", qos: .userInitiated)
    private var timer: Timer?
    private var refreshing = false

    var reverseItems: [ProxyItem] { items.filter { $0.definition.direction == .reverse } }
    var forwardItems: [ProxyItem] { items.filter { $0.definition.direction == .forward } }
    var runningCount: Int { items.filter { $0.runtime.isActive }.count }

    func target(named name: String) -> SSHTarget? {
        targets.first { $0.name == name }
    }

    /// 列表/详情里展示的 SSH 目标：引用了命名目标时显示解析结果，并标注来源。
    func displayTarget(_ definition: ProxyDefinition) -> String {
        let ref = definition.targetRef.trimmingCharacters(in: .whitespaces)
        guard !ref.isEmpty else { return definition.target.address }
        guard let target = target(named: ref) else { return "复用 \(ref)（目标不存在）" }
        return "\(target.address) · 复用 \(target.name)"
    }

    func displayIdentity(_ definition: ProxyDefinition) -> String {
        let ref = definition.targetRef.trimmingCharacters(in: .whitespaces)
        if !ref.isEmpty, let target = target(named: ref) { return target.identity }
        return definition.target.identity
    }

    func refreshTargets() {
        work.async {
            guard let response = try? self.kernel.client.call(.targets()) else { return }
            let targets = (response.targets ?? []).sorted { $0.name < $1.name }
            DispatchQueue.main.async { self.targets = targets }
        }
    }

    func saveTarget(_ target: SSHTarget, isNew: Bool) {
        work.async {
            do {
                let response = try self.kernel.client.call(.saveTarget(target, isNew: isNew))
                DispatchQueue.main.async {
                    self.setStatus(response.message ?? "SSH 目标已保存", isError: false)
                    self.refreshTargets()
                }
            } catch {
                DispatchQueue.main.async { self.setStatus(error.localizedDescription, isError: true) }
            }
        }
    }

    func deleteTarget(_ name: String) {
        work.async {
            do {
                let response = try self.kernel.client.call(.deleteTarget(name: name))
                DispatchQueue.main.async {
                    self.setStatus(response.message ?? "SSH 目标已删除", isError: false)
                    self.refreshTargets()
                }
            } catch {
                DispatchQueue.main.async { self.setStatus(error.localizedDescription, isError: true) }
            }
        }
    }

    var selectedItem: ProxyItem? {
        guard let selection else { return nil }
        return items.first { $0.definition.name == selection }
    }

    var kernelDescription: String {
        if kernelReady {
            let pid = kernelPid.map { " pid \($0)" } ?? ""
            return attachedToExternal ? "已附加到外部内核\(pid)" : "内核运行中\(pid)"
        }
        return "内核未运行"
    }

    func start() {
        guard timer == nil else { return }
        bootKernel()
        timer = Timer.scheduledTimer(withTimeInterval: 1.5, repeats: true) { [weak self] _ in
            self?.refresh()
        }
    }

    func bootKernel() {
        work.async {
            do {
                try self.kernel.ensureRunning()
                let pong = self.kernel.ping()
                DispatchQueue.main.async {
                    self.kernelReady = true
                    self.kernelPid = pong?.managerPID
                    self.attachedToExternal = self.kernel.attachedToExternal
                    self.setStatus(self.attachedToExternal ? "已连接到已在运行的内核" : "内核已启动", isError: false)
                    self.refresh()
                }
            } catch {
                DispatchQueue.main.async {
                    self.kernelReady = false
                    self.setStatus(error.localizedDescription, isError: true)
                }
            }
        }
    }

    func refresh() {
        work.async {
            if self.refreshing { return }
            self.refreshing = true
            defer { self.refreshing = false }
            do {
                let response = try self.kernel.client.call(.list())
                let items = (response.rows ?? []).map {
                    ProxyItem(definition: $0.definition, runtime: $0.runtime)
                }
                let targetResponse = try? self.kernel.client.call(.targets())
                let targets = (targetResponse?.targets ?? []).sorted { $0.name < $1.name }
                DispatchQueue.main.async {
                    self.items = items
                    self.targets = targets
                    self.kernelReady = true
                    if let selection = self.selection, !items.contains(where: { $0.definition.name == selection }) {
                        self.selection = items.first?.definition.name
                    } else if self.selection == nil {
                        self.selection = items.first?.definition.name
                    }
                    self.loadLogs()
                }
            } catch {
                DispatchQueue.main.async {
                    self.kernelReady = false
                }
            }
        }
    }

    func loadLogs() {
        guard let name = selection else {
            logs = []
            return
        }
        work.async {
            let lines = (try? self.kernel.client.call(.logs(name: name, lines: 400)))?.logs ?? []
            DispatchQueue.main.async {
                if self.selection == name {
                    self.logs = lines
                }
            }
        }
    }

    func start(_ name: String) { operate("start", name: name) }
    func stop(_ name: String) { operate("stop", name: name) }
    func restart(_ name: String) { operate("restart", name: name) }
    func remove(_ name: String) { operate("delete", name: name) }

    func startAll() {
        let names = items.filter { !$0.runtime.isActive }.map(\.definition.name)
        operateMany("start", names: names)
    }

    func stopAll() {
        let names = items.filter { $0.runtime.isActive }.map(\.definition.name)
        operateMany("stop", names: names)
    }

    func save(_ definition: ProxyDefinition, isNew: Bool) {
        busy.insert(definition.name)
        work.async {
            var message = "已保存 \(definition.name)"
            var isError = false
            do {
                let response = try self.kernel.client.call(.save(definition, isNew: isNew))
                message = response.message ?? message
            } catch {
                message = error.localizedDescription
                isError = true
            }
            DispatchQueue.main.async {
                self.busy.remove(definition.name)
                self.setStatus(message, isError: isError)
                if !isError {
                    self.selection = definition.name
                    self.editing = nil
                }
                self.refresh()
            }
        }
    }

    func openEditor(for item: ProxyItem?) {
        if let item {
            editing = item.definition
            isCreating = false
        } else {
            editing = ProxyDefinition(
                name: "",
                direction: .reverse,
                target: Target(user: "", host: "", port: 22),
                forwards: [Forward(type: "R", bindPort: 0, destPort: 0)],
                restart: .onFailure,
                backoff: Backoff(),
                autostart: true
            )
            isCreating = true
        }
    }

    func shutdown() {
        timer?.invalidate()
        timer = nil
        kernel.shutdown()
    }

    private func operate(_ cmd: String, name: String) {
        operateMany(cmd, names: [name])
    }

    private func operateMany(_ cmd: String, names: [String]) {
        guard !names.isEmpty else { return }
        for name in names {
            busy.insert(name)
        }
        work.async {
            var failures: [String] = []
            var lastMessage = ""
            for name in names {
                do {
                    let response = try self.kernel.client.call(.operate(cmd, name: name))
                    lastMessage = response.message ?? "\(name) 完成"
                } catch {
                    failures.append("\(name): \(error.localizedDescription)")
                }
            }
            let message: String
            let isError: Bool
            if failures.isEmpty {
                message = names.count == 1 ? lastMessage : "已处理 \(names.count) 条"
                isError = false
            } else {
                message = failures.joined(separator: "；")
                isError = true
            }
            DispatchQueue.main.async {
                for name in names {
                    self.busy.remove(name)
                }
                self.setStatus(message, isError: isError)
                self.refresh()
            }
        }
    }

    private func setStatus(_ message: String, isError: Bool) {
        status = message
        statusIsError = isError
    }
}
