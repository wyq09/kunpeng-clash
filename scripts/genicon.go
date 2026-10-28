// 生成应用图标 resources/icon.png（1024x1024）。
//
// 设计（参考 macOS 应用图标的克制风格）：
//   - macOS 风格超椭圆（squircle）深色底板 #1a1d24（带极浅的纵向明度渐变）
//   - 中央一个极简白色几何"鲲鹏"符号：一笔连续的锥形笔画，
//     两翼上扬、翼尖回勾，中心峰为鸟背——抽象笔画，非卡通鸟
//   - 底部一条细蓝条 #4078dc（呼应 FlClash 主色）
//
// 渲染方式：4x 超采样 + 盒式降采样，保证边缘平滑无锯齿。
// 用法：go run scripts/genicon.go [输出路径]（默认 resources/icon.png）
package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

const (
	S  = 1024 // 最终尺寸
	SS = 4    // 超采样倍数
	N  = S * SS
)

// ---- 可调设计参数（1024 坐标系）----

var (
	tileInset   = 100.0                        // 底板外边距（Apple 图标网格：1024 画布，内容 824）
	squircleN   = 4.6                          // 超椭圆指数：越大越方，macOS 风格约 4.5~5
	gradTop     = [3]float64{0x21, 0x25, 0x30} // #212530
	gradBottom  = [3]float64{0x16, 0x19, 0x20} // #161920
	strokeWhite = [3]float64{0xf3, 0xf5, 0xf9} // #f3f5f9
	accentBlue  = [3]float64{0x40, 0x78, 0xdc} // #4078dc

	strokeMaxW = 68.0 // 鸟笔画最大宽度
	barX0      = 312.0
	barX1      = 712.0
	barY0      = 742.0
	barY1      = 782.0
	barR       = 20.0
)

// 鲲鹏符号：两笔镜像的新月形翅膀，翼尖在中央上方近乎交汇，
// 外端翼尖回勾——展翅上扬的抽象飞鸟（非卡通鸟）。
var wingLeft = [][2]float64{
	{258, 492}, // 左外翼尖（回勾）
	{382, 560}, // 翼腹（笔画最厚处）
	{502, 394}, // 内翼尖（中央上方）
}

var wingRight = [][2]float64{
	{522, 394},
	{642, 560},
	{766, 492},
}

// ---- 几何工具（全部工作在超采样坐标系）----

func sc(v float64) float64 { return v * SS }

// superellipse 内部判定（软边由超采样承担）
func insideSquircle(x, y float64) bool {
	cx, cy := float64(N)/2, float64(N)/2
	a := (float64(S)-2*tileInset)/2*SS
	n := squircleN
	dx := math.Abs((x - cx) / a)
	dy := math.Abs((y - cy) / a)
	return math.Pow(dx, n)+math.Pow(dy, n) <= 1
}

func insideRoundedRect(px, py, x0, y0, x1, y1, r float64) bool {
	if px < x0 || px > x1 || py < y0 || py > y1 {
		return false
	}
	cx := math.Max(math.Min(px, x1-r), x0+r)
	cy := math.Max(math.Min(py, y1-r), y0+r)
	return (px-cx)*(px-cx)+(py-cy)*(py-cy) <= r*r
}

// Catmull-Rom 样条：把控制点列转成稠密采样点（超采样坐标）
func splinePoints(pts [][2]float64, per int) [][2]float64 {
	if len(pts) < 2 {
		return nil
	}
	out := make([][2]float64, 0, per*(len(pts)-1))
	p := func(i int) [2]float64 {
		if i < 0 {
			i = 0
		}
		if i > len(pts)-1 {
			i = len(pts) - 1
		}
		return pts[i]
	}
	for i := 0; i < len(pts)-1; i++ {
		p0, p1, p2, p3 := p(i-1), p(i), p(i+1), p(i+2)
		for j := 0; j < per; j++ {
			t := float64(j) / float64(per)
			t2, t3 := t*t, t*t*t
			f := func(a, b, c, d float64) float64 {
				return 0.5 * ((2 * b) + (-a+c)*t + (2*a-5*b+4*c-d)*t2 + (-a+3*b-3*c+d)*t3)
			}
			out = append(out, [2]float64{
				f(p0[0], p1[0], p2[0], p3[0]) * SS,
				f(p0[1], p1[1], p2[1], p3[1]) * SS,
			})
		}
	}
	out = append(out, [2]float64{pts[len(pts)-1][0] * SS, pts[len(pts)-1][1] * SS})
	return out
}

