//go:build ignore

// gen_icon.go 生成 StreamGuard 应用图标（盾牌 + 流量条）。
//
// 用法: go run scripts/gen_icon.go
//
// 输出:
//   - cmd/streamguard-gui/build/appicon.png  (1024x1024, Wails 源图标)
//   - cmd/streamguard-gui/build/windows/icon.ico (多尺寸 ICO)
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

const size = 1024

// 配色（与界面深色主题一致）
var (
	bgTop    = color.RGBA{0x1a, 0x1d, 0x26, 0xff} // 面板色
	bgBottom = color.RGBA{0x12, 0x14, 0x1a, 0xff} // 背景色
	shieldHi = color.RGBA{0x5b, 0x96, 0xff, 0xff} // 强调蓝（亮）
	shieldLo = color.RGBA{0x2b, 0x5c, 0xd6, 0xff} // 深蓝
	barColor = color.RGBA{0x2e, 0xcc, 0x71, 0xff} // 成功绿
	barDim   = color.RGBA{0x2e, 0xcc, 0x71, 0x99} // 半透明绿
)

func main() {
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	// 1. 圆角背景 + 垂直渐变
	drawRoundedGradient(img)

	// 2. 盾牌
	drawShield(img)

	// 3. 盾牌内的流量条（限流意象：三条递增的横条）
	drawBars(img)

	// 输出 PNG
	outDir := filepath.Join("cmd", "streamguard-gui", "build")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		panic(err)
	}
	pngPath := filepath.Join(outDir, "appicon.png")
	if err := writePNG(pngPath, img); err != nil {
		panic(err)
	}
	println("wrote", pngPath)

	// 输出 ICO（多尺寸）
	icoPath := filepath.Join(outDir, "windows", "icon.ico")
	if err := os.MkdirAll(filepath.Dir(icoPath), 0o755); err != nil {
		panic(err)
	}
	if err := writeICO(icoPath, img, []int{16, 32, 48, 64, 128, 256}); err != nil {
		panic(err)
	}
	println("wrote", icoPath)
}

// drawRoundedGradient 绘制圆角矩形背景，带垂直渐变。
func drawRoundedGradient(img *image.RGBA) {
	const radius = 200
	for y := 0; y < size; y++ {
		t := float64(y) / float64(size-1)
		c := lerpColor(bgTop, bgBottom, t)
		for x := 0; x < size; x++ {
			if !inRoundedRect(x, y, size, size, radius) {
				continue
			}
			img.Set(x, y, c)
		}
	}
}

// inRoundedRect 判断点是否在圆角矩形内（含抗锯齿边缘）。
func inRoundedRect(x, y, w, h, r int) bool {
	fx, fy := float64(x)+0.5, float64(y)+0.5
	fw, fh, fr := float64(w), float64(h), float64(r)

	if fx < 0 || fy < 0 || fx > fw || fy > fh {
		return false
	}
	// 四个角的圆心
	type pt struct{ cx, cy float64 }
	corners := []pt{
		{fr, fr},
		{fw - fr, fr},
		{fr, fh - fr},
		{fw - fr, fh - fr},
	}
	for _, c := range corners {
		// 仅当点位于该角对应的象限内才做圆判定
		inX := (c.cx == fr && fx < fr) || (c.cx == fw-fr && fx > fw-fr)
		inY := (c.cy == fr && fy < fr) || (c.cy == fh-fr && fy > fh-fr)
		if inX && inY {
			dx, dy := fx-c.cx, fy-c.cy
			return dx*dx+dy*dy <= fr*fr
		}
	}
	return true
}

// drawShield 绘制盾牌形状（带渐变）。
//
// 盾牌由两部分构成：
//   - 上部：圆角矩形（顶部两角圆润）
//   - 下部：椭圆收窄成尖角
func drawShield(img *image.RGBA) {
	cx := float64(size) / 2
	top := float64(size) * 0.19
	bottom := float64(size) * 0.85
	halfW := float64(size) * 0.27
	cornerR := float64(size) * 0.10 // 顶部圆角半径

	for y := int(top); y <= int(bottom); y++ {
		fy := float64(y)
		t := (fy - top) / (bottom - top)

		// 计算该行的半宽
		var w float64
		if t < 0.50 {
			w = halfW
		} else {
			// 椭圆收窄，形成圆润尖角
			k := (t - 0.50) / 0.50
			w = halfW * math.Sqrt(math.Max(0, 1-k*k))
		}

		// 顶部圆角：靠近 top 时收窄
		if fy < top+cornerR {
			dy := top + cornerR - fy
			inset := cornerR - math.Sqrt(math.Max(0, cornerR*cornerR-dy*dy))
			w -= inset
		}

		if w <= 0 {
			continue
		}

		c := lerpColor(shieldHi, shieldLo, t)
		for x := int(cx - w); x <= int(cx+w); x++ {
			img.Set(x, y, c)
		}
	}
}

