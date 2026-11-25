package lib

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A package with a library in it, served with range support: the library
// comes out whole, checked, and in few requests.
func TestDownloadRuntime(t *testing.T) {
	lib := bytes.Repeat([]byte("onnxruntime "), 300000) // ~3.6 MB
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"runtimes/other/native/x.dll", "runtimes/test/native/libort.so"} {
		w, _ := zw.Create(name)
		w.Write(lib)
	}
	zw.Close()
	data := buf.Bytes()
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.ServeContent(w, r, "p.nupkg", time.Time{}, bytes.NewReader(data))
	}))
	defer srv.Close()

	sum := sha256.Sum256(lib)
	f := runtimeFile{entry: "runtimes/test/native/libort.so", name: "libort.so", sha: hex.EncodeToString(sum[:])}
	dst := filepath.Join(t.TempDir(), "libort.so")
	if err := downloadRuntime(context.Background(), srv.URL, f, dst, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	if !bytes.Equal(got, lib) {
		t.Fatalf("library differs: %d bytes", len(got))
	}
	if requests > 5 {
		t.Fatalf("%d requests: the directory should be read in blocks", requests)
	}

	f.sha = "00"
	if err := downloadRuntime(context.Background(), srv.URL, f, filepath.Join(t.TempDir(), "bad"), nil); err == nil {
		t.Fatal("a wrong checksum should fail")
	}
}
