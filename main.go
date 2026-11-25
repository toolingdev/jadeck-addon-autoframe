// Auto frame: a jadeck add-on that finds the people in each camera's picture
// and works out the shot to frame: one person, the pastor and translator,
// or everyone on stage. It reads each camera's video straight from the
// camera, so it works with any camera that streams RTSP, not one brand's
// add-on. This first version is shadow mode: it shows what it would do
// and never moves a camera.
package main

import (
	"encoding/json"

	"github.com/toolingdev/jadeck/sdk/addon"
)

// Version is set by the release build.
var Version = "0.1.0"

func main() { addon.Serve(spec()) }

const changelog = `0.1.0: first release, shadow mode (never moves a camera).
- Finds people in each picked camera's picture with the built-in model (RTMO-s, downloaded the first time), your own ONNX model, or a detection service on your network.
- Keeps each person's identity from frame to frame; tap a person to follow them.
- Modes per camera: Follow, Pair, Group, Hold; shots Close, Waist, Wide; a stage zone so the congregation is ignored.
- Shows the shot it would frame and the move it would make ("pan right 12% · zoom in 1.6×").`

var sampleState = map[string]any{
	"detector": "RTMO-s (built in) · DirectML", "ms": 9, "paused": false, "shadow": true, "available": 2,
	"cameras": []map[string]any{
		{"id": "a", "name": "Pulpit", "via": "camera", "mode": "pair", "shot": "waist", "zone": []float64{0.12, 0.08, 0.96, 0.96},
			"video": "live", "move": "in frame: holding", "holding": true, "aim": []float64{0.3, 0.2, 0.72, 0.62}, "target": 3,
			"people": []map[string]any{
				{"id": 3, "box": []float64{0.36, 0.3, 0.5, 0.92}, "inShot": true, "target": true},
				{"id": 5, "box": []float64{0.55, 0.34, 0.66, 0.9}, "inShot": true},
				{"id": 7, "box": []float64{0.02, 0.6, 0.08, 0.98}}}},
		{"id": "b", "name": "Wide", "via": "camera", "mode": "group", "shot": "wide", "zone": []float64{0.04, 0.08, 0.96, 0.96},
			"video": "live", "move": "zoom in 1.3×", "aim": []float64{0.12, 0.12, 0.88, 0.88},
			"people": []map[string]any{
				{"id": 1, "box": []float64{0.16, 0.36, 0.24, 0.8}, "inShot": true},
				{"id": 2, "box": []float64{0.32, 0.38, 0.4, 0.8}, "inShot": true},
				{"id": 4, "box": []float64{0.48, 0.36, 0.56, 0.8}, "inShot": true},
				{"id": 6, "box": []float64{0.64, 0.38, 0.72, 0.8}, "inShot": true}}},
	},
}

