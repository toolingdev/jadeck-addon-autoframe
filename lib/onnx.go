package lib

// ONNX models, run with ONNX Runtime: on the GPU through DirectML on
// Windows (any DirectX 12 card, no CUDA install), the CPU elsewhere. Families say how a model wants its picture and
// how it lays out its answer:
//
//	rtmo  RTMO (OpenMMLab, Apache-2.0): people and 17 body points in one
//	      pass. Input 1×3×640×640 BGR 0..255; outputs dets [1,N,5]
//	      (x1,y1,x2,y2,score) and keypoints [1,N,17,3].
//	yolo  the YOLOv8/11 output layout (detect [1,4+C,N] or pose [1,56,N]),
//	      class 0 = person. Input 1×3×640×640 RGB 0..1. Bring a model whose
//	      license fits you.

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

const netSize = 640

var ortOnce struct {
	sync.Mutex
	done bool
}

func runtimeLibrary(given string) (string, error) {
	if given != "" {
		return given, nil
	}
	if p := os.Getenv("ONNXRUNTIME_LIB"); p != "" {
		return p, nil
	}
	exe, _ := os.Executable()
	dir := filepath.Dir(exe)
	var names []string
	switch runtime.GOOS {
	case "windows":
		names = []string{filepath.Join(dir, "onnxruntime.dll"), filepath.Join(dir, "lib", "onnxruntime.dll")}
	case "darwin":
		names = []string{filepath.Join(dir, "libonnxruntime.dylib"), filepath.Join(dir, "lib", "libonnxruntime.dylib"),
			"/opt/homebrew/lib/libonnxruntime.dylib", "/usr/local/lib/libonnxruntime.dylib"}
	default:
		names = []string{filepath.Join(dir, "libonnxruntime.so"), filepath.Join(dir, "lib", "libonnxruntime.so"),
			"/usr/lib/libonnxruntime.so", "/usr/local/lib/libonnxruntime.so"}
	}
	for _, n := range names {
		if _, err := os.Stat(n); err == nil {
			return n, nil
		}
	}
	return "", errors.New("ONNX Runtime isn't installed next to the add-on")
}

func initRuntime(lib string) error {
	ortOnce.Lock()
	defer ortOnce.Unlock()
	if ortOnce.done {
		return nil
	}
	p, err := runtimeLibrary(lib)
	if err != nil {
		return err
	}
	ort.SetSharedLibraryPath(p)
	if err := ort.InitializeEnvironment(); err != nil {
		return fmt.Errorf("ONNX Runtime: %w", err)
	}
	ortOnce.done = true
	return nil
}

