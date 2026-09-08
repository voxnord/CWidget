// Генератор assets/icon.png и assets/icon.ico (запускать из корня проекта).
// ICO с PNG-внутри (256x256) поддерживается Windows Vista+ — этого достаточно.
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

const S = 256 // размер иконки

func main() {
    // ВАЖНО: используем image.RGBA — именно у *image.RGBA есть метод SetRGBA.
    // У *image.NRGBA его нет (там SetNRGBA), из-за этого была ошибка компиляции.
    img := image.NewRGBA(image.Rect(0, 0, S, S))

    // Фон: скруглённый квадрат с диагональным градиентом
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

    // Белые стрелки «⇄»
    w := color.RGBA{0xff, 0xff, 0xff, 0xff}
    fillRect(img, 60, 88, 180, 108, w)                           // тело верхней стрелки
    fillTriangle(img, pt(180, 78), pt(180, 118), pt(216, 98), w) // наконечник →
    fillRect(img, 76, 148, 196, 168, w)                          // тело нижней стрелки
    fillTriangle(img, pt(76, 138), pt(76, 178), pt(40, 158), w)  // наконечник ←

    if err := os.MkdirAll("assets", 0o755); err != nil {
        panic(err)
    }
    var buf bytes.Buffer
    if err := png.Encode(&buf, img); err != nil {
        panic(err)
    }
    pngPath := filepath.Join("assets", "icon.png")
    icoPath := filepath.Join("assets", "icon.ico")
    if err := os.WriteFile(pngPath, buf.Bytes(), 0o644); err != nil {
        panic(err)
    }
    if err := writeICO(icoPath, buf.Bytes()); err != nil {
        panic(err)
    }
    fmt.Println("Созданы:", pngPath, "и", icoPath)
}

/* ---------- геометрия ---------- */

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

/* ---------- формат ICO ---------- */

func writeICO(path string, pngBytes []byte) error {
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
    return os.WriteFile(path, out.Bytes(), 0o644)
}