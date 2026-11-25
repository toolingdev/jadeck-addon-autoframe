// Command detect runs the tracker's detector on a picture and draws what
// it found: detect <in.jpg> <out.png> [model] [family]
package main

import (
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"os"
	"time"

	tr "github.com/toolingdev/jadeck-addon-autoframe/lib"
)

func main() {
	fh, err := os.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	src, _, err := image.Decode(fh)
	if err != nil {
		panic(err)
	}
	b := src.Bounds()
	f := &tr.Frame{W: b.Dx(), H: b.Dy(), RGB: make([]byte, b.Dx()*b.Dy()*3)}
	for y := 0; y < f.H; y++ {
		for x := 0; x < f.W; x++ {
			r, g, bb, _ := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			i := (y*f.W + x) * 3
			f.RGB[i], f.RGB[i+1], f.RGB[i+2] = byte(r>>8), byte(g>>8), byte(bb>>8)
		}
	}
	cfg := tr.DetectorConfig{Kind: "builtin"}
	if len(os.Args) > 4 {
		cfg = tr.DetectorConfig{Kind: "onnx", Model: os.Args[3], Family: os.Args[4]}
	}
	t0 := time.Now()
	d, err := tr.OpenDetector(cfg)
	if err != nil {
		panic(err)
	}
	fmt.Println(d.Describe(), "opened in", time.Since(t0).Round(time.Millisecond))
	var ps []tr.Person
	for i := 0; i < 5; i++ {
		t := time.Now()
		ps, err = d.Detect(f)
		if err != nil {
			panic(err)
		}
		fmt.Println("run", i, time.Since(t).Round(time.Millisecond), len(ps), "people")
	}
	out := image.NewRGBA(b)
	for y := 0; y < f.H; y++ {
		for x := 0; x < f.W; x++ {
			out.Set(x, y, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	green := color.RGBA{34, 197, 94, 255}
	for _, p := range ps {
		x1, y1, x2, y2 := int(p.Box[0]*float64(f.W)), int(p.Box[1]*float64(f.H)), int(p.Box[2]*float64(f.W)), int(p.Box[3]*float64(f.H))
		for t := 0; t < 3; t++ {
			for x := x1; x <= x2; x++ {
				out.Set(x, y1+t, green)
				out.Set(x, y2-t, green)
			}
			for y := y1; y <= y2; y++ {
				out.Set(x1+t, y, green)
				out.Set(x2-t, y, green)
			}
		}
		for _, k := range p.Points {
			if k[2] < 0.3 {
				continue
			}
			cx, cy := int(k[0]*float64(f.W)), int(k[1]*float64(f.H))
			for dy := -3; dy <= 3; dy++ {
				for dx := -3; dx <= 3; dx++ {
					out.Set(cx+dx, cy+dy, color.RGBA{245, 165, 36, 255})
				}
			}
		}
		fmt.Printf("  %.2f box %.3f %.3f %.3f %.3f points %d\n", p.Score, p.Box[0], p.Box[1], p.Box[2], p.Box[3], len(p.Points))
	}
	o, _ := os.Create(os.Args[2])
	png.Encode(o, out)
	o.Close()
}
