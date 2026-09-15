// Command icons manages the application icons. assets/ is the single source
// of truth.
//
//	go run ./cmd/icons gen     — generate the default icon into assets/.
//	                           Writes ONLY to assets/, never touches build/.
//	                           Does not overwrite existing files (without -f).
//	go run ./cmd/icons gen -f  — overwrite existing icons with the default one.
//	go run ./cmd/icons sync    — prepare the build: guarantees BOTH icons exist
//	                           in assets/ (the ICO is created from the PNG,
//	                           the PNG is extracted from the ICO) and copies
//	                           them into build/.
//
// Why this matters:
//   - main.go contains //go:embed assets/icon.png and assets/icon.ico —
//     a missing ANY of these files breaks the build of the whole project;
//   - the exe icon is read by the Wails CLI exclusively from
//     build/windows/icon.ico, and the installer/Linux icon from
//     build/appicon.png — the CLI has no other paths, so sync copies
//     assets/ exactly there.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
)

// Paths. The "Path" suffix is intentional: bare names would collide with
// the buildICO function (in Go a const and a func cannot share a name
// within a package).
const (
    assetPNGPath = "assets/icon.png"
    assetICOPath = "assets/icon.ico"
    buildPNGPath = "build/appicon.png"
    buildICOPath = "build/windows/icon.ico"
)

const S = 256 // size of the generated icon

func main() {
    if len(os.Args) < 2 {
        usage()
        os.Exit(2)
    }
    switch os.Args[1] {
    case "gen":
        force := len(os.Args) > 2 && (os.Args[2] == "-f" || os.Args[2] == "--force")
        gen(force)
    case "sync":
        syncIcons()
    default:
        usage()
        os.Exit(2)
    }
}

func usage() {
    fmt.Println(`Usage: go run ./cmd/icons <command>

  gen      generate the default icon into assets/ (existing files are kept)
  gen -f   overwrite existing icons with the default one
  sync     prepare the build: fill in missing icons in assets/
           and sync assets/ -> build/ (the exe icon)`)
}

func fail(format string, a ...any) {
    fmt.Fprintf(os.Stderr, "Error: "+format+"\n", a...)
    os.Exit(1)
}

func fileExists(p string) bool {
    _, err := os.Stat(p)
    return err == nil
}

/* --------------------------------- gen ----------------------------------- */

func gen(force bool) {
    for _, p := range []string{assetPNGPath, assetICOPath} {
        if fileExists(p) && !force {
            fail("%s already exists — not overwriting your icons.\n"+
                "If you need the default icon specifically: go run ./cmd/icons gen -f", p)
        }
    }

    img := image.NewRGBA(image.Rect(0, 0, S, S))

    top := color.RGBA{0x6c, 0x8c, 0xff, 0xff}
    bot := color.RGBA{0x27, 0x3c, 0x8c, 0xff}
    radius := 56
    for y := 0; y < S; y++ {
        for x := 0; x < S; x++ {
            t := float64(x+y) / (2 * float64(S))
            c := mix(top, bot, t)
            if !insideRounded(x, y, S, radius) {
                c = color.RGBA{0, 0, 0, 0} // transparent corner
            }
            img.SetRGBA(x, y, c)
        }
    }

    w := color.RGBA{0xff, 0xff, 0xff, 0xff}
    fillRect(img, 60, 88, 180, 108, w)
    fillTriangle(img, pt(180, 78), pt(180, 118), pt(216, 98), w)
    fillRect(img, 76, 148, 196, 168, w)
    fillTriangle(img, pt(76, 138), pt(76, 178), pt(40, 158), w)

    var buf bytes.Buffer
    if err := png.Encode(&buf, img); err != nil {
        panic(err)
    }
    mustWrite(assetPNGPath, buf.Bytes())
    mustWrite(assetICOPath, buildICO(buf.Bytes()))
    fmt.Printf("Created: %s and %s\n", assetPNGPath, assetICOPath)
    fmt.Println("For the exe icon run: go run ./cmd/icons sync")
}

