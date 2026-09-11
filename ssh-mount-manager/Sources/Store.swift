import Combine
import Foundation

final class Store: ObservableObject {
    @Published var specs: [MountSpec] = []

    let baseDir: URL
    let logDir: URL
    let eventsURL: URL
    private let configURL: URL

    init(baseDir base: URL? = nil) {
        let root = base ?? FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".ssh-mount-manager", isDirectory: true)
        baseDir = root
        logDir = root.appendingPathComponent("logs", isDirectory: true)
        eventsURL = root.appendingPathComponent("events.log")
        configURL = root.appendingPathComponent("mounts.json")
        try? FileManager.default.createDirectory(at: logDir, withIntermediateDirectories: true)
        load()
    }

    func load() {
        guard let data = try? Data(contentsOf: configURL) else { return }
        specs = (try? JSONDecoder().decode([MountSpec].self, from: data)) ?? []
    }

    func save() {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        guard let data = try? encoder.encode(specs) else { return }
        try? FileManager.default.createDirectory(at: baseDir, withIntermediateDirectories: true)
        try? data.write(to: configURL, options: .atomic)
    }

    func upsert(_ spec: MountSpec) {
        if let index = specs.firstIndex(where: { $0.id == spec.id }) {
            specs[index] = spec
        } else {
            specs.append(spec)
        }
        save()
    }

    func remove(id: UUID) {
        specs.removeAll { $0.id == id }
        save()
    }

    func spec(named name: String) -> MountSpec? {
        specs.first { $0.name == name }
    }

    func logPath(for spec: MountSpec) -> URL {
        logDir.appendingPathComponent("\(spec.name).log")
    }

    func appendEvent(_ text: String) {
        let formatter = DateFormatter()
        formatter.dateFormat = "yyyy-MM-dd HH:mm:ss"
        let line = "\(formatter.string(from: Date())) \(text)\n"
        guard let data = line.data(using: .utf8) else { return }
        try? FileManager.default.createDirectory(at: baseDir, withIntermediateDirectories: true)
        if let handle = try? FileHandle(forWritingTo: eventsURL) {
            handle.seekToEndOfFile()
            handle.write(data)
            try? handle.close()
        } else {
            try? data.write(to: eventsURL)
        }
    }
}