// 锥形笔画：沿弧长 s∈[0,L]，宽度 w = maxW * sin(pi*s/L)^pw
func stampTaperedStroke(img *image.RGBA, spine [][2]float64, maxW, pw float64, c [3]float64) {
	cum := make([]float64, len(spine))
	var total float64
	for i := 1; i < len(spine); i++ {
		dx := spine[i][0] - spine[i-1][0]
		dy := spine[i][1] - spine[i-1][1]
		total += math.Hypot(dx, dy)
		cum[i] = total
	}
	if total == 0 {
		return
	}
	col := color.RGBA{uint8(c[0]), uint8(c[1]), uint8(c[2]), 255}
	for i, pt := range spine {
		s := cum[i] / total
		w := maxW * SS * math.Pow(math.Sin(math.Pi*s), pw)
		if w < 0.5 {
			continue
		}
		stampDisc(img, pt[0], pt[1], w/2, col)
	}
}

func stampDisc(img *image.RGBA, x, y, r float64, c color.RGBA) {
	x0 := int(math.Floor(x - r))
	x1 := int(math.Ceil(x + r))
	y0 := int(math.Floor(y - r))
	y1 := int(math.Ceil(y + r))
	r2 := r * r
	for py := y0; py <= y1; py++ {
		if py < 0 || py >= N {
			continue
		}
		for px := x0; px <= x1; px++ {
			if px < 0 || px >= N {
				continue
			}
			dx := float64(px) - x
			dy := float64(py) - y
			if dx*dx+dy*dy <= r2 {
				img.SetRGBA(px, py, c)
			}
		}
	}
}

func main() {
	out := "resources/icon.png"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}

	img := image.NewRGBA(image.Rect(0, 0, N, N))

	// 1. 底板：squircle + 极浅纵向明度渐变
	inset := tileInset * SS
	for y := 0; y < N; y++ {
		t := (float64(y) - inset) / (float64(N) - 2*inset)
		if t < 0 {
			t = 0
		}
		if t > 1 {
			t = 1
		}
		base := color.RGBA{
			uint8(gradTop[0] + (gradBottom[0]-gradTop[0])*t),
			uint8(gradTop[1] + (gradBottom[1]-gradTop[1])*t),
			uint8(gradTop[2] + (gradBottom[2]-gradTop[2])*t),
			255,
		}
		for x := 0; x < N; x++ {
			if insideSquircle(float64(x)+0.5, float64(y)+0.5) {
				img.SetRGBA(x, y, base)
			}
		}
	}

	// 2. 鲲鹏符号：两笔锥形翅膀
	stampTaperedStroke(img, splinePoints(wingLeft, 600), strokeMaxW, 0.85, strokeWhite)
	stampTaperedStroke(img, splinePoints(wingRight, 600), strokeMaxW, 0.85, strokeWhite)

	// 3. 底部细蓝条
	bx0, by0, bx1, by1, br := sc(barX0), sc(barY0), sc(barX1), sc(barY1), sc(barR)
	barCol := color.RGBA{uint8(accentBlue[0]), uint8(accentBlue[1]), uint8(accentBlue[2]), 255}
	for y := int(by0); y <= int(by1); y++ {
		for x := int(bx0); x <= int(bx1); x++ {
			if insideRoundedRect(float64(x)+0.5, float64(y)+0.5, bx0, by0, bx1, by1, br) {
				img.SetRGBA(x, y, barCol)
			}
		}
	}

	// 4. 盒式降采样 4x -> 1024
	final := image.NewRGBA(image.Rect(0, 0, S, S))
	inv := 1.0 / float64(SS*SS)
	for y := 0; y < S; y++ {
		for x := 0; x < S; x++ {
			var r, g, b, a float64
			for sy := 0; sy < SS; sy++ {
				for sx := 0; sx < SS; sx++ {
					cr, cg, cb, ca := img.At(x*SS+sx, y*SS+sy).RGBA()
					r += float64(cr >> 8)
					g += float64(cg >> 8)
					b += float64(cb >> 8)
					a += float64(ca >> 8)
				}
			}
			final.SetRGBA(x, y, color.RGBA{
				uint8(r * inv), uint8(g * inv), uint8(b * inv), uint8(a * inv),
			})
		}
	}

	f, err := os.Create(out)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, final); err != nil {
		panic(err)
	}
}
