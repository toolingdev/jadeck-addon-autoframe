// Command mkpack builds the .ptzaddon from a folder holding addon.json,
// bin/<goos>-<goarch>/addon[.exe] (one per platform), README.md,
// CHANGELOG.md and ui/.
//
//	go run ./cmd/mkpack [--key KEY] <dir> <out.ptzaddon>
//
// KEY is a base64 ed25519 seed or a file holding one (default: the
// PTZ_SIGNING_KEY environment variable; none = unsigned). The standard
// `ptz-addon build` can't be used: it cross-compiles with cgo off, and
// the ONNX Runtime binding needs cgo, so CI builds each platform on its
// own runner and packs the programs here.
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/toolingdev/jadeck/sdk/addon/pack"
)

func die(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "mkpack: "+f+"\n", a...)
	os.Exit(1)
}

func main() {
	key := flag.String("key", os.Getenv("PTZ_SIGNING_KEY"), "signing key: base64 seed or a file holding one")
	flag.Parse()
	if flag.NArg() != 2 {
		die("usage: mkpack [--key KEY] <dir> <out.ptzaddon>")
	}
	var priv ed25519.PrivateKey
	if *key != "" {
		s := *key
		if b, err := os.ReadFile(s); err == nil {
			s = string(b)
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
		if err != nil || len(raw) != ed25519.SeedSize {
			die("the signing key must be a %d-byte base64 seed", ed25519.SeedSize)
		}
		priv = ed25519.NewKeyFromSeed(raw)
	}
	b, m, err := pack.Build(flag.Arg(0), priv)
	if err != nil {
		die("%v", err)
	}
	if err := os.WriteFile(flag.Arg(1), b, 0o644); err != nil {
		die("%v", err)
	}
	signed := "unsigned"
	if priv != nil {
		signed = "signed"
	}
	fmt.Printf("%s %s: %d bytes, %s\n", m.ID, m.Version, len(b), signed)
}
