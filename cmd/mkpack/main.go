// Command mkpack builds an unsigned .ptzaddon from a staging folder
// (addon.json, bin/<goos>-<goarch>/addon, ui/). Release builds are signed
// by the release pipeline instead.
//
//	go run ./cmd/mkpack <staging dir> <out.ptzaddon>
package main

import (
	"fmt"
	"os"

	"github.com/toolingdev/jadeck/sdk/addon/pack"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: mkpack <staging dir> <out.ptzaddon>")
		os.Exit(2)
	}
	b, m, err := pack.Build(os.Args[1], nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mkpack:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(os.Args[2], b, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "mkpack:", err)
		os.Exit(1)
	}
	fmt.Printf("%s %s: %d bytes\n", m.ID, m.Version, len(b))
}
