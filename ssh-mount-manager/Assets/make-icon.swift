import AppKit
import CoreGraphics
import CoreText
import Foundation

// 用法: swift Assets/make-icon.swift <mount|proxy>
// 输出: Assets/icon-1024.png 与 Assets/AppIcon.icns

let kind = CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : "mount"

struct Palette {
    let top: CGColor
    let bottom: CGColor
}

let palettes: [String: Palette] = [
    "mount": Palette(
        top: CGColor(srgbRed: 0.42, green: 0.65, blue: 1.00, alpha: 1),
        bottom: CGColor(srgbRed: 0.13, green: 0.32, blue: 0.85, alpha: 1)
    ),
    "proxy": Palette(
        top: CGColor(srgbRed: 0.28, green: 0.83, blue: 0.72, alpha: 1),
        bottom: CGColor(srgbRed: 0.04, green: 0.52, blue: 0.47, alpha: 1)
    ),
]

guard let palette = palettes[kind] else {
    FileHandle.standardError.write(Data("unknown kind: \(kind)\n".utf8))
    exit(2)
}

let assets = URL(fileURLWithPath: FileManager.default.currentDirectoryPath).appendingPathComponent("Assets")
try? FileManager.default.createDirectory(at: assets, withIntermediateDirectories: true)

let canvas: CGFloat = 1024
let margin: CGFloat = 100
let body = CGRect(x: margin, y: margin, width: canvas - margin * 2, height: canvas - margin * 2)
let bodyRadius: CGFloat = 186
let badge = CGRect(x: 352, y: 698, width: 320, height: 102)

func white(_ alpha: CGFloat) -> CGColor {
    CGColor(srgbRed: 1, green: 1, blue: 1, alpha: alpha)
}

func roundedPath(_ rect: CGRect, radius: CGFloat) -> CGPath {
    CGPath(roundedRect: rect, cornerWidth: radius, cornerHeight: radius, transform: nil)
}

func fillPath(_ ctx: CGContext, _ path: CGPath, _ color: CGColor) {
    ctx.saveGState()
    ctx.addPath(path)
    ctx.setFillColor(color)
    ctx.fillPath()
    ctx.restoreGState()
}

func drawMountGlyph(_ ctx: CGContext) {
    let arrow = CGMutablePath()
    arrow.addRoundedRect(in: CGRect(x: 482, y: 190, width: 60, height: 200), cornerWidth: 30, cornerHeight: 30)
    arrow.move(to: CGPoint(x: 512, y: 476))
    arrow.addLine(to: CGPoint(x: 370, y: 368))
    arrow.addLine(to: CGPoint(x: 654, y: 368))
    arrow.closeSubpath()
    fillPath(ctx, arrow, white(0.97))

    let drive = roundedPath(CGRect(x: 258, y: 512, width: 508, height: 158), radius: 46)
    fillPath(ctx, drive, white(0.97))

    let slot = roundedPath(CGRect(x: 606, y: 632, width: 116, height: 18), radius: 9)
    fillPath(ctx, slot, palette.bottom.copy(alpha: 0.45)!)

    let led = CGPath(ellipseIn: CGRect(x: 306, y: 626, width: 30, height: 30), transform: nil)
    fillPath(ctx, led, palette.bottom.copy(alpha: 0.45)!)
}

func drawProxyGlyph(_ ctx: CGContext) {
    let right = CGMutablePath()
    right.addRoundedRect(in: CGRect(x: 312, y: 268, width: 330, height: 58), cornerWidth: 29, cornerHeight: 29)
    right.move(to: CGPoint(x: 786, y: 297))
    right.addLine(to: CGPoint(x: 648, y: 182))
    right.addLine(to: CGPoint(x: 648, y: 412))
    right.closeSubpath()
    fillPath(ctx, right, white(0.97))

    let left = CGMutablePath()
    left.addRoundedRect(in: CGRect(x: 382, y: 512, width: 330, height: 58), cornerWidth: 29, cornerHeight: 29)
    left.move(to: CGPoint(x: 238, y: 541))
    left.addLine(to: CGPoint(x: 376, y: 426))
    left.addLine(to: CGPoint(x: 376, y: 656))
    left.closeSubpath()
    fillPath(ctx, left, white(0.97))
}

func drawBadge(_ ctx: CGContext) {
    let pill = roundedPath(badge, radius: badge.height / 2)
    fillPath(ctx, pill, white(0.20))
    ctx.saveGState()
    ctx.addPath(pill)
    ctx.setStrokeColor(white(0.34))
    ctx.setLineWidth(3)
    ctx.strokePath()
    ctx.restoreGState()
}

// 文字单独在未翻转的坐标里绘制（左下原点），避免随参考系镜像
func drawSSHText(_ ctx: CGContext, size: CGFloat) {
    let scale = size / canvas
    let attributes: [NSAttributedString.Key: Any] = [
        .font: NSFont.systemFont(ofSize: 76 * scale, weight: .bold),
        .foregroundColor: NSColor.white.withAlphaComponent(0.97),
        .kern: 12 * scale,
    ]
    let line = CTLineCreateWithAttributedString(NSAttributedString(string: "SSH", attributes: attributes))
    let bounds = CTLineGetBoundsWithOptions(line, .useOpticalBounds)
    let centerX = badge.midX * scale
    let centerY = (canvas - badge.midY) * scale
    ctx.saveGState()
    ctx.textMatrix = .identity
    ctx.textPosition = CGPoint(x: centerX - bounds.width / 2 - 6 * scale, y: centerY - bounds.height / 2)
    CTLineDraw(line, ctx)
    ctx.restoreGState()
}

