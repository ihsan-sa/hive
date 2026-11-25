// swift test/macbanner.swift BUNDLE-ID SHOT.png... : what macOS itself says about an app's notifications, for test/macdl.js.
// First line: the app's entry in macOS's notification settings (com.apple.ncprefs) in words, or that it has none. Then, for
// each screenshot, the text on it as macOS's own text recognition (Vision) reads it, one line per shot, parts joined by " | ".
// swift test/macbanner.swift --allow BUNDLE-ID : turns "Allow notifications" on in that entry (the caller restarts usernoted).
// The flags bits are not documented by Apple; this reads them as Kolide measured them by toggling each setting
// (kolide.com/blog/are-your-employees-slack-messages-leaking-while-their-screen-is-locked): bit 25 allow, 3 banners, 4 alerts,
// 2 sound, 1 badge, 12 hide on the lock screen, 0 keep out of Notification Center.
import Foundation
import AppKit
import Vision
var args = Array(CommandLine.arguments.dropFirst())
let allow = args.first == "--allow"
if allow { args.removeFirst() }
let id = args.first ?? ""
let domain = "com.apple.ncprefs" as CFString
func words(_ f: Int) -> String {
  let on = { (b: Int) in (f >> b) & 1 == 1 }
  let style = on(4) ? "alerts" : on(3) ? "banners" : "none"
  return "allow notifications \(on(25) ? "on" : "OFF"), style \(style), sound \(on(2) ? "on" : "off"), badge \(on(1) ? "on" : "off"), "
    + "lock screen \(on(12) ? "hidden" : "shown"), Notification Center \(on(0) ? "kept out" : "listed")"
}
guard var apps = CFPreferencesCopyAppValue("apps" as CFString, domain) as? [[String: Any]] else { print("ncprefs: no apps list in com.apple.ncprefs"); exit(allow ? 1 : 0) }
let idx = apps.indices.filter { (apps[$0]["bundle-id"] as? String) == id }
if allow {
  guard let i = idx.first, let f = apps[i]["flags"] as? Int else { print("ncprefs: no entry for \(id) to turn on"); exit(1) }
  apps[i]["flags"] = f | (1 << 25)
  CFPreferencesSetAppValue("apps" as CFString, apps as CFArray, domain)
  print(CFPreferencesAppSynchronize(domain) ? "ncprefs: turned on for \(id): \(words(f | (1 << 25)))" : "ncprefs: could not write com.apple.ncprefs")
  exit(0)
}
if idx.isEmpty { print("ncprefs: no entry for \(id) among \(apps.count) apps") }
for i in idx {
  let m = apps[i], f = m["flags"] as? Int ?? 0
  print("ncprefs: \(words(f)) (flags=\(f) auth=\(m["auth"] ?? "none"))")
}
for f in args.dropFirst() {
  guard let img = NSImage(contentsOfFile: f), let cg = img.cgImage(forProposedRect: nil, context: nil, hints: nil) else { print("ocr \(f): unreadable"); continue }
  let req = VNRecognizeTextRequest()
  req.recognitionLevel = .accurate
  do { try VNImageRequestHandler(cgImage: cg, options: [:]).perform([req]) } catch { print("ocr \(f): failed \(error)"); continue }
  let lines = (req.results ?? []).compactMap { $0.topCandidates(1).first?.string }
  print("ocr \(f): " + lines.joined(separator: " | "))
}
