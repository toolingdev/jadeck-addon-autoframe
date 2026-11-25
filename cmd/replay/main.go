// Command replay runs the tracker on a video file and saves a picture of
// what it saw every few seconds: replay <video> <outdir> [seconds] [mode] [shot]
package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/toolingdev/jadeck-addon-autoframe/lib"
)

func main() {
	secs := 30
	if len(os.Args) > 3 {
		secs, _ = strconv.Atoi(os.Args[3])
	}
	mode, shot := lib.ModePair, lib.ShotWaist
	if len(os.Args) > 4 {
		mode = os.Args[4]
	}
	if len(os.Args) > 5 {
		shot = os.Args[5]
	}
	det, err := lib.OpenDetector(lib.DetectorConfig{Kind: "builtin"})
	if err != nil {
		panic(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(secs)*time.Second)
	defer cancel()
	src := &lib.Source{URL: os.Args[1], FPS: 10}
	go src.Run(ctx)
	var tr lib.Tracker
	zone := [4]float64{0.02, 0.05, 0.98, 0.98}
	if len(os.Args) > 6 {
		fmt.Sscanf(os.Args[6], "%f,%f,%f,%f", &zone[0], &zone[1], &zone[2], &zone[3])
	}
	zoneG = zone
	var last uint64
	var n, total int
	var ms float64
	snap := time.Now()
	ids := map[int]bool{}
	for ctx.Err() == nil {
		f := src.Latest()
		if f == nil || f.Seq == last {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		last = f.Seq
		t0 := time.Now()
		ps, err := det.Detect(f)
		if err != nil {
			panic(err)
		}
		ms += float64(time.Since(t0).Milliseconds())
		n++
		ts := tr.Update(ps, f.At)
		for _, t := range ts {
			ids[t.ID] = true
		}
		total += len(ts)
		fr := lib.FrameShot(ts, mode, shot, zone, 0)
		if time.Since(snap) > 5*time.Second {
			snap = time.Now()
			name := filepath.Join(os.Args[2], fmt.Sprintf("snap-%03d.png", n))
			save(name, f, ts, fr)
			fmt.Printf("frame %d: %d people %v · %s\n", n, len(ts), idsOf(ts), fr.Move)
		}
	}
	fmt.Printf("%d frames, %.0f ms avg detect, %.1f people avg, %d identities seen\n", n, ms/float64(max(n, 1)), float64(total)/float64(max(n, 1)), len(ids))
}

func idsOf(ts []*lib.Track) []int {
	var o []int
	for _, t := range ts {
		o = append(o, t.ID)
	}
	return o
}

var zoneG [4]float64

func save(name string, f *lib.Frame, ts []*lib.Track, fr lib.Framing) {
	img := image.NewRGBA(image.Rect(0, 0, f.W, f.H))
	for i, j := 0, 0; i < len(f.RGB); i, j = i+3, j+4 {
		img.Pix[j], img.Pix[j+1], img.Pix[j+2], img.Pix[j+3] = f.RGB[i], f.RGB[i+1], f.RGB[i+2], 255
	}
	in := map[int]bool{}
	for _, s := range fr.Subjects {
		in[s] = true
	}
	for _, t := range ts {
		c := color.RGBA{180, 180, 180, 255}
		if in[t.ID] {
			c = color.RGBA{34, 197, 94, 255}
		}
		draw(img, t.Box, c, 2)
	}
	if len(fr.Subjects) > 0 {
		draw(img, fr.Aim, color.RGBA{255, 255, 255, 255}, 1)
	}
	draw(img, zoneG, color.RGBA{245, 165, 36, 255}, 1)
	o, _ := os.Create(name)
	png.Encode(o, img)
	o.Close()
	if len(fr.Subjects) > 0 {
		crop(name[:len(name)-4]+"-shot.png", f, fr.Aim)
	}
}

// crop saves the aim as the camera would show it: 16:9, 480×270, grey
// where the shot runs past this picture (the camera would pan or zoom out).
func crop(name string, f *lib.Frame, a [4]float64) {
	const w, h = 480, 270
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			fx := a[0] + (a[2]-a[0])*float64(x)/w
			fy := a[1] + (a[3]-a[1])*float64(y)/h
			j := (y*w + x) * 4
			if fx < 0 || fy < 0 || fx >= 1 || fy >= 1 {
				img.Pix[j], img.Pix[j+1], img.Pix[j+2], img.Pix[j+3] = 40, 40, 44, 255
				continue
			}
			i := (int(fy*float64(f.H))*f.W + int(fx*float64(f.W))) * 3
			img.Pix[j], img.Pix[j+1], img.Pix[j+2], img.Pix[j+3] = f.RGB[i], f.RGB[i+1], f.RGB[i+2], 255
		}
	}
	o, _ := os.Create(name)
	png.Encode(o, img)
	o.Close()
}

func draw(img *image.RGBA, b [4]float64, c color.RGBA, w int) {
	W, H := img.Bounds().Dx(), img.Bounds().Dy()
	x1, y1, x2, y2 := int(b[0]*float64(W)), int(b[1]*float64(H)), int(b[2]*float64(W)), int(b[3]*float64(H))
	for t := 0; t < w; t++ {
		for x := x1; x <= x2; x++ {
			img.Set(x, y1+t, c)
			img.Set(x, y2-t, c)
		}
		for y := y1; y <= y2; y++ {
			img.Set(x1+t, y, c)
			img.Set(x2-t, y, c)
		}
	}
}