func drawIcon(_ ctx: CGContext, size: CGFloat) {
    ctx.saveGState()
    ctx.setShouldAntialias(true)
    ctx.interpolationQuality = .high
    ctx.translateBy(x: 0, y: size)
    ctx.scaleBy(x: 1, y: -1)
    let scale = size / canvas
    ctx.scaleBy(x: scale, y: scale)

    let shape = roundedPath(body, radius: bodyRadius)

    ctx.saveGState()
    ctx.setShadow(offset: CGSize(width: 0, height: 16), blur: 34, color: CGColor(srgbRed: 0, green: 0, blue: 0, alpha: 0.30))
    ctx.addPath(shape)
    ctx.setFillColor(palette.bottom)
    ctx.fillPath()
    ctx.restoreGState()

    ctx.saveGState()
    ctx.addPath(shape)
    ctx.clip()

    if let gradient = CGGradient(colorsSpace: CGColorSpaceCreateDeviceRGB(),
                                 colors: [palette.top, palette.bottom] as CFArray,
                                 locations: [0, 1]) {
        ctx.drawLinearGradient(gradient,
                               start: CGPoint(x: body.minX, y: body.minY),
                               end: CGPoint(x: body.maxX, y: body.maxY),
                               options: [])
    }

    if let highlight = CGGradient(colorsSpace: CGColorSpaceCreateDeviceRGB(),
                                  colors: [white(0.22), white(0.0)] as CFArray,
                                  locations: [0, 1]) {
        ctx.drawLinearGradient(highlight,
                               start: CGPoint(x: body.minX, y: body.minY),
                               end: CGPoint(x: body.minX, y: body.minY + body.height * 0.62),
                               options: [])
    }

    if let shade = CGGradient(colorsSpace: CGColorSpaceCreateDeviceRGB(),
                              colors: [CGColor(srgbRed: 0, green: 0, blue: 0, alpha: 0), CGColor(srgbRed: 0, green: 0, blue: 0, alpha: 0.16)] as CFArray,
                              locations: [0, 1]) {
        ctx.drawLinearGradient(shade,
                               start: CGPoint(x: body.minX, y: body.maxY - body.height * 0.35),
                               end: CGPoint(x: body.minX, y: body.maxY),
                               options: [])
    }
    ctx.restoreGState()

    ctx.addPath(shape)
    ctx.setStrokeColor(white(0.28))
    ctx.setLineWidth(3)
    ctx.strokePath()

    if kind == "mount" {
        drawMountGlyph(ctx)
    } else {
        drawProxyGlyph(ctx)
    }
    drawBadge(ctx)
    ctx.restoreGState()
}

func png(size: CGFloat) -> Data? {
    let pixels = Int(size)
    guard let ctx = CGContext(data: nil,
                              width: pixels,
                              height: pixels,
                              bitsPerComponent: 8,
                              bytesPerRow: 0,
                              space: CGColorSpaceCreateDeviceRGB(),
                              bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return nil }
    drawIcon(ctx, size: size)
    drawSSHText(ctx, size: size)
    guard let image = ctx.makeImage() else { return nil }
    let rep = NSBitmapImageRep(cgImage: image)
    rep.size = NSSize(width: pixels, height: pixels)
    return rep.representation(using: .png, properties: [:])
}

let iconset = assets.appendingPathComponent("AppIcon.iconset")
try? FileManager.default.removeItem(at: iconset)
try? FileManager.default.createDirectory(at: iconset, withIntermediateDirectories: true)

let variants: [(String, CGFloat)] = [
    ("icon_16x16.png", 16), ("icon_16x16@2x.png", 32),
    ("icon_32x32.png", 32), ("icon_32x32@2x.png", 64),
    ("icon_128x128.png", 128), ("icon_128x128@2x.png", 256),
    ("icon_256x256.png", 256), ("icon_256x256@2x.png", 512),
    ("icon_512x512.png", 512), ("icon_512x512@2x.png", 1024),
]

for (name, size) in variants {
    guard let data = png(size: size) else {
        FileHandle.standardError.write(Data("render failed: \(name)\n".utf8))
        exit(1)
    }
    try data.write(to: iconset.appendingPathComponent(name))
}

if let preview = png(size: 1024) {
    try preview.write(to: assets.appendingPathComponent("icon-1024.png"))
}

let iconutil = Process()
iconutil.executableURL = URL(fileURLWithPath: "/usr/bin/iconutil")
iconutil.arguments = ["-c", "icns", iconset.path, "-o", assets.appendingPathComponent("AppIcon.icns").path]
try iconutil.run()
iconutil.waitUntilExit()

if iconutil.terminationStatus != 0 {
    FileHandle.standardError.write(Data("iconutil failed\n".utf8))
    exit(1)
}

print("generated \(kind) icon -> \(assets.appendingPathComponent("AppIcon.icns").path)")