func builtinModel() string {
	if p := os.Getenv("AUTOFRAME_MODEL"); p != "" {
		return p
	}
	exe, _ := os.Executable()
	// The add-on's own folder is replaced on every update, so the model is
	// kept in the per-user config folder (see model.go).
	for _, p := range []string{
		modelHome(),
		filepath.Join(filepath.Dir(exe), "models", "rtmo-s.onnx"),
		filepath.Join("models", "rtmo-s.onnx"),
		filepath.Join("models", "rtmo-s", "end2end.onnx"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return modelHome()
}

type onnxDetector struct {
	family   string
	label    string
	accel    string
	minScore float64
	sess     *ort.DynamicAdvancedSession
	outNames []string
	input    []float32
}

func openONNX(model, family, label string, c DetectorConfig) (Detector, error) {
	if _, err := os.Stat(model); err != nil {
		return nil, fmt.Errorf("model file: %w", err)
	}
	if err := initRuntime(c.Runtime); err != nil {
		return nil, err
	}
	var outs []string
	switch family {
	case "rtmo":
		outs = []string{"dets", "keypoints"}
	case "yolo":
		_, o, err := ort.GetInputOutputInfo(model)
		if err != nil || len(o) == 0 {
			return nil, fmt.Errorf("reading the model: %v", err)
		}
		outs = []string{o[0].Name}
	default:
		return nil, fmt.Errorf("unknown model family %q (rtmo or yolo)", family)
	}
	in, _, err := ort.GetInputOutputInfo(model)
	if err != nil || len(in) == 0 {
		return nil, fmt.Errorf("reading the model: %v", err)
	}
	if label == "" {
		label = filepath.Base(model)
	}
	d := &onnxDetector{family: family, label: label, minScore: c.MinScore, outNames: outs,
		input: make([]float32, 3*netSize*netSize)}
	// GPU first, then the CPU.
	for _, accel := range accelerators() {
		opts, err := ort.NewSessionOptions()
		if err != nil {
			return nil, err
		}
		ok := true
		switch accel {
		case "DirectML":
			ok = opts.AppendExecutionProviderDirectML(0) == nil
		case "CoreML":
			ok = opts.AppendExecutionProviderCoreMLV2(map[string]string{"ModelFormat": "MLProgram", "MLComputeUnits": "ALL"}) == nil
		}
		if ok {
			d.sess, err = ort.NewDynamicAdvancedSession(model, []string{in[0].Name}, outs, opts)
		}
		opts.Destroy()
		if ok && err == nil {
			d.accel = accel
			// first run compiles; do it now, not on the first live frame
			if _, err := d.Detect(&Frame{W: 64, H: 36, RGB: make([]byte, 64*36*3)}); err != nil {
				d.sess.Destroy()
				d.sess = nil
				continue
			}
			return d, nil
		}
	}
	return nil, fmt.Errorf("ONNX Runtime couldn't open %s: %v", filepath.Base(model), err)
}

func accelerators() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{"DirectML", "CPU"}
	}
	// CoreML can't run RTMO when a frame has nobody in it (a zero-length
	// dynamic shape), so a Mac uses the CPU: about 50 ms a frame on Apple
	// silicon, enough for testing.
	return []string{"CPU"}
}

func (d *onnxDetector) Describe() string { return d.label + " · " + d.accel }

func (d *onnxDetector) Close() error {
	if d.sess != nil {
		return d.sess.Destroy()
	}
	return nil
}

// letterbox scales f into the 640×640 input at the top-left, padded with
// grey, and returns the scale used.
func (d *onnxDetector) letterbox(f *Frame) float64 {
	r := math.Min(float64(netSize)/float64(f.W), float64(netSize)/float64(f.H))
	nw, nh := int(float64(f.W)*r), int(float64(f.H)*r)
	plane := netSize * netSize
	pad := float32(114)
	if d.family == "yolo" {
		pad = 114.0 / 255
	}
	for i := range d.input {
		d.input[i] = pad
	}
	for y := 0; y < nh; y++ {
		sy := int(float64(y) / r)
		if sy >= f.H {
			sy = f.H - 1
		}
		row := f.RGB[sy*f.W*3:]
		for x := 0; x < nw; x++ {
			sx := int(float64(x) / r)
			if sx >= f.W {
				sx = f.W - 1
			}
			R, G, B := float32(row[sx*3]), float32(row[sx*3+1]), float32(row[sx*3+2])
			i := y*netSize + x
			if d.family == "yolo" {
				d.input[i], d.input[plane+i], d.input[2*plane+i] = R/255, G/255, B/255
			} else { // BGR, 0..255
				d.input[i], d.input[plane+i], d.input[2*plane+i] = B, G, R
			}
		}
	}
	return r
}

