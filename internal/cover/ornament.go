package cover

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	"golang.org/x/image/vector"
)

// None of the embedded fonts has ❦ (U+2766) or ⁂ (U+2042), so both
// ornaments are drawn as vector shapes, traced from the reference cover
// (where Chrome took them from a system font). Coordinates are pixels in
// a box ornWidth wide centred on the page, top at the .orn line box.

const (
	ornWidth  = 144
	ornHeight = 140
)

type pt struct{ x, y float64 }

func (p pt) add(q pt) pt             { return pt{p.x + q.x, p.y + q.y} }
func (p pt) scale(k float64) pt      { return pt{p.x * k, p.y * k} }
func (p pt) sub(q pt) pt             { return pt{p.x - q.x, p.y - q.y} }
func (p pt) rotate(a float64) pt     { s, c := math.Sincos(a); return pt{p.x*c - p.y*s, p.x*s + p.y*c} }
func (p pt) f32() (float32, float32) { return float32(p.x), float32(p.y) }

// fill rasterises one outline into the ornament box at (ox, oy). Each
// outline gets its own rasterizer, so overlapping outlines of opposite
// winding cannot cancel out.
func fill(img draw.Image, ox, oy int, col color.Color, outline func(z *vector.Rasterizer)) {
	z := vector.NewRasterizer(ornWidth, ornHeight)
	outline(z)
	z.Draw(img, image.Rect(ox, oy, ox+ornWidth, oy+ornHeight), image.NewUniform(col), image.Point{})
}

// polygon closes a path through pts.
func polygon(z *vector.Rasterizer, pts []pt) {
	z.MoveTo(pts[0].f32())
	for _, p := range pts[1:] {
		z.LineTo(p.f32())
	}
	z.ClosePath()
}

// cubicAt returns the point and the tangent of a cubic Bézier at t.
func cubicAt(c [4]pt, t float64) (p, d pt) {
	u := 1 - t
	p = c[0].scale(u * u * u).add(c[1].scale(3 * u * u * t)).add(c[2].scale(3 * u * t * t)).add(c[3].scale(t * t * t))
	d = c[1].sub(c[0]).scale(3 * u * u).add(c[2].sub(c[1]).scale(6 * u * t)).add(c[3].sub(c[2]).scale(3 * t * t))
	return p, d
}

// stroke outlines a cubic Bézier drawn with a pen tapering from width w0
// to w1, like a nib.
func stroke(c [4]pt, w0, w1 float64) func(z *vector.Rasterizer) {
	return func(z *vector.Rasterizer) {
		const steps = 32
		left := make([]pt, 0, steps+1)
		right := make([]pt, 0, steps+1)
		for i := 0; i <= steps; i++ {
			t := float64(i) / steps
			p, d := cubicAt(c, t)
			n := math.Hypot(d.x, d.y)
			if n == 0 {
				d, n = c[3].sub(c[0]), math.Hypot(c[3].x-c[0].x, c[3].y-c[0].y)
			}
			half := (w0 + (w1-w0)*t) / 2
			normal := pt{-d.y / n, d.x / n}.scale(half)
			left = append(left, p.add(normal))
			right = append(right, p.sub(normal))
		}
		for i := len(right) - 1; i >= 0; i-- {
			left = append(left, right[i])
		}
		polygon(z, left)
	}
}

// disc outlines a circle.
func disc(c pt, r float64) func(z *vector.Rasterizer) {
	return func(z *vector.Rasterizer) {
		k := 0.5523 * r // control distance for a quarter circle
		z.MoveTo(c.add(pt{r, 0}).f32())
		quarter := func(b, cp1, cp2 pt) {
			x1, y1 := c.add(cp1).f32()
			x2, y2 := c.add(cp2).f32()
			x3, y3 := c.add(b).f32()
			z.CubeTo(x1, y1, x2, y2, x3, y3)
		}
		quarter(pt{0, r}, pt{r, k}, pt{k, r})
		quarter(pt{-r, 0}, pt{-k, r}, pt{-r, k})
		quarter(pt{0, -r}, pt{-r, -k}, pt{-k, -r})
		quarter(pt{r, 0}, pt{k, -r}, pt{r, -k})
		z.ClosePath()
	}
}

// heart outlines the fleuron's leaf as a closed chain of cubics.
func heart(z *vector.Rasterizer) {
	segs := [][3]pt{
		{{77, 43}, {81, 42}, {84, 42}},
		{{89, 42}, {92, 47}, {92, 54}},
		{{92, 62}, {84, 66}, {78, 71}},
		{{75, 74}, {73, 78}, {71, 82}},
		{{60, 79}, {51, 69}, {50, 58}},
		{{50, 48}, {55, 42}, {60, 42}},
		{{66, 42}, {71, 45}, {74, 49}},
	}
	z.MoveTo(74, 49)
	for _, s := range segs {
		x1, y1 := s[0].f32()
		x2, y2 := s[1].f32()
		x3, y3 := s[2].f32()
		z.CubeTo(x1, y1, x2, y2, x3, y3)
	}
	z.ClosePath()
}

// drawFleuron draws ❦: a heart-shaped leaf on a looped stem with two
// tendrils and a curled tail, in the .orn line box whose top is top.
func drawFleuron(img draw.Image, cx, top int, col color.Color) {
	ox, oy := cx-ornWidth/2, top
	for _, outline := range []func(*vector.Rasterizer){
		heart,
		stroke([4]pt{{70, 81}, {77, 83}, {84, 81}, {87, 75}}, 4.5, 3), // tail
		disc(pt{87.5, 74.5}, 2.8),
		stroke([4]pt{{74, 50}, {74, 44}, {76, 38}, {76, 34}}, 3.8, 3), // stem
		stroke([4]pt{{76, 34}, {79, 28}, {76, 23}, {72, 24}}, 3, 3),   // loop
		stroke([4]pt{{72, 24}, {68, 25}, {68, 31}, {73, 33}}, 3, 3),
		stroke([4]pt{{73, 33}, {80, 37}, {88, 34}, {93, 28}}, 3, 3.5), // right tendril
		disc(pt{94, 27}, 3.8),
		stroke([4]pt{{70, 25}, {62, 21}, {48, 22}, {42, 29}}, 3, 3.2), // left tendril
		stroke([4]pt{{42, 29}, {39, 33}, {40, 38}, {43, 40}}, 3.2, 3),
		disc(pt{43.5, 40}, 3),
	} {
		fill(img, ox, oy, col, outline)
	}
}

// drawAsterism draws ⁂, three five-armed asterisks in a triangle, in the
// .orn line box whose top is top.
func drawAsterism(img draw.Image, cx, top int, col color.Color) {
	ox, oy := cx-ornWidth/2, top
	for _, c := range []pt{{72, 49}, {44, 89}, {100, 89}} {
		fill(img, ox, oy, col, asterisk(c))
	}
}

// asterisk outlines five arms, flaring slightly towards their square
// tips, the first pointing up. All arms share one winding, so they union.
func asterisk(c pt) func(z *vector.Rasterizer) {
	const (
		arm     = 21.0
		rootW   = 3.0 // half-width at the centre
		tipW    = 4.2 // half-width at the tip
		armStep = 2 * math.Pi / 5
	)
	return func(z *vector.Rasterizer) {
		for i := range 5 {
			a := float64(i) * armStep
			corners := []pt{{-rootW, 0}, {-tipW, -arm}, {tipW, -arm}, {rootW, 0}}
			for j := range corners {
				corners[j] = c.add(corners[j].rotate(a))
			}
			polygon(z, corners)
		}
	}
}
