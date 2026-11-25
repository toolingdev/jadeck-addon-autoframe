// Command inspect prints an ONNX model's inputs and outputs.
package main

import (
	"fmt"
	"os"

	ort "github.com/yalue/onnxruntime_go"
)

func main() {
	ort.SetSharedLibraryPath(os.Args[1])
	if err := ort.InitializeEnvironment(); err != nil {
		fmt.Println("init:", err)
		os.Exit(1)
	}
	in, out, err := ort.GetInputOutputInfo(os.Args[2])
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	for _, i := range in {
		fmt.Println("in ", i.String())
	}
	for _, o := range out {
		fmt.Println("out", o.String())
	}
}