// drawBars 在盾牌内绘制三条流量条（限流意象）。
func drawBars(img *image.RGBA) {
	cx := float64(size) / 2
	barH := float64(size) * 0.042
	gap := float64(size) * 0.052
	startY := float64(size) * 0.35

	// 三条横条，长度递增（表示流量累积/排队）
	widths := []float64{0.15, 0.23, 0.31}
	colors := []color.RGBA{barDim, barDim, barColor}

	for i := range widths {
		w := float64(size) * widths[i]
		y0 := startY + float64(i)*(barH+gap)
		c := colors[i]
		drawRoundedBar(img, cx-w/2, y0, w, barH, barH/2, c)
	}
}

// drawRoundedBar 绘制圆角横条。
func drawRoundedBar(img *image.RGBA, x, y, w, h, r float64, c color.RGBA) {
	for py := int(y); py < int(y+h); py++ {
		for px := int(x); px < int(x+w); px++ {
			fx, fy := float64(px)+0.5, float64(py)+0.5
			if !inRoundedRectF(fx-x, fy-y, w, h, r) {
				continue
			}
			img.Set(px, py, c)
		}
	}
}

// inRoundedRectF 浮点版圆角矩形判定。
func inRoundedRectF(x, y, w, h, r float64) bool {
	if x < 0 || y < 0 || x > w || y > h {
		return false
	}
	if x < r && y < r {
		return (x-r)*(x-r)+(y-r)*(y-r) <= r*r
	}
	if x > w-r && y < r {
		return (x-(w-r))*(x-(w-r))+(y-r)*(y-r) <= r*r
	}
	if x < r && y > h-r {
		return (x-r)*(x-r)+(y-(h-r))*(y-(h-r)) <= r*r
	}
	if x > w-r && y > h-r {
		return (x-(w-r))*(x-(w-r))+(y-(h-r))*(y-(h-r)) <= r*r
	}
	return true
}

// lerpColor 线性插值两个颜色。
func lerpColor(a, b color.RGBA, t float64) color.RGBA {
	t = math.Max(0, math.Min(1, t))
	return color.RGBA{
		uint8(float64(a.R) + (float64(b.R)-float64(a.R))*t),
		uint8(float64(a.G) + (float64(b.G)-float64(a.G))*t),
		uint8(float64(a.B) + (float64(b.B)-float64(a.B))*t),
		uint8(float64(a.A) + (float64(b.A)-float64(a.A))*t),
	}
}

// writePNG 写出 PNG 文件。
func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// writeICO 写出多尺寸 ICO 文件（PNG 压缩格式，Vista+ 支持）。
func writeICO(path string, src image.Image, sizes []int) error {
	type entry struct {
		w, h int
		data []byte
	}
	var entries []entry

	for _, s := range sizes {
		// 缩放（最近邻，保持锐利）
		scaled := resizeNearest(src, s)
		var buf bytes.Buffer
		if err := png.Encode(&buf, scaled); err != nil {
			return err
		}
		entries = append(entries, entry{w: s, h: s, data: buf.Bytes()})
	}

	var out bytes.Buffer
	// ICONDIR
	binary.Write(&out, binary.LittleEndian, uint16(0))            // reserved
	binary.Write(&out, binary.LittleEndian, uint16(1))            // type: icon
	binary.Write(&out, binary.LittleEndian, uint16(len(entries))) // count

	// 计算数据偏移
	offset := 6 + 16*len(entries)
	for _, e := range entries {
		wb, hb := byte(e.w), byte(e.h)
		if e.w >= 256 {
			wb = 0
		}
		if e.h >= 256 {
			hb = 0
		}
		out.WriteByte(wb)                                            // width
		out.WriteByte(hb)                                            // height
		out.WriteByte(0)                                             // color count
		out.WriteByte(0)                                             // reserved
		binary.Write(&out, binary.LittleEndian, uint16(1))           // planes
		binary.Write(&out, binary.LittleEndian, uint16(32))          // bpp
		binary.Write(&out, binary.LittleEndian, uint32(len(e.data))) // size
		binary.Write(&out, binary.LittleEndian, uint32(offset))      // offset
		offset += len(e.data)
	}

	for _, e := range entries {
		out.Write(e.data)
	}

	return os.WriteFile(path, out.Bytes(), 0o644)
}

// resizeNearest 最近邻缩放。
func resizeNearest(src image.Image, dstSize int) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, dstSize, dstSize))
	draw.Draw(dst, dst.Bounds(), image.Transparent, image.Point{}, draw.Src)

	sx := float64(b.Dx()) / float64(dstSize)
	sy := float64(b.Dy()) / float64(dstSize)

	for y := 0; y < dstSize; y++ {
		for x := 0; x < dstSize; x++ {
			px := b.Min.X + int(float64(x)*sx)
			py := b.Min.Y + int(float64(y)*sy)
			dst.Set(x, y, src.At(px, py))
		}
	}
	return dst
}
