// Command runtimesums downloads every platform's ONNX Runtime library the
// add-on can fetch and prints its SHA-256, for lib/runtime.go's table.
// Run it after changing RuntimeVersion.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/toolingdev/jadeck-addon-autoframe/lib"
)

func main() {
	dir, err := os.MkdirTemp("", "ortsums")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	ps := lib.RuntimePlatforms()
	sort.Strings(ps)
	for _, p := range ps {
		dst := filepath.Join(dir, filepath.Base(p)+"-"+filepath.Dir(p))
		if err := lib.DownloadRuntimeFor(context.Background(), p, dst); err != nil {
			fmt.Printf("%-14s error: %v\n", p, err)
			continue
		}
		b, _ := os.ReadFile(dst)
		s := sha256.Sum256(b)
		fmt.Printf("%-14s %s %d\n", p, hex.EncodeToString(s[:]), len(b))
	}
}
