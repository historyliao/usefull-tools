import Foundation

struct ControlRequest: Encodable {
    var cmd: String
    var name: String?
    var lines: Int?
    var definition: ProxyDefinition?

    static func ping() -> ControlRequest { ControlRequest(cmd: "ping") }
    static func list() -> ControlRequest { ControlRequest(cmd: "list") }
    static func logs(name: String, lines: Int) -> ControlRequest {
        ControlRequest(cmd: "logs", name: name, lines: lines)
    }
    static func operate(_ cmd: String, name: String) -> ControlRequest {
        ControlRequest(cmd: cmd, name: name)
    }
    static func save(_ definition: ProxyDefinition, isNew: Bool) -> ControlRequest {
        ControlRequest(cmd: isNew ? "add" : "update", definition: definition)
    }
}

struct ControlRow: Decodable {
    var definition: ProxyDefinition
    var runtime: ProxyRuntime
}

struct ControlResponse: Decodable {
    var ok: Bool
    var error: String?
    var message: String?
    var version: String?
    var managerPID: Int?
    var rows: [ControlRow]?
    var logs: [String]?

    enum CodingKeys: String, CodingKey {
        case ok
        case error
        case message
        case version
        case rows
        case logs
        case managerPID = "manager_pid"
    }
}

enum ControlError: LocalizedError {
    case notRunning
    case remote(String)
    case transport(String)
    case tooLong

    var errorDescription: String? {
        switch self {
        case .notRunning: return "内核未运行"
        case .remote(let message): return message
        case .transport(let message): return "控制通道异常: \(message)"
        case .tooLong: return "控制通道路径过长"
        }
    }
}

/// 与 Go 内核的 unix socket 控制通道：一次连接一个请求/响应。
struct ControlClient {
    let socketPath: String

    func call(_ request: ControlRequest) throws -> ControlResponse {
        let fd = socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else {
            throw ControlError.transport("socket() 失败: \(errno)")
        }
        defer { close(fd) }

        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        address.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
        let pathBytes = Array(socketPath.utf8CString)
        guard pathBytes.count <= MemoryLayout.size(ofValue: address.sun_path) else {
            throw ControlError.tooLong
        }
        withUnsafeMutablePointer(to: &address.sun_path) { pointer in
            pointer.withMemoryRebound(to: CChar.self, capacity: pathBytes.count) { destination in
                for (index, byte) in pathBytes.enumerated() {
                    destination[index] = byte
                }
            }
        }

        var timeout = timeval(tv_sec: 30, tv_usec: 0)
        setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &timeout, socklen_t(MemoryLayout<timeval>.size))
        setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &timeout, socklen_t(MemoryLayout<timeval>.size))

        let connected = withUnsafePointer(to: &address) { pointer in
            pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) { sockaddrPointer in
                connect(fd, sockaddrPointer, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard connected == 0 else {
            throw ControlError.notRunning
        }

        let payload = try JSONEncoder().encode(request)
        try write(fd: fd, data: payload)
        _ = shutdown(fd, SHUT_WR)
        let data = try readAll(fd: fd)
        guard !data.isEmpty else {
            throw ControlError.transport("内核没有返回内容")
        }
        let response = try JSONDecoder().decode(ControlResponse.self, from: data)
        guard response.ok else {
            throw ControlError.remote(response.error ?? "操作失败")
        }
        return response
    }

    private func write(fd: Int32, data: Data) throws {
        var offset = 0
        let bytes = [UInt8](data)
        while offset < bytes.count {
            let written = bytes.withUnsafeBytes { buffer -> Int in
                Darwin.write(fd, buffer.baseAddress!.advanced(by: offset), bytes.count - offset)
            }
            if written <= 0 {
                throw ControlError.transport("写入失败: \(errno)")
            }
            offset += written
        }
    }

    private func readAll(fd: Int32) throws -> Data {
        var data = Data()
        var buffer = [UInt8](repeating: 0, count: 8192)
        while true {
            let count = read(fd, &buffer, buffer.count)
            if count > 0 {
                data.append(contentsOf: buffer[0..<count])
                continue
            }
            if count == 0 { break }
            if errno == EINTR { continue }
            throw ControlError.transport("读取失败: \(errno)")
        }
        return data
    }
}
