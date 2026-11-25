package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/toolingdev/jadeck-addon-autoframe/lib"
	"github.com/toolingdev/jadeck/sdk/addon"
)

type config struct {
	Detector    string  `json:"detector"` // builtin | onnx | service
	Model       string  `json:"model"`
	Family      string  `json:"family"`
	Service     string  `json:"service"`
	MinScore    float64 `json:"minScore"`
	FPS         int     `json:"fps"`
	FFmpeg      string  `json:"ffmpeg"`
	Runtime     string  `json:"runtime"`
	TestVideo   string  `json:"testVideo"`
	Others      string  `json:"otherCameras"` // "Name, address[, rtsp path]" per line
	VideoServer string  `json:"videoServer"`  // jadeck's video gateway host ("" = the controller's)
}

// camSettings are what the operator picks per camera; they're kept on disk.
type camSettings struct {
	Mode   string     `json:"mode"`
	Shot   string     `json:"shot"`
	Zone   [4]float64 `json:"zone"`
	Target int        `json:"target"` // track id; not kept across restarts
}

func defaultCam() camSettings {
	return camSettings{Mode: lib.ModeFollow, Shot: lib.ShotWaist, Zone: [4]float64{0.04, 0.06, 0.96, 0.98}}
}

type camera struct {
	ID, Name string
	URL      string // where the tracker reads it: the camera itself when it can
	Via      string // "camera" or "jadeck" (the camera has a password jadeck keeps)
	src      *lib.Source
	cancel   context.CancelFunc
	tracker  lib.Tracker
	wanted   time.Time // last time a widget showed it

	// last result (Conn.mu)
	people  []*lib.Track
	framing lib.Framing
	jpeg    []byte
	seq     uint64
	ms      float64
	at      time.Time
}

type Conn struct {
	cfg     config
	access  *addon.ControllerAccess
	det     lib.Detector // nil until the model is ready
	detNote string       // what the detector is doing when it isn't ready
	sample  bool

	mu      sync.Mutex
	cams    map[string]*camera
	order   []string
	set     map[string]camSettings
	paused  bool
	err     string
	notify  addon.Notifier
	ctx     context.Context
	stop    context.CancelFunc
	lastPub time.Time
	// tracked ms per frame across cameras
	detMs float64
}

func settingsFile() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "jadeck-addon-autoframe", "cameras.json")
}

func open(raw json.RawMessage) (*Conn, error) {
	var cfg config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	if cfg.FPS <= 0 || cfg.FPS > 30 {
		cfg.FPS = 10
	}
	dc := lib.DetectorConfig{Kind: cfg.Detector, Model: cfg.Model, Family: cfg.Family,
		Service: cfg.Service, Runtime: cfg.Runtime, MinScore: cfg.MinScore}
	c := &Conn{cfg: cfg, access: addon.ControllerFrom(raw),
		cams: map[string]*camera{}, set: map[string]camSettings{}}
	if b, err := os.ReadFile(settingsFile()); err == nil {
		_ = json.Unmarshal(b, &c.set)
	}
	builtin := cfg.Detector == "" || cfg.Detector == "builtin"
	if builtin && !lib.BuiltinModelReady() {
		// First run: fetch the model in the background; the widget shows
		// the progress and the cameras start once it's ready.
		c.detNote = "downloading the people finder (36 MB)"
		c.ctx, c.stop = context.WithCancel(context.Background())
		go c.fetchModel(dc)
	} else {
		det, err := lib.OpenDetector(dc)
		if err != nil {
			return nil, err
		}
		c.det = det
		c.ctx, c.stop = context.WithCancel(context.Background())
	}
	go c.discover()
	go c.detectLoop()
	return c, nil
}

