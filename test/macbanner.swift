// swift test/macbanner.swift BUNDLE-ID SHOT.png... : what macOS itself says about an app's notifications, for test/macdl.js.
// First line: the app's entry in macOS's notification settings (com.apple.ncprefs), or that it has none. Then, for each
// screenshot, the text on it as macOS's own text recognition (Vision) reads it, one line per shot, parts joined by " | ".
import Foundation
import AppKit
import Vision
let args = Array(CommandLine.arguments.dropFirst())
let id = args.first ?? ""
if let apps = CFPreferencesCopyAppValue("apps" as CFString, "com.apple.ncprefs" as CFString) as? [[String: Any]] {
  let mine = apps.filter { ($0["bundle-id"] as? String) == id }
  if mine.isEmpty { print("ncprefs: no entry for \(id) among \(apps.count) apps") }
  for m in mine { print("ncprefs: " + m.filter { !($0.value is Data) }.map { "\($0.key)=\($0.value)" }.sorted().joined(separator: " ")) }
} else { print("ncprefs: no apps list in com.apple.ncprefs") }
for f in args.dropFirst() {
  guard let img = NSImage(contentsOfFile: f), let cg = img.cgImage(forProposedRect: nil, context: nil, hints: nil) else { print("ocr \(f): unreadable"); continue }
  let req = VNRecognizeTextRequest()
  req.recognitionLevel = .accurate
  do { try VNImageRequestHandler(cgImage: cg, options: [:]).perform([req]) } catch { print("ocr \(f): failed \(error)"); continue }
  let lines = (req.results ?? []).compactMap { $0.topCandidates(1).first?.string }
  print("ocr \(f): " + lines.joined(separator: " | "))
}