func spec() addon.Spec {
	return addon.Spec{
		Manifest: addon.Manifest{
			ID: "autoframe", Name: "Auto frame", Version: Version, Publisher: "jadeck", APIVersion: addon.APIVersion,
			Repo: "toolingdev/jadeck-addon-autoframe", Controller: true,
			Description: "Finds the people on stage in each camera and frames the shot: one person, a pair, or the band.",
			Reaches:     "Each camera you pick (its RTSP video, read directly), jadeck's camera list, and a detection service only if you add one",
			ConnectionTypes: []addon.ConnectionType{{
				ID: "autoframe", Label: "Auto frame", Capabilities: []string{addon.CapTool},
				Description: "Runs the people finder on this computer (on the graphics card when it can). Pick the cameras in each Auto frame widget.",
				Needs:       "ffmpeg · a camera with RTSP video",
				Fields: []addon.Field{
					{Key: "detector", Label: "People finder", Type: "select", Default: "builtin", Options: []addon.Option{
						{Value: "builtin", Label: "Built in (RTMO-s)"}, {Value: "onnx", Label: "My own model file (ONNX)"}, {Value: "service", Label: "A detection service on my network"}},
						Help: "Built in works for most rooms. Bring your own model if you've trained one on your stage."},
					{Key: "model", Label: "Model file", Type: "text", Placeholder: `C:\models\stage.onnx`, Help: "Only for My own model file."},
					{Key: "family", Label: "Model family", Type: "select", Default: "rtmo", Options: []addon.Option{
						{Value: "rtmo", Label: "RTMO (people + body points)"}, {Value: "yolo", Label: "YOLO v8/11 layout (detect or pose)"}},
						Help: "How the model lays out its answer. Only for My own model file."},
					{Key: "service", Label: "Service address", Type: "text", Placeholder: "http://192.168.1.50:9000/detect",
						Help: "Only for a detection service: it gets each frame as a JPEG and answers the people as JSON (see the add-on's README). Keep it on your network."},
					{Key: "fps", Label: "Looks a second", Type: "number", Default: 10, Min: addon.Num(2), Max: addon.Num(30),
						Help: "Per camera. 10 is smooth enough to follow someone walking."},
					{Key: "minScore", Label: "How sure before it counts someone", Type: "number", Default: 0.35, Min: addon.Num(0.1), Max: addon.Num(0.9), Advanced: true},
					{Key: "otherCameras", Label: "Other cameras", Type: "textarea", Advanced: true, Placeholder: "Balcony, 192.168.1.90, /2",
						Help: "Cameras jadeck doesn't control: one per line, name, address, and the RTSP path (or a full rtsp:// address)."},
					{Key: "testVideo", Label: "Test video", Type: "text", Advanced: true, Placeholder: "/Users/me/Movies/service.mp4",
						Help: "A recording to try the tracker on; it shows up as a camera called Test video."},
					{Key: "ffmpeg", Label: "ffmpeg", Type: "text", Advanced: true, Placeholder: "ffmpeg", Help: "Blank: the one on the PATH."},
					{Key: "permissions", Label: "It may", Type: "scopes", Default: []string{"camera.read"},
						Help: "See cameras is all this version needs (it reads jadeck's camera list)."},
				},
				Sample:     map[string]any{"detector": "builtin", "fps": 10},
				SampleTest: "RTMO-s (built in) · DirectML · 2 cameras",
			}},
			Actions: []addon.ActionDef{
				{ID: "set_mode", Label: "Framing mode", Params: []addon.Field{{Key: "camera", Label: "Camera", Type: "text"}, {Key: "mode", Label: "Mode", Type: "select",
					Options: []addon.Option{{Value: "follow", Label: "Follow"}, {Value: "pair", Label: "Pair"}, {Value: "group", Label: "Group"}, {Value: "hold", Label: "Hold"}}}}, MacroStep: true},
				{ID: "set_shot", Label: "Framing shot", Params: []addon.Field{{Key: "camera", Label: "Camera", Type: "text"}, {Key: "shot", Label: "Shot", Type: "select",
					Options: []addon.Option{{Value: "close", Label: "Close"}, {Value: "waist", Label: "Waist"}, {Value: "wide", Label: "Wide"}}}}, MacroStep: true},
				{ID: "pick", Label: "Pick a person", Params: []addon.Field{{Key: "camera", Label: "Camera", Type: "text"}, {Key: "x", Label: "x", Type: "number"}, {Key: "y", Label: "y", Type: "number"}}},
				{ID: "set_zone", Label: "Stage zone", Params: []addon.Field{{Key: "camera", Label: "Camera", Type: "text"}, {Key: "zone", Label: "Zone", Type: "json"}}},
				{ID: "pause", Label: "Pause auto frame"},
			},
			Widgets: []addon.WidgetDef{
				{Kind: addon.KindFrame, Type: "frame", Label: "Auto frame",
					Description: "Each picked camera's picture with the people it found, the shot it would frame, and Follow / Pair / Group / Hold.",
					Entry:       "ui/stage.html", Size: &addon.Size{W: 12, H: 8}, MinPx: &addon.Size{W: 280, H: 220},
					Fields: []addon.Field{{Key: "cameras", Label: "Cameras", Type: addon.FieldOrder, OptionsFrom: "autoframe.cameras",
						Help: "Pick the cameras this widget tracks, in order. The tracker reads each one's video itself."}},
					Sample: sampleState},
			},
			Changelog: changelog,
		},
		Open: map[string]addon.OpenFunc{"autoframe": func(raw json.RawMessage) (any, error) { return open(raw) }},
	}
}