// fetchModel downloads the built-in model, then opens the detector.
// A failed download retries every minute.
func (c *Conn) fetchModel(dc lib.DetectorConfig) {
	for c.ctx.Err() == nil {
		last := -1
		err := lib.DownloadBuiltinModel(c.ctx, func(done, total int64) {
			pct := int(done * 100 / total)
			if pct != last && pct%5 == 0 {
				last = pct
				c.mu.Lock()
				c.detNote = fmt.Sprintf("downloading the people finder: %d%%", pct)
				c.mu.Unlock()
				c.changed()
			}
		})
		if err == nil {
			det, err2 := lib.OpenDetector(dc)
			c.mu.Lock()
			if err2 != nil {
				c.detNote, c.err = "", "detector: "+err2.Error()
			} else {
				c.det, c.detNote, c.err = det, "", ""
			}
			c.mu.Unlock()
			c.changed()
			return
		}
		if c.ctx.Err() != nil {
			return
		}
		c.mu.Lock()
		c.detNote, c.err = "", err.Error()+" (trying again in a minute)"
		c.mu.Unlock()
		c.changed()
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(time.Minute):
		}
		c.mu.Lock()
		c.err, c.detNote = "", "downloading the people finder (36 MB)"
		c.mu.Unlock()
	}
}

// detector is the open detector, nil while the model downloads.
func (c *Conn) detector() lib.Detector {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.det
}

// describe names the detector, or says what it's waiting for. Call with c.mu held.
func (c *Conn) describe() string {
	if c.det != nil {
		return c.det.Describe()
	}
	if c.detNote != "" {
		return "RTMO-s (built in) · " + c.detNote
	}
	return "people finder not ready"
}

func (c *Conn) SetNotify(n addon.Notifier) { c.mu.Lock(); c.notify = n; c.mu.Unlock() }

func (c *Conn) Close() error {
	c.stop()
	c.mu.Lock()
	for _, k := range c.cams {
		k.cancel()
	}
	det := c.det
	c.mu.Unlock()
	if det == nil {
		return nil
	}
	return det.Close()
}

func (c *Conn) changed() {
	c.mu.Lock()
	n := c.notify
	c.mu.Unlock()
	if n != nil {
		n()
	}
}

func (c *Conn) saveSettings() {
	b, _ := json.MarshalIndent(c.set, "", "  ")
	_ = os.MkdirAll(filepath.Dir(settingsFile()), 0o755)
	_ = os.WriteFile(settingsFile(), b, 0o644)
}

func (c *Conn) camSet(id string) camSettings {
	s, ok := c.set[id]
	if !ok {
		s = defaultCam()
	}
	return s
}

// ------------------------------------------------------------ cameras

type apiCamera struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Host        string `json:"host"`
	RTSPPort    int    `json:"rtspPort"`
	StreamSD    string `json:"streamSd"`
	User        string `json:"user"`
	HasPassword bool   `json:"hasPassword"`
	Video       bool   `json:"video"`
}

type camInfo struct{ id, name, url, via string }

// cameraInfos is every camera the tracker can watch: jadeck's cameras
// (read straight from the camera's own small stream; through jadeck's
// video only when the camera has a password, which jadeck doesn't hand
// out), the cameras typed into the connection, and the test video.
func (c *Conn) cameraInfos() ([]camInfo, error) {
	var out []camInfo
	if c.cfg.TestVideo != "" {
		out = append(out, camInfo{"test", "Test video", c.cfg.TestVideo, "file"})
	}
	for i, line := range strings.Split(c.cfg.Others, "\n") {
		parts := strings.Split(line, ",")
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			continue
		}
		name, addr := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		u := addr
		if !strings.Contains(addr, "://") {
			path := "/2"
			if len(parts) > 2 && strings.TrimSpace(parts[2]) != "" {
				path = "/" + strings.TrimPrefix(strings.TrimSpace(parts[2]), "/")
			}
			u = "rtsp://" + addr + ":554" + path
			if strings.Contains(addr, ":") {
				u = "rtsp://" + addr + path
			}
		}
		out = append(out, camInfo{fmt.Sprintf("own-%d", i+1), name, u, "camera"})
	}
	cams, gw, err := c.listCameras()
	for _, k := range cams {
		port := k.RTSPPort
		if port == 0 {
			port = 554
		}
		path := k.StreamSD
		if path == "" {
			path = "/2"
		}
		ci := camInfo{k.ID, k.Name, "", "camera"}
		switch {
		case !k.HasPassword:
			ci.url = fmt.Sprintf("rtsp://%s:%d/%s", k.Host, port, strings.TrimPrefix(path, "/"))
		case k.Video:
			ci.url, ci.via = fmt.Sprintf("rtsp://%s:8554/cam-%s-sd", gw, k.ID), "jadeck"
		default:
			continue // password-protected and not shared through jadeck's video
		}
		out = append(out, ci)
	}
	return out, err
}

