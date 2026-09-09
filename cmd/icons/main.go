// Утилита управления иконками приложения. Единственный источник иконок — assets/.
//
//	go run ./cmd/icons gen     — сгенерировать иконку по умолчанию в assets/.
//	                           Пишет ТОЛЬКО в assets/, build/ не трогает.
//	                           Существующие файлы не перезаписывает (без -f).
//	go run ./cmd/icons gen -f  — перезаписать существующие иконки дефолтной.
//	go run ./cmd/icons sync    — подготовить сборку: гарантирует наличие ОБЕИХ
//	                           иконок в assets/ (ICO создаётся из PNG, PNG
//	                           извлекается из ICO) и синхронизирует их в build/.
//
// Почему это важно:
//   - main.go содержит //go:embed assets/icon.png и assets/icon.ico —
//     отсутствие ЛЮБОГО из файлов ломает компиляцию проекта;
//   - иконку exe Wails CLI берёт исключительно из build/windows/icon.ico,
//     а иконку инсталлера/Linux — из build/appicon.png; других путей у CLI нет,
//     поэтому sync и переносит assets/ именно туда.
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

// Пути. Суффикс Path — намеренно: имена без суффикса конфликтовали бы
// с функциями buildICO и т.п. (в Go const и func не могут совпадать в пакете).
const (
    assetPNGPath = "assets/icon.png"
    assetICOPath = "assets/icon.ico"
    buildPNGPath = "build/appicon.png"
    buildICOPath = "build/windows/icon.ico"
)

const S = 256 // размер генерируемой иконки

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
        sync()
    default:
        usage()
        os.Exit(2)
    }
}

func usage() {
    fmt.Println(`Использование: go run ./cmd/icons <команда>

  gen      сгенерировать иконку по умолчанию в assets/ (существующие не трогает)
  gen -f   перезаписать существующие иконки дефолтной
  sync     подготовить сборку: дополнить недостающие иконки в assets/
           и синхронизировать assets/ -> build/ (иконка exe)`)
}

func fail(format string, a ...any) {
    fmt.Fprintf(os.Stderr, "Ошибка: "+format+"\n", a...)
    os.Exit(1)
}

func fileExists(p string) bool {
    _, err := os.Stat(p)
    return err == nil
}

/* -------------------------------- gen ------------------------------------ */

func gen(force bool) {
    for _, p := range []string{assetPNGPath, assetICOPath} {
        if fileExists(p) && !force {
            fail("%s уже существует — не перезаписываю ваши иконки.\n"+
                "Если нужна именно дефолтная иконка: go run ./cmd/icons gen -f", p)
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
                c = color.RGBA{0, 0, 0, 0} // прозрачный угол
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
    fmt.Printf("Созданы: %s и %s\n", assetPNGPath, assetICOPath)
    fmt.Println("Для иконки exe выполните: go run ./cmd/icons sync")
}

/* -------------------------------- sync ----------------------------------- */

func sync() {
    pngExists := fileExists(assetPNGPath)
    icoExists := fileExists(assetICOPath)

    if !pngExists && !icoExists {
        fail("в assets/ нет ни icon.png, ни icon.ico.\n"+
            "Выполните 'go run ./cmd/icons gen' или положите свои иконки\n"+
            "(квадратный PNG, ≥256×256, желательно 1024×1024, и ICO при желании).")
    }

    // PNG отсутствует, ICO есть: пробуем извлечь PNG из ICO.
    // Без assets/icon.png не соберётся embed в main.go (трей Linux/macOS).
    if !pngExists {
        b, err := extractPNGFromICO(assetICOPath)
        if err != nil {
            fail("нет %s, а из %s PNG извлечь не удалось (%v).\n"+
                "Положите %s вручную и повторите sync.", assetPNGPath, assetICOPath, err, assetPNGPath)
        }
        mustWrite(assetPNGPath, b)
        fmt.Printf("Восстановлен %s (извлечён из %s)\n", assetPNGPath, assetICOPath)
    }

    // ICO отсутствует, PNG есть: создаём ICO прямо в assets/ —
    // без него //go:embed в main.go не даст проекту скомпилироваться.
    if !fileExists(assetICOPath) {
        pngBytes, err := os.ReadFile(assetPNGPath)
        if err != nil {
            fail("%v", err)
        }
        mustWrite(assetICOPath, buildICO(pngBytes))
        fmt.Printf("Создан %s (из %s) — без него сборка не компилируется\n", assetICOPath, assetPNGPath)
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

    // Перенос в места, которые жёстко читает Wails CLI.
    mustWrite(buildPNGPath, pngBytes)
    mustWrite(buildICOPath, icoBytes)

    fmt.Println("Иконки синхронизированы:")
    fmt.Printf("  %-24s <- %s\n", buildPNGPath, assetPNGPath)
    fmt.Printf("  %-24s <- %s\n", buildICOPath, assetICOPath)
    fmt.Println("Теперь: wails build")
}

// checkPNG — мягкая валидация: не квадрат/мелкий — предупреждаем, но не блокируем.
func checkPNG(pngBytes []byte) {
    cfg, err := png.DecodeConfig(bytes.NewReader(pngBytes))
    if err != nil {
        fail("%s — не валидный PNG: %v", assetPNGPath, err)
    }
    if cfg.Width != cfg.Height {
        fmt.Printf("ВНИМАНИЕ: PNG не квадратный (%dx%d) — иконка может выглядеть сплюснутой\n", cfg.Width, cfg.Height)
    }
    if cfg.Width < 256 {
        fmt.Printf("ВНИМАНИЕ: %dx%d меньше 256 — рекомендую 256×256 или 1024×1024\n", cfg.Width, cfg.Height)
    }
}

// extractPNGFromICO достаёт PNG-изображение из ICO формата «PNG inside»
// (так устроены современные ICO, включая создаваемые этой утилитой).
// Из нескольких подходящих выбирает наибольший.
func extractPNGFromICO(path string) ([]byte, error) {
    raw, err := os.ReadFile(path)
    if err != nil {
        return nil, err
    }
    if len(raw) < 6 {
        return nil, fmt.Errorf("файл меньше заголовка ICO")
    }
    typ := binary.LittleEndian.Uint16(raw[2:4])
    count := binary.LittleEndian.Uint16(raw[4:6])
    if typ != 1 || count == 0 {
        return nil, fmt.Errorf("это не ICO-файл")
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
        return nil, fmt.Errorf("внутри ICO нет PNG-изображения (вероятно, старый BMP-формат)")
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

/* ------------------------ геометрия (для gen) ----------------------------- */

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

/* ----------------------------- формат ICO -------------------------------- */

func buildICO(pngBytes []byte) []byte {
    var out bytes.Buffer
    binary.Write(&out, binary.LittleEndian, uint16(0)) // reserved
    binary.Write(&out, binary.LittleEndian, uint16(1)) // тип: иконка
    binary.Write(&out, binary.LittleEndian, uint16(1)) // количество изображений

    // ICONDIRENTRY (16 байт); 256x256 кодируется нулями
    out.WriteByte(0) // width
    out.WriteByte(0) // height
    out.WriteByte(0) // палитра
    out.WriteByte(0) // reserved
    binary.Write(&out, binary.LittleEndian, uint16(1))  // planes
    binary.Write(&out, binary.LittleEndian, uint16(32)) // bpp
    binary.Write(&out, binary.LittleEndian, uint32(len(pngBytes)))
    binary.Write(&out, binary.LittleEndian, uint32(6+16)) // offset = 22

    out.Write(pngBytes)
    return out.Bytes()
}