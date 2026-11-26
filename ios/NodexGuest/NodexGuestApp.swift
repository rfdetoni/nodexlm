import SwiftUI
import Security
import UIKit

private func nativeString(_ pointer: UnsafeMutablePointer<CChar>?) -> String {
    guard let pointer else { return "" }
    defer { NodexIOSFree(pointer) }
    return String(cString: pointer)
}

private enum PairingStore {
    static let identity: [String: Any] = [
        kSecClass as String: kSecClassGenericPassword,
        kSecAttrService as String: "dev.nodexlm.ios.guest",
        kSecAttrAccount as String: "host"
    ]
    static func save(_ data: Data) throws {
        var attributes = identity
        attributes[kSecValueData as String] = data
        attributes[kSecAttrAccessible as String] = kSecAttrAccessibleWhenUnlockedThisDeviceOnly
        let updated = SecItemUpdate(identity as CFDictionary, [kSecValueData as String: data] as CFDictionary)
        if updated == errSecSuccess { return }
        guard updated == errSecItemNotFound, SecItemAdd(attributes as CFDictionary, nil) == errSecSuccess else {
            throw NSError(domain: "NodexLM", code: 1)
        }
    }
    static func load() -> String? {
        var query = identity
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var item: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &item) == errSecSuccess,
              let data = item as? Data else { return nil }
        return String(data: data, encoding: .utf8)
    }
    static func clear() { SecItemDelete(identity as CFDictionary) }
}

@MainActor final class Guest: ObservableObject {
    @Published var endpoint = ""
    @Published var code = ""
    @Published var status = "Stopped"
    @Published var busy = false
    @Published var running = false
    @Published var enrolled = PairingStore.load() != nil
    private var timer: Timer?
    private var stopRequested = false
    private let nativeQueue = DispatchQueue(label: "dev.nodexlm.native")
    private let socket = FileManager.default.temporaryDirectory.appendingPathComponent("nodex-rpc.sock").path

    private func reportMemory() {
        let process = ProcessInfo.processInfo
        let thermal: Int32
        switch process.thermalState {
        case .nominal: thermal = 0
        case .fair: thermal = 1
        case .serious: thermal = 3
        case .critical: thermal = 6
        @unknown default: thermal = 3
        }
        NodexIOSMemory(process.physicalMemory, NodexAvailableMemory(), Int32(process.activeProcessorCount), thermal)
        if thermal >= 3 || NodexAvailableMemory() < 512 * 1024 * 1024 { stop() }
    }

    func pair() {
        guard !busy, !running else { return }
        busy = true
        let host = endpoint.trimmingCharacters(in: .whitespacesAndNewlines)
        let secret = code
        code = ""
        let name = UIDevice.current.model
        nativeQueue.async {
            let raw = host.withCString { h in secret.withCString { c in name.withCString { n in
                nativeString(NodexIOSPair(UnsafeMutablePointer(mutating: h), UnsafeMutablePointer(mutating: c), UnsafeMutablePointer(mutating: n)))
            }}}
            let data = Data(raw.utf8)
            var message = "Pairing failed"
            var success = false
            if let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any] {
                if let error = object["error"] as? String { message = error }
                else if object["token"] is String {
                    do { try PairingStore.save(data); success = true; message = "Paired" }
                    catch { message = "Cannot save protected enrollment" }
                }
            }
            DispatchQueue.main.async {
                self.enrolled = self.enrolled || success
                self.status = message
                self.busy = false
            }
        }
    }

    func start() {
        guard !busy, !running, let credential = PairingStore.load() else { return }
        guard ProcessInfo.processInfo.thermalState.rawValue < ProcessInfo.ThermalState.serious.rawValue,
              NodexAvailableMemory() >= 512 * 1024 * 1024 else { status = "Insufficient thermal or memory headroom"; return }
        busy = true
        stopRequested = false
        status = "Starting Metal"
        reportMemory()
        let path = socket
        let name = UIDevice.current.model
        nativeQueue.async {
            guard path.withCString({ NodexMetalStart($0) }) == 1 else {
                DispatchQueue.main.async { self.status = "Metal initialization failed"; self.busy = false }
                return
            }
            let raw = credential.withCString { c in path.withCString { p in name.withCString { n in
                nativeString(NodexIOSStart(UnsafeMutablePointer(mutating: c), UnsafeMutablePointer(mutating: p), UnsafeMutablePointer(mutating: n)))
            }}}
            let object = (try? JSONSerialization.jsonObject(with: Data(raw.utf8))) as? [String: Any]
            if let error = object?["error"] as? String {
                NodexMetalStop()
                DispatchQueue.main.async { self.status = error; self.busy = false }
                return
            }
            DispatchQueue.main.async {
                self.running = true
                self.busy = false
                if self.stopRequested { self.stop(); return }
                UIApplication.shared.isIdleTimerDisabled = true
                self.timer = Timer.scheduledTimer(withTimeInterval: 2, repeats: true) { [weak self] _ in
                    Task { @MainActor in
                        guard let self, self.running else { return }
                        self.reportMemory()
                        let status = nativeString(NodexIOSStatus())
                        self.status = status
                        if NodexMetalRunning() == 0 || status.hasPrefix("Stopped") || status.hasPrefix("Guest stopped") || status.hasPrefix("Host rejected") || status.hasPrefix("Native Metal RPC did not") { self.stop() }
                    }
                }
            }
        }
    }

    func stop() {
        stopRequested = true
        guard running else { return }
        running = false
        busy = true
        timer?.invalidate()
        timer = nil
        UIApplication.shared.isIdleTimerDisabled = false
        nativeQueue.async {
            NodexIOSStop()
            NodexMetalStop()
            DispatchQueue.main.async { self.status = "Stopped"; self.busy = false }
        }
    }

    func forget() {
        guard !busy, !running else { return }
        PairingStore.clear()
        enrolled = false
        status = "Enrollment removed"
    }
}

@main struct NodexGuestApp: App {
    @StateObject private var guest = Guest()
    @Environment(\.scenePhase) private var phase
    var body: some Scene {
        WindowGroup {
            NavigationStack {
                Form {
                    Section("Host enrollment") {
                        TextField("https://192.168.1.5:8443", text: $guest.endpoint)
                            .textContentType(.URL).autocapitalization(.none).disableAutocorrection(true)
                        SecureField("Temporary pairing code", text: $guest.code)
                        Button("Pair") { guest.pair() }.disabled(guest.busy || guest.running)
                        Button("Forget enrollment", role: .destructive) { guest.forget() }.disabled(guest.busy || guest.running)
                    }
                    Section("Metal Guest") {
                        Text(guest.status)
                        Button("Start") { guest.start() }.disabled(!guest.enrolled || guest.busy || guest.running)
                        Button("Stop") { guest.stop() }.disabled(!guest.running && !guest.busy)
                        Text("Keep this app in the foreground. Leaving the app or exceeding memory/thermal limits stops the Guest.")
                    }
                }.navigationTitle("NodexLM Guest")
            }
            .onChange(of: phase) { _, value in if value != .active { guest.stop() } }
            .onReceive(NotificationCenter.default.publisher(for: UIApplication.didReceiveMemoryWarningNotification)) { _ in guest.stop() }
        }
    }
}