func (c *Conn) discover() {
	for {
		infos, err := c.cameraInfos()
		c.mu.Lock()
		c.err = ""
		if err != nil {
			c.err = err.Error()
		}
		want := map[string]camInfo{}
		var order []string
		for _, ci := range infos {
			want[ci.id] = ci
			order = append(order, ci.id)
		}
		for id, k := range c.cams {
			if ci, ok := want[id]; !ok || ci.url != k.URL {
				if k.cancel != nil {
					k.cancel()
				}
				delete(c.cams, id)
			}
		}
		for _, id := range order {
			if k, ok := c.cams[id]; ok {
				k.Name = want[id].name
				continue
			}
			ci := want[id]
			c.cams[id] = &camera{ID: id, Name: ci.name, URL: ci.url, Via: ci.via}
		}
		c.order = order
		c.mu.Unlock()
		c.changed()
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
	}
}

// watch starts reading a camera's video when a widget shows it, and stops
// a minute after no widget has.
func (c *Conn) watch(k *camera) {
	k.wanted = time.Now()
	if k.src == nil {
		ctx, cancel := context.WithCancel(c.ctx)
		k.src = &lib.Source{URL: k.URL, FFmpeg: c.cfg.FFmpeg, FPS: c.cfg.FPS}
		k.cancel = cancel
		go k.src.Run(ctx)
	}
}

func (c *Conn) idleCameras() {
	for _, k := range c.cams {
		if k.src != nil && time.Since(k.wanted) > time.Minute {
			k.cancel()
			k.src, k.cancel, k.jpeg, k.people = nil, nil, nil, nil
			k.tracker = lib.Tracker{}
		}
	}
}

func (c *Conn) listCameras() ([]apiCamera, string, error) {
	if c.access == nil || c.access.URL == "" {
		return nil, "", nil
	}
	u, err := url.Parse(c.access.URL)
	if err != nil {
		return nil, "", err
	}
	host := u.Hostname()
	if c.cfg.VideoServer != "" {
		host = c.cfg.VideoServer
	}
	req, _ := http.NewRequestWithContext(c.ctx, "GET", strings.TrimSuffix(c.access.URL, "/")+"/api/cameras", nil)
	req.Header.Set("Authorization", "Bearer "+c.access.Token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("jadeck's camera list: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, "", fmt.Errorf("jadeck answered %d for the camera list (tick \"See cameras\")", res.StatusCode)
	}
	var list []apiCamera
	if err := json.NewDecoder(res.Body).Decode(&list); err != nil {
		return nil, "", fmt.Errorf("jadeck's camera list: %v", err)
	}
	return list, host, nil
}

// ------------------------------------------------------------ detection

// detectLoop runs the detector over the cameras in turn, each on its
// newest frame.
func (c *Conn) detectLoop() {
	tick := time.NewTicker(time.Second / time.Duration(c.cfg.FPS))
	defer tick.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-tick.C:
		}
		c.mu.Lock()
		paused := c.paused
		var cams []*camera
		c.idleCameras()
		for _, id := range c.order {
			if k := c.cams[id]; k != nil && k.src != nil {
				cams = append(cams, k)
			}
		}
		c.mu.Unlock()
		if paused {
			continue
		}
		did := false
		for _, k := range cams {
			f := k.src.Latest()
			c.mu.Lock()
			fresh := f != nil && f.Seq != k.seq && time.Since(f.At) < 2*time.Second
			c.mu.Unlock()
			if !fresh {
				continue
			}
			det := c.detector()
			if det == nil {
				break
			}
			t0 := time.Now()
			ps, err := det.Detect(f)
			ms := float64(time.Since(t0).Microseconds()) / 1000
			if err != nil {
				c.mu.Lock()
				c.err = "detector: " + err.Error()
				c.mu.Unlock()
				continue
			}
			pic := preview(f)
			c.mu.Lock()
			s := c.camSet(k.ID)
			people := k.tracker.Update(ps, f.At)
			k.people, k.framing = people, lib.FrameShot(people, s.Mode, s.Shot, s.Zone, s.Target)
			k.jpeg, k.seq, k.ms, k.at = pic, f.Seq, ms, f.At
			c.detMs = 0.8*c.detMs + 0.2*ms
			c.mu.Unlock()
			did = true
		}
		if did {
			c.changed()
		}
	}
}

