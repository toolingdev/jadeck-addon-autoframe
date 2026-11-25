// Auto frame widget: each picked camera's picture with the people found,
// the stage zone (drag to redraw), the shot it would frame, and the mode
// and shot pickers. Shadow mode: nothing here moves a camera.
(function () {
  "use strict";
  var JK = window.JK, K = window.Kit, NS = "http://www.w3.org/2000/svg";
  var main = document.getElementById("main");
  var hdr = JK.header({ title: "Auto frame" });
  var pauseBtn = JK.button({ label: "Pause all", onClick: function () { return K.act("pause"); } });
  pauseBtn.style.minHeight = "28px";
  pauseBtn.style.padding = "0 10px";
  var cards = {}; // camera id → card

  var MODES = [{ value: "follow", label: "Follow" }, { value: "pair", label: "Pair" }, { value: "group", label: "Group" }, { value: "hold", label: "Hold" }];
  var SHOTS = [{ value: "close", label: "Close" }, { value: "waist", label: "Waist" }, { value: "wide", label: "Wide" }];

  function svg(tag, attrs, parent) {
    var e = document.createElementNS(NS, tag);
    for (var k in attrs) e.setAttribute(k, attrs[k]);
    if (parent) parent.appendChild(e);
    return e;
  }

  function card(cam) {
    var c = { id: cam.id, seq: null, loading: false, url: null };
    c.el = K.el("section", "cam");
    var top = JK.el("div", "cam-top", c.el);
    c.name = JK.el("span", "cam-name", top);
    c.via = JK.el("span", "cam-via", top);
    c.badge = JK.el("span", "badge", top);
    var body = JK.el("div", "body", c.el);
    c.stage = JK.el("div", "stage", body);
    c.img = JK.el("img", "", c.stage);
    c.img.alt = "";
    c.svg = svg("svg", { viewBox: "0 0 1600 900", preserveAspectRatio: "none" }, c.stage);
    c.msg = JK.el("div", "msg", c.stage);
    var ctl = JK.el("div", "ctl", body);
    var r1 = JK.el("div", "row", ctl);
    c.mode = JK.segmented({ options: MODES, onChange: function (v) { K.act("set_mode", { camera: c.id, mode: v }); } });
    r1.appendChild(c.mode);
    var r2 = JK.el("div", "row", ctl);
    JK.el("span", "lbl", r2, "SHOT");
    c.shot = JK.segmented({ options: SHOTS, onChange: function (v) { K.act("set_shot", { camera: c.id, shot: v }); } });
    r2.appendChild(c.shot);
    c.move = JK.el("div", "move", ctl);
    JK.el("i", "", c.move);
    c.moveText = JK.el("span", "", c.move);
    c.foot = JK.el("div", "foot", ctl);
    c.footL = JK.el("span", "", c.foot);
    c.footR = JK.el("span", "", c.foot);
    JK.el("div", "hint", ctl, "Tap a person to follow them. Drag on the picture to draw the stage zone.");
    pointer(c);
    return c;
  }

  // tap = pick a person; drag = draw the stage zone
  function pointer(c) {
    var start = null, box = null;
    function pos(e) {
      var r = c.stage.getBoundingClientRect();
      return { x: Math.min(1, Math.max(0, (e.clientX - r.left) / r.width)), y: Math.min(1, Math.max(0, (e.clientY - r.top) / r.height)) };
    }
    c.stage.addEventListener("pointerdown", function (e) {
      if (K.editing) return;
      start = pos(e);
      c.stage.setPointerCapture(e.pointerId);
      JK.holds++;
    });
    c.stage.addEventListener("pointermove", function (e) {
      if (!start) return;
      var p = pos(e);
      if (!box && Math.hypot(p.x - start.x, p.y - start.y) < 0.04) return;
      if (!box) box = svg("rect", { class: "drawing", fill: "rgba(245,165,36,.08)", stroke: "#f5a524", "stroke-width": 3, "stroke-dasharray": "14 10" }, c.svg);
      box.setAttribute("x", Math.min(start.x, p.x) * 1600);
      box.setAttribute("y", Math.min(start.y, p.y) * 900);
      box.setAttribute("width", Math.abs(p.x - start.x) * 1600);
      box.setAttribute("height", Math.abs(p.y - start.y) * 900);
    });
    function end(e) {
      if (!start) return;
      var p = pos(e), s = start;
      start = null;
      JK.holds = Math.max(0, JK.holds - 1);
      if (box) {
        box.remove();
        box = null;
        K.act("set_zone", { camera: c.id, zone: [s.x, s.y, p.x, p.y] }).finally(K.idle);
      } else {
        K.act("pick", { camera: c.id, x: p.x, y: p.y }).finally(K.idle);
      }
    }
    c.stage.addEventListener("pointerup", end);
    c.stage.addEventListener("pointercancel", function () { start = null; if (box) { box.remove(); box = null; } JK.holds = Math.max(0, JK.holds - 1); K.idle(); });
  }

  function rect(b, attrs, parent) {
    var a = { x: b[0] * 1600, y: b[1] * 900, width: Math.max(0, (b[2] - b[0]) * 1600), height: Math.max(0, (b[3] - b[1]) * 900), rx: 8 };
    for (var k in attrs) a[k] = attrs[k];
    return svg("rect", a, parent);
  }

  function label(x, y, text, color, parent) {
    var w = text.length * 15 + 18;
    svg("rect", { x: x, y: y - 30, width: w, height: 28, rx: 5, fill: color }, parent);
    var t = svg("text", { x: x + 9, y: y - 10, fill: "#07080a", "font-size": 18, "font-weight": 700, "font-family": "Inter, system-ui, sans-serif" }, parent);
    t.textContent = text;
  }

  function overlay(c, cam) {
    while (c.svg.firstChild) c.svg.removeChild(c.svg.firstChild);
    if (cam.zone) rect(cam.zone, { fill: "none", stroke: "#f5a524", "stroke-width": 2.5, "stroke-dasharray": "12 9", opacity: 0.85 }, c.svg);
    var subjects = 0;
    (cam.people || []).forEach(function (p) {
      var on = p.inShot;
      if (on) subjects++;
      rect(p.box, { fill: "none", stroke: on ? "#22c55e" : "rgba(232,235,240,.45)", "stroke-width": on ? 4 : 2.5, "stroke-dasharray": on ? "" : "10 8" }, c.svg);
      if (p.target) label(p.box[0] * 1600, p.box[1] * 900, "Following", "#22c55e", c.svg);
    });
    if (cam.aim && subjects) {
      rect(cam.aim, { fill: "none", stroke: "#ffffff", "stroke-width": 2.5, "stroke-dasharray": "4 10", opacity: 0.9 }, c.svg);
      var cx = (cam.aim[0] + cam.aim[2]) / 2 * 1600, cy = (cam.aim[1] + cam.aim[3]) / 2 * 900;
      svg("path", { d: "M" + (cx - 14) + " " + cy + "H" + (cx + 14) + "M" + cx + " " + (cy - 14) + "V" + (cy + 14), stroke: "#fff", "stroke-width": 2.5, opacity: 0.9 }, c.svg);
    }
  }

  function picture(c, cam) {
    if (!cam.frame) { c.img.hidden = true; return; }
    c.img.hidden = false;
    if (cam.frame === c.seq || c.loading) return;
    c.loading = true;
    var want = cam.frame;
    // jadeck rewrites "res:frame/x.jpg" in state to its full route
    // (/api/connections/<id>/res/frame/x.jpg); the bridge wants the res path.
    K.res(String(want).replace(/^.*\/res\//, "")).then(function (blob) {
      var u = URL.createObjectURL(blob), old = c.url;
      c.img.onload = function () { if (old) URL.revokeObjectURL(old); };
      c.img.src = u;
      c.url = u;
      c.seq = want;
    }).catch(function () {}).finally(function () { c.loading = false; });
  }

  function videoText(cam) {
    var v = cam.video || "";
    if (v === "live") return "";
    if (v === "connecting" || v === "starting") return "Connecting to the camera…";
    return v ? v.charAt(0).toUpperCase() + v.slice(1) : "";
  }

  K.render = function (st) {
    var cams = st.cameras || [];
    var src = (st.detector || "") + (st.ms ? " · " + st.ms + " ms" : "");
    hdr.set({ status: st.error ? "warn" : (cams.length ? "ok" : ""), source: src, right: pauseBtn });
    hdr.note(st.error || "", !!st.error);
    pauseBtn.set({ label: st.paused ? "Resume" : "Pause all", active: !!st.paused });

    var seen = {};
    if (!cams.length) {
      main.textContent = "";
      cards = {};
      JK.el("div", "empty", main, st.available ? "Pick cameras in this widget's settings (" + st.available + " available)." : "No cameras yet: add one in jadeck, or under Other cameras in the Auto frame connection.");
      return;
    }
    var empty = main.querySelector(".empty");
    if (empty) empty.remove();
    cams.forEach(function (cam, i) {
      var c = cards[cam.id] || (cards[cam.id] = card(cam));
      seen[cam.id] = true;
      if (main.children[i] !== c.el) main.insertBefore(c.el, main.children[i] || null);
      c.name.textContent = cam.name;
      c.via.textContent = cam.via === "jadeck" ? "via jadeck video" : cam.via === "file" ? "recording" : "";
      c.badge.textContent = st.paused ? "Paused" : (st.shadow ? "Shadow" : "");
      c.badge.title = st.shadow ? "Shows what it would do; never moves the camera." : "";
      c.mode.set(cam.mode);
      c.shot.set(cam.shot);
      c.shot.disable(cam.mode === "hold");
      var vt = videoText(cam);
      c.msg.textContent = vt;
      c.msg.hidden = !vt && !!cam.frame;
      if (!vt && !cam.frame) c.msg.textContent = "Waiting for the first picture…";
      picture(c, cam);
      overlay(c, cam);
      c.move.className = "move" + (cam.lost ? " lost" : cam.holding ? "" : " go");
      c.moveText.textContent = cam.move ? (st.shadow && !cam.holding ? "Would " + cam.move : cam.move.charAt(0).toUpperCase() + cam.move.slice(1)) : "";
      var n = (cam.people || []).length;
      c.footL.textContent = n + (n === 1 ? " person" : " people") + " in view";
      c.footR.textContent = cam.target ? "tap them again to let go" : "";
    });
    Object.keys(cards).forEach(function (id) {
      if (!seen[id]) { cards[id].el.remove(); if (cards[id].url) URL.revokeObjectURL(cards[id].url); delete cards[id]; }
    });
  };
  K.start();
})();
