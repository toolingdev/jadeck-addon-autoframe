# Changelog

## 0.1.0

First release, shadow mode: it shows what it would do and never moves a
camera.

- Finds people in each picked camera's picture with the built-in model
  (RTMO-s, downloaded and checked the first time it runs), your own ONNX
  model, or a detection service on your network.
- Keeps each person's identity from frame to frame; tap a person to follow
  them.
- Modes per camera: Follow, Pair, Group, Hold; shots Close, Waist, Wide; a
  stage zone judged by where people's feet are.
- Shows the shot it would frame and the move it would make.