/* -------------------------------- sync ----------------------------------- */

func syncIcons() {
    pngExists := fileExists(assetPNGPath)
    icoExists := fileExists(assetICOPath)

    if !pngExists && !icoExists {
        fail("assets/ has neither icon.png nor icon.ico.\n" +
            "Run 'go run ./cmd/icons gen' or put your own icons there\n" +
            "(a square PNG, >=256x256, ideally 1024x1024, plus an ICO if you have one).")
    }

    // PNG missing, ICO present: try to extract a PNG from the ICO.
    // Without assets/icon.png the //go:embed in main.go (the Linux/macOS
    // tray icon) will not compile.
    if !pngExists {
        b, err := extractPNGFromICO(assetICOPath)
        if err != nil {
            fail("%s is missing and no PNG could be extracted from %s (%v).\n"+
                "Put %s there manually and re-run sync.", assetPNGPath, assetICOPath, err, assetPNGPath)
        }
        mustWrite(assetPNGPath, b)
        fmt.Printf("Restored %s (extracted from %s)\n", assetPNGPath, assetICOPath)
    }

    // ICO missing, PNG present: create the ICO right in assets/ —
    // without it the //go:embed in main.go blocks compilation.
    if !fileExists(assetICOPath) {
        pngBytes, err := os.ReadFile(assetPNGPath)
        if err != nil {
            fail("%v", err)
        }
        mustWrite(assetICOPath, buildICO(pngBytes))
        fmt.Printf("Created %s (from %s) — the build does not compile without it\n", assetICOPath, assetPNGPath)
    }

    pngBytes, err := os.ReadFile(assetPNGPath)
    if err != nil {
        fail("%v", err)
    }
    checkPNG(pngBytes)
    icoBytes, err := os.ReadFile(assetICOPath)
    if err != nil {
        fail("%v", err)
    }

    // Copy to the locations the Wails CLI reads hard.
    mustWrite(buildPNGPath, pngBytes)
    mustWrite(buildICOPath, icoBytes)

    fmt.Println("Icons synced:")
    fmt.Printf("  %-24s <- %s\n", buildPNGPath, assetPNGPath)
    fmt.Printf("  %-24s <- %s\n", buildICOPath, assetICOPath)
    fmt.Println("Now run: wails build")
}

// checkPNG is a soft validation: a non-square or tiny image is a warning,
// not an error.
func checkPNG(pngBytes []byte) {
    cfg, err := png.DecodeConfig(bytes.NewReader(pngBytes))
    if err != nil {
        fail("%s is not a valid PNG: %v", assetPNGPath, err)
    }
    if cfg.Width != cfg.Height {
        fmt.Printf("WARNING: the PNG is not square (%dx%d) — the icon may look squashed\n", cfg.Width, cfg.Height)
    }
    if cfg.Width < 256 {
        fmt.Printf("WARNING: %dx%d is below 256 — 256x256 or 1024x1024 is recommended\n", cfg.Width, cfg.Height)
    }
}

// extractPNGFromICO pulls the PNG image out of a "PNG inside" ICO
// (how modern ICO files are built, including the ones this tool creates).
// If several images match, the largest one is chosen.
func extractPNGFromICO(path string) ([]byte, error) {
    raw, err := os.ReadFile(path)
    if err != nil {
        return nil, err
    }
    if len(raw) < 6 {
        return nil, fmt.Errorf("file is smaller than the ICO header")
    }
    typ := binary.LittleEndian.Uint16(raw[2:4])
    count := binary.LittleEndian.Uint16(raw[4:6])
    if typ != 1 || count == 0 {
        return nil, fmt.Errorf("not an ICO file")
    }
    sig := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
    var best []byte
    for i := 0; i < int(count); i++ {
        off := 6 + i*16
        if off+16 > len(raw) {
            break
        }
        size := binary.LittleEndian.Uint32(raw[off+8 : off+12])
        doff := binary.LittleEndian.Uint32(raw[off+12 : off+16])
        if uint64(doff)+uint64(size) > uint64(len(raw)) || size < 8 {
            continue
        }
        data := raw[doff : doff+size]
        if bytes.HasPrefix(data, sig) && len(data) > len(best) {
            best = data
        }
    }
    if best == nil {
        return nil, fmt.Errorf("no PNG image inside the ICO (probably the legacy BMP format)")
    }
    return best, nil
}

