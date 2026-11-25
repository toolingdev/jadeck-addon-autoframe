# Auto frame — jadeck add-on

Finds the people on stage in each camera's picture and works out the shot
to frame: one speaker, a speaker and a translator, or the whole band. Pick
any number of cameras in the widget; each one gets its own mode and shot.

**This version is shadow mode:** it shows what it would do ("pan right
12% · zoom in 1.6×") and never moves a camera. Watch it through a service
or two before trusting it with the real thing.

It reads each camera's video straight from the camera (RTSP), so it works
with any camera that streams, not one brand's add-on.

## Quick setup

1. Install ffmpeg (it decodes the video):
   - Windows: `winget install Gyan.FFmpeg`
   - Mac: `brew install ffmpeg`
2. ONNX Runtime (it runs the people finder) downloads itself the first time,
   from Microsoft's official package (about 20 MB). To use your own build
   instead (for example one with DirectML for the graphics card), set
   `ONNXRUNTIME_LIB` to its path. Intel Macs: `brew install onnxruntime`.
3. In jadeck: **Settings → Connections → Add → Auto frame**. The defaults
   work for most rooms.
4. Add the **Auto frame** widget to a dashboard and pick its cameras.

The first time it runs, the add-on downloads ONNX Runtime and the built-in
people finder (RTMO-s, 36 MB, from OpenMMLab), checks both against known
checksums, and keeps them in your user settings folder, so updates don't
download them again:

- Mac: `~/Library/Application Support/jadeck-addon-autoframe/`
- Windows: `%AppData%\jadeck-addon-autoframe\`

No internet at the venue? Copy `models/rtmo-s.onnx` and the
`onnxruntime-1.32.0` folder there from a computer that has them.

It runs on the CPU (about 50 ms a frame on Apple silicon). Graphics-card
support on Windows is planned.

## Using it

- **Modes:** Follow (one person), Pair (a speaker and a translator), Group
  (everyone in the stage zone), Hold (don't move).
- **Shots:** Close (chest up), Waist, Wide (the whole visible body).
- **Tap a person** to follow them; tap them again to let go.
- **Drag on the picture** to draw the stage zone. People count only when
  their feet are inside it, so someone walking in front of the stage is
  ignored.
- **Pause all** stops every camera's tracking at once.

## Bring your own people finder

In the connection's **People finder** setting:

- **Built in (RTMO-s):** the default.
- **My own model file (ONNX):** an RTMO model, or a YOLO v8/11 detect or
  pose model. Set **Model family** to match.
- **A detection service on my network:** the add-on POSTs each frame as a
  JPEG and expects:

  ```json
  {"people": [{"box": [x1, y1, x2, y2], "score": 0.9,
               "points": [[x, y, confidence], ...]}]}
  ```

  Coordinates are 0..1 of the picture. `points` are the 17 COCO body
  points and are optional (without them, shots are cut by proportion).

## Cameras jadeck doesn't control

Add them under **Other cameras**, one per line: `Name, address, path`
(or a full `rtsp://` address). A recording works too: set **Test video**
to a file and it shows up as a camera called Test video.

## Building

The ONNX Runtime binding needs cgo, so build on the platform you're
building for (CI builds each platform on its own runner; see
`.github/workflows/release.yml`):

```
go build -o bin/$(go env GOOS)-$(go env GOARCH)/addon .
./bin/$(go env GOOS)-$(go env GOARCH)/addon --manifest > addon.json
mkdir -p dist && go run ./cmd/mkpack . dist/autoframe.ptzaddon
```

Tools: `cmd/replay` runs the tracker over a video and saves snapshots;
`cmd/detect` draws what the model finds in one picture; `cmd/inspect`
prints a model's inputs and outputs; `cmd/runtimesums` refreshes the ONNX
Runtime checksums after a version change.

## Credits

RTMO-s: OpenMMLab MMPose, Apache-2.0. ONNX Runtime: Microsoft, MIT.
