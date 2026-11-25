// Package lib is the tracker: detectors, identities, framing.
package lib

// The detector slot: whatever finds people in a frame. Everything after it
// (identities, framing, camera moves) is the tracker's own, so any detector
// that keeps this contract plugs in: the built-in model, an ONNX file of a
// known family, or a detection service on the network.

import (
	"fmt"
	"strings"
	"time"
)

// Frame is one decoded picture: packed RGB, row by row.
type Frame struct {
	W, H int
	RGB  []byte
	At   time.Time // when it arrived from the camera
	Seq  uint64
}

// Person is one detection. Box and Points are normalized to the frame
// (0..1, origin top-left). Points, when the detector gives them, are the
// 17 COCO body points (nose, eyes, ears, shoulders, elbows, wrists, hips,
// knees, ankles) as x, y, confidence.
type Person struct {
	Box    [4]float64   `json:"box"` // x1, y1, x2, y2
	Score  float64      `json:"score"`
	Points [][3]float64 `json:"points,omitempty"`
}

// Detector finds people. Detect may be called from one goroutine at a time.
type Detector interface {
	Detect(f *Frame) ([]Person, error)
	// Describe says what runs, for the widget: "RTMO-s · CoreML".
	Describe() string
	Close() error
}

// COCO body point indexes.
const (
	kpNose      = 0
	kpLShoulder = 5
	kpRShoulder = 6
	kpLHip      = 11
	kpRHip      = 12
	kpLAnkle    = 15
	kpRAnkle    = 16
)

// DetectorConfig is the connection's detector choice.
type DetectorConfig struct {
	Kind     string  // builtin | onnx | service
	Model    string  // onnx: file
	Family   string  // onnx: rtmo | yolo
	Service  string  // service: URL
	Runtime  string  // ONNX Runtime library path ("" = find it)
	MinScore float64 // drop weaker detections
}

func OpenDetector(c DetectorConfig) (Detector, error) {
	if c.MinScore <= 0 {
		c.MinScore = 0.35
	}
	switch c.Kind {
	case "", "builtin":
		return openONNX(builtinModel(), "rtmo", "RTMO-s (built in)", c)
	case "onnx":
		if c.Model == "" {
			return nil, fmt.Errorf("pick the model file")
		}
		return openONNX(c.Model, c.Family, "", c)
	case "service":
		if !strings.HasPrefix(c.Service, "http://") && !strings.HasPrefix(c.Service, "https://") {
			return nil, fmt.Errorf("the service address must start with http:// or https://")
		}
		return newServiceDetector(c.Service, c.MinScore), nil
	}
	return nil, fmt.Errorf("unknown detector %q", c.Kind)
}
