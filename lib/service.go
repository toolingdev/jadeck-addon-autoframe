package lib

// A detection service on the network: the tracker POSTs each frame as a
// JPEG (Content-Type image/jpeg) and the service answers
//
//	{"people": [{"box": [x1, y1, x2, y2], "score": 0.92,
//	             "points": [[x, y, confidence], ...]}]}
//
// with box and points normalized to the picture (0..1, origin top-left);
// points are optional (17 COCO body points when given). Anything that
// speaks this can be the detector: your own model in Python, an inference
// server on a GPU box. Keep it on the local network: every frame makes a
// round trip.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/url"
	"time"
)

type serviceDetector struct {
	url      string
	minScore float64
	client   *http.Client
	buf      bytes.Buffer
}

func newServiceDetector(u string, minScore float64) *serviceDetector {
	return &serviceDetector{url: u, minScore: minScore, client: &http.Client{Timeout: 2 * time.Second}}
}

func (s *serviceDetector) Describe() string {
	if u, err := url.Parse(s.url); err == nil {
		return "Service · " + u.Host
	}
	return "Service"
}

func (s *serviceDetector) Close() error { return nil }

func (s *serviceDetector) Detect(f *Frame) ([]Person, error) {
	s.buf.Reset()
	if err := jpeg.Encode(&s.buf, rgbImage(f), &jpeg.Options{Quality: 80}); err != nil {
		return nil, err
	}
	res, err := s.client.Post(s.url, "image/jpeg", bytes.NewReader(s.buf.Bytes()))
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 200))
		return nil, fmt.Errorf("service answered %d: %s", res.StatusCode, b)
	}
	var out struct {
		People []Person `json:"people"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("service answer: %w", err)
	}
	var keep []Person
	for _, p := range out.People {
		if p.Score >= s.minScore || p.Score == 0 {
			for i := range p.Box {
				p.Box[i] = clamp01(p.Box[i])
			}
			keep = append(keep, p)
		}
	}
	return keep, nil
}

// rgbImage wraps a frame as an image.Image (copying into RGBA).
func rgbImage(f *Frame) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, f.W, f.H))
	for i, j := 0, 0; i+2 < len(f.RGB) && j+3 < len(img.Pix); i, j = i+3, j+4 {
		img.Pix[j], img.Pix[j+1], img.Pix[j+2], img.Pix[j+3] = f.RGB[i], f.RGB[i+1], f.RGB[i+2], 255
	}
	return img
}