func (d *onnxDetector) Detect(f *Frame) ([]Person, error) {
	r := d.letterbox(f)
	in, err := ort.NewTensor(ort.NewShape(1, 3, netSize, netSize), d.input)
	if err != nil {
		return nil, err
	}
	defer in.Destroy()
	outs := make([]ort.Value, len(d.outNames))
	if err := d.sess.Run([]ort.Value{in}, outs); err != nil {
		return nil, err
	}
	defer func() {
		for _, o := range outs {
			if o != nil {
				o.Destroy()
			}
		}
	}()
	tensor := func(i int) ([]float32, ort.Shape, error) {
		t, ok := outs[i].(*ort.Tensor[float32])
		if !ok {
			return nil, nil, fmt.Errorf("output %s isn't float32", d.outNames[i])
		}
		return t.GetData(), t.GetShape(), nil
	}
	W, H := float64(f.W), float64(f.H)
	norm := func(x, y float64) (float64, float64) { return clamp01(x / r / W), clamp01(y / r / H) }
	var people []Person
	switch d.family {
	case "rtmo":
		dets, ds, err := tensor(0)
		if err != nil {
			return nil, err
		}
		kps, ks, err := tensor(1)
		if err != nil {
			return nil, err
		}
		if len(ds) != 3 || ds[2] < 5 {
			return nil, fmt.Errorf("unexpected dets shape %v", ds)
		}
		n, step := int(ds[1]), int(ds[2])
		kpN, kpD := 0, 0
		if len(ks) == 4 {
			kpN, kpD = int(ks[2]), int(ks[3])
		}
		for i := 0; i < n; i++ {
			v := dets[i*step:]
			if float64(v[4]) < d.minScore {
				continue
			}
			x1, y1 := norm(float64(v[0]), float64(v[1]))
			x2, y2 := norm(float64(v[2]), float64(v[3]))
			p := Person{Box: [4]float64{x1, y1, x2, y2}, Score: float64(v[4])}
			if kpN > 0 && kpD >= 2 {
				for k := 0; k < kpN; k++ {
					o := (i*kpN + k) * kpD
					x, y := norm(float64(kps[o]), float64(kps[o+1]))
					s := 1.0
					if kpD >= 3 {
						s = float64(kps[o+2])
					}
					p.Points = append(p.Points, [3]float64{x, y, s})
				}
			}
			people = append(people, p)
		}
	case "yolo":
		data, s, err := tensor(0)
		if err != nil {
			return nil, err
		}
		if len(s) != 3 {
			return nil, fmt.Errorf("unexpected output shape %v", s)
		}
		rows, n := int(s[1]), int(s[2])
		at := func(row, i int) float64 { return float64(data[row*n+i]) }
		pose := rows == 56
		for i := 0; i < n; i++ {
			sc := at(4, i)
			if sc < d.minScore {
				continue
			}
			cx, cy, w, h := at(0, i), at(1, i), at(2, i), at(3, i)
			x1, y1 := norm(cx-w/2, cy-h/2)
			x2, y2 := norm(cx+w/2, cy+h/2)
			p := Person{Box: [4]float64{x1, y1, x2, y2}, Score: sc}
			if pose {
				for k := 0; k < 17; k++ {
					x, y := norm(at(5+k*3, i), at(6+k*3, i))
					p.Points = append(p.Points, [3]float64{x, y, at(7+k*3, i)})
				}
			}
			people = append(people, p)
		}
		people = nms(people, 0.5)
	}
	return people, nil
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

func iou(a, b [4]float64) float64 {
	ix := math.Max(0, math.Min(a[2], b[2])-math.Max(a[0], b[0]))
	iy := math.Max(0, math.Min(a[3], b[3])-math.Max(a[1], b[1]))
	in := ix * iy
	u := (a[2]-a[0])*(a[3]-a[1]) + (b[2]-b[0])*(b[3]-b[1]) - in
	if u <= 0 {
		return 0
	}
	return in / u
}

func nms(ps []Person, thr float64) []Person {
	sort.Slice(ps, func(i, j int) bool { return ps[i].Score > ps[j].Score })
	var keep []Person
	for _, p := range ps {
		ok := true
		for _, k := range keep {
			if iou(p.Box, k.Box) > thr {
				ok = false
				break
			}
		}
		if ok {
			keep = append(keep, p)
		}
	}
	return keep
}