// preview is the frame as a small JPEG for the widget.
func preview(f *lib.Frame) []byte {
	const w, h = 480, 270
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		sy := y * f.H / h
		for x := 0; x < w; x++ {
			sx := x * f.W / w
			i, j := (sy*f.W+sx)*3, (y*w+x)*4
			img.Pix[j], img.Pix[j+1], img.Pix[j+2], img.Pix[j+3] = f.RGB[i], f.RGB[i+1], f.RGB[i+2], 255
		}
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, &jpeg.Options{Quality: 70})
	return b.Bytes()
}

// ------------------------------------------------------------ state

func r3(v float64) float64 { return float64(int(v*1000+0.5)) / 1000 }

func box(b [4]float64) []float64 { return []float64{r3(b[0]), r3(b[1]), r3(b[2]), r3(b[3])} }

func (c *Conn) AddonState(ctx context.Context) (map[string]any, error) {
	return c.AddonStateFor(ctx, nil)
}

// AddonOptions lists the cameras for the widget's camera picker.
func (c *Conn) AddonOptions(ctx context.Context, name string) ([]addon.Option, error) {
	if name != "autoframe.cameras" {
		return nil, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []addon.Option
	for _, id := range c.order {
		out = append(out, addon.Option{Value: id, Label: c.cams[id].Name})
	}
	return out, nil
}

func pickedCameras(settings map[string]any) []string {
	var ids []string
	if v, ok := settings["cameras"].([]any); ok {
		for _, x := range v {
			if s, ok := x.(string); ok && s != "" {
				ids = append(ids, s)
			}
		}
	}
	return ids
}

// AddonStateFor sends a widget the cameras picked in its settings, and
// starts watching them.
func (c *Conn) AddonStateFor(ctx context.Context, settings map[string]any) (map[string]any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	picked := pickedCameras(settings)
	var cams []map[string]any
	for _, id := range picked {
		k := c.cams[id]
		if k == nil {
			continue
		}
		c.watch(k)
		s := c.camSet(id)
		in := map[int]bool{}
		for _, sid := range k.framing.Subjects {
			in[sid] = true
		}
		var people []map[string]any
		for _, t := range k.people {
			people = append(people, map[string]any{"id": t.ID, "box": box(t.Box), "inShot": in[t.ID], "target": t.ID == s.Target})
		}
		sort.Slice(people, func(i, j int) bool { return people[i]["box"].([]float64)[0] < people[j]["box"].([]float64)[0] })
		status := "starting"
		if k.src != nil {
			status = k.src.Status()
		}
		cam := map[string]any{
			"id": id, "name": k.Name, "via": k.Via, "mode": s.Mode, "shot": s.Shot, "zone": box(s.Zone), "target": s.Target,
			"video": status, "people": people,
		}
		if k.jpeg != nil {
			cam["frame"] = fmt.Sprintf("res:frame/%s.jpg?seq=%d", id, k.seq)
			cam["move"] = k.framing.Move
			cam["holding"] = k.framing.Holding
			cam["lost"] = k.framing.Lost
			if !k.framing.Holding || len(k.framing.Subjects) > 0 {
				cam["aim"] = box(k.framing.Aim)
			}
			cam["age"] = time.Since(k.at).Milliseconds()
		}
		cams = append(cams, cam)
	}
	st := map[string]any{
		"cameras":   cams,
		"available": len(c.order),
		"detector":  c.describe(),
		"ms":        int(c.detMs + 0.5),
		"paused":    c.paused,
		"shadow":    true, // v1 never moves a camera
	}
	if c.err != "" {
		st["error"] = c.err
	}
	return st, nil
}

func (c *Conn) Resource(ctx context.Context, path string) (string, []byte, error) {
	p, _, _ := strings.Cut(path, "?")
	id, ok := strings.CutPrefix(p, "frame/")
	if !ok {
		return "", nil, addon.ErrNotFound
	}
	id = strings.TrimSuffix(id, ".jpg")
	c.mu.Lock()
	defer c.mu.Unlock()
	if k := c.cams[id]; k != nil && k.jpeg != nil {
		return "image/jpeg", k.jpeg, nil
	}
	return "", nil, addon.ErrNotFound
}

// ------------------------------------------------------------ actions

type actParams struct {
	Camera string    `json:"camera"`
	Mode   string    `json:"mode"`
	Shot   string    `json:"shot"`
	X      float64   `json:"x"`
	Y      float64   `json:"y"`
	Zone   []float64 `json:"zone"`
}

func (c *Conn) AddonAction(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	var p actParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
	}
	c.mu.Lock()
	defer func() { c.mu.Unlock(); c.changed() }()
	if name == "pause" {
		c.paused = !c.paused
		return map[string]any{"paused": c.paused}, nil
	}
	k := c.cams[p.Camera]
	if k == nil {
		return nil, fmt.Errorf("no camera %q", p.Camera)
	}
	s := c.camSet(p.Camera)
	switch name {
	case "set_mode":
		switch p.Mode {
		case lib.ModeFollow, lib.ModePair, lib.ModeGroup, lib.ModeHold:
			s.Mode = p.Mode
		default:
			return nil, fmt.Errorf("mode must be follow, pair, group or hold")
		}
	case "set_shot":
		switch p.Shot {
		case lib.ShotClose, lib.ShotWaist, lib.ShotWide:
			s.Shot = p.Shot
		default:
			return nil, fmt.Errorf("shot must be close, waist or wide")
		}
	case "pick":
		if t := lib.Find(k.people, p.X, p.Y); t != nil {
			if s.Target == t.ID {
				s.Target = 0 // tap again: let go
			} else {
				s.Target = t.ID
				if s.Mode == lib.ModeGroup || s.Mode == lib.ModeHold {
					s.Mode = lib.ModeFollow
				}
			}
		} else {
			s.Target = 0
		}
	case "set_zone":
		if len(p.Zone) != 4 {
			return nil, fmt.Errorf("zone needs x1, y1, x2, y2")
		}
		z := [4]float64{p.Zone[0], p.Zone[1], p.Zone[2], p.Zone[3]}
		if z[0] > z[2] {
			z[0], z[2] = z[2], z[0]
		}
		if z[1] > z[3] {
			z[1], z[3] = z[3], z[1]
		}
		if z[2]-z[0] < 0.05 || z[3]-z[1] < 0.05 {
			return nil, fmt.Errorf("the zone is too small")
		}
		for i := range z {
			z[i] = max(0, min(1, z[i]))
		}
		s.Zone = z
	default:
		return nil, fmt.Errorf("unknown action %q", name)
	}
	c.set[p.Camera] = s
	if name != "pick" {
		c.saveSettings()
	}
	k.framing = lib.FrameShot(k.people, s.Mode, s.Shot, s.Zone, s.Target)
	return map[string]any{"ok": true}, nil
}