func mustWrite(path string, b []byte) {
    if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
        panic(err)
    }
    if err := os.WriteFile(path, b, 0o644); err != nil {
        panic(err)
    }
}

/* ------------------------ geometry (for gen) ----------------------------- */

func pt(x, y int) image.Point { return image.Pt(x, y) }

func insideRounded(x, y, size, r int) bool {
    if x < 0 || y < 0 || x >= size || y >= size {
        return false
    }
    cx := min(max(x, r), size-1-r)
    cy := min(max(y, r), size-1-r)
    dx, dy := x-cx, y-cy
    return dx*dx+dy*dy <= r*r
}

func mix(a, b color.RGBA, t float64) color.RGBA {
    l := func(u, v uint8) uint8 {
        return uint8(float64(u) + (float64(v)-float64(u))*t)
    }
    return color.RGBA{l(a.R, b.R), l(a.G, b.G), l(a.B, b.B), 0xff}
}

func fillRect(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA) {
    for y := y0; y < y1; y++ {
        for x := x0; x < x1; x++ {
            img.SetRGBA(x, y, c)
        }
    }
}

func fillTriangle(img *image.RGBA, a, b, c image.Point, col color.RGBA) {
    edge := func(ax, ay, bx, by, px, py float64) float64 {
        return (px-ax)*(by-ay) - (py-ay)*(bx-ax)
    }
    f := func(p image.Point) [3]float64 {
        return [3]float64{
            edge(float64(a.X), float64(a.Y), float64(b.X), float64(b.Y), float64(p.X), float64(p.Y)),
            edge(float64(b.X), float64(b.Y), float64(c.X), float64(c.Y), float64(p.X), float64(p.Y)),
            edge(float64(c.X), float64(c.Y), float64(a.X), float64(a.Y), float64(p.X), float64(p.Y)),
        }
    }
    minX := min(a.X, b.X, c.X)
    maxX := max(a.X, b.X, c.X)
    minY := min(a.Y, b.Y, c.Y)
    maxY := max(a.Y, b.Y, c.Y)
    for y := minY; y <= maxY; y++ {
        for x := minX; x <= maxX; x++ {
            d := f(image.Pt(x, y))
            hasNeg := d[0] < 0 || d[1] < 0 || d[2] < 0
            hasPos := d[0] > 0 || d[1] > 0 || d[2] > 0
            if !(hasNeg && hasPos) {
                img.SetRGBA(x, y, col)
            }
        }
    }
}

/* ------------------------------- ICO format ------------------------------ */

func buildICO(pngBytes []byte) []byte {
    var out bytes.Buffer
    binary.Write(&out, binary.LittleEndian, uint16(0)) // reserved
    binary.Write(&out, binary.LittleEndian, uint16(1)) // type: icon
    binary.Write(&out, binary.LittleEndian, uint16(1)) // image count

    // ICONDIRENTRY (16 bytes); 256x256 is encoded as zeroes
    out.WriteByte(0) // width
    out.WriteByte(0) // height
    out.WriteByte(0) // palette
    out.WriteByte(0) // reserved
    binary.Write(&out, binary.LittleEndian, uint16(1))  // planes
    binary.Write(&out, binary.LittleEndian, uint16(32)) // bpp
    binary.Write(&out, binary.LittleEndian, uint32(len(pngBytes)))
    binary.Write(&out, binary.LittleEndian, uint32(6+16)) // offset = 22

    out.Write(pngBytes)
    return out.Bytes()
}