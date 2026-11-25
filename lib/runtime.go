package lib

// ONNX Runtime is a native library the add-on package can't carry, so the
// add-on fetches it the first time it runs, like the model. The source is
// Microsoft's official NuGet package, which holds every platform's library
// (184 MB). Only this platform's library is read out of it, with HTTP range
// requests, so the download is about 20 MB. The file is checked against a
// known SHA-256 and kept in the per-user config folder.
//
// The version must match the C API headers of github.com/yalue/onnxruntime_go
// (1.38.0 uses 1.31; newer libraries accept older API versions).

import (
	"archive/zip"
	"compress/flate"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
)

const (
	RuntimeVersion = "1.32.0"
	runtimeURL     = "https://api.nuget.org/v3-flatcontainer/microsoft.ml.onnxruntime/" + RuntimeVersion +
		"/microsoft.ml.onnxruntime." + RuntimeVersion + ".nupkg"
)

// runtimeFile is the library inside the package for one platform.
type runtimeFile struct {
	entry string // path in the .nupkg
	name  string // file name on disk
	sha   string // SHA-256 of the file
}

var runtimeFiles = map[string]runtimeFile{
	"windows/amd64": {"runtimes/win-x64/native/onnxruntime.dll", "onnxruntime.dll", "348c3d29d6995eee37e3451825eaddb7ea7400be3afda6ca2286b12bc775f042"},
	"windows/arm64": {"runtimes/win-arm64/native/onnxruntime.dll", "onnxruntime.dll", "d9db6113549a3bd1eae55dbcb96bd95ee1f35c2823f39a42972664abab6517d2"},
	"darwin/arm64":  {"runtimes/osx-arm64/native/libonnxruntime.dylib", "libonnxruntime.dylib", "21ff304871400e846a30c8a1165d15173add56598afb45f98227e2dfc716a497"},
	"linux/amd64":   {"runtimes/linux-x64/native/libonnxruntime.so", "libonnxruntime.so", "4213d6d8c42ac4754865849cb139d31fc39127db6a326babf90bbc81a6eb52fc"},
	"linux/arm64":   {"runtimes/linux-arm64/native/libonnxruntime.so", "libonnxruntime.so", "fd1db07c49cb1ad11370ef4393a405eef959fa9622fb5b2880f85d025757e719"},
	// Intel Macs: not in the package; brew install onnxruntime.
}

func platformKey() string { return runtime.GOOS + "/" + runtime.GOARCH }

// runtimeHome is where a downloaded library lives.
func runtimeHome() string {
	f, ok := runtimeFiles[platformKey()]
	name := f.name
	if !ok {
		name = "onnxruntime"
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		cfg = os.TempDir()
	}
	return filepath.Join(cfg, "jadeck-addon-autoframe", "onnxruntime-"+RuntimeVersion, name)
}

// RuntimeReady says whether ONNX Runtime can be found without downloading.
func RuntimeReady(given string) bool {
	_, err := runtimeLibrary(given)
	return err == nil
}

// DownloadRuntime fetches this platform's ONNX Runtime into runtimeHome.
func DownloadRuntime(ctx context.Context, progress func(done, total int64)) error {
	f, ok := runtimeFiles[platformKey()]
	if !ok {
		return fmt.Errorf("no ONNX Runtime download for %s: install it and set ONNXRUNTIME_LIB", platformKey())
	}
	return downloadRuntime(ctx, runtimeURL, f, runtimeHome(), progress)
}

func downloadRuntime(ctx context.Context, url string, f runtimeFile, dst string, progress func(done, total int64)) error {
	ra, size, err := openRemote(ctx, url)
	if err != nil {
		return fmt.Errorf("couldn't reach the ONNX Runtime download: %w", err)
	}
	zr, err := zip.NewReader(ra, size)
	if err != nil {
		return fmt.Errorf("ONNX Runtime download: %w", err)
	}
	var zf *zip.File
	for _, x := range zr.File {
		if x.Name == f.entry {
			zf = x
		}
	}
	if zf == nil {
		return fmt.Errorf("ONNX Runtime download has no %s", f.entry)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	// One streamed range request for the library's bytes (not zip's small
	// reads through ReaderAt).
	off, err := zf.DataOffset()
	if err != nil {
		return err
	}
	body, err := ra.stream(off, int64(zf.CompressedSize64))
	if err != nil {
		return err
	}
	defer body.Close()
	var rc io.Reader = body
	switch zf.Method {
	case zip.Store:
	case zip.Deflate:
		fr := flate.NewReader(body)
		defer fr.Close()
		rc = fr
	default:
		return fmt.Errorf("ONNX Runtime download: unsupported compression %d", zf.Method)
	}
	out, err := os.CreateTemp(filepath.Dir(dst), "ort-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	h := sha256.New()
	pr := &progressReader{r: rc, total: int64(zf.UncompressedSize64), fn: progress}
	if _, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(pr, 500<<20)); err != nil {
		out.Close()
		return fmt.Errorf("download stopped: %w", err)
	}
	if err := out.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); f.sha != "-" && got != f.sha {
		return errors.New("the ONNX Runtime download didn't match its checksum; try again")
	}
	if err := os.Chmod(out.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(out.Name(), dst)
}

// remote is an io.ReaderAt over HTTP range requests, read in 1 MB blocks
// so zip's many small reads of the directory cost a request or two.
type remote struct {
	ctx    context.Context
	url    string
	size   int64
	blocks map[int64][]byte
	reads  int
}

const blockSize = 1 << 20

func openRemote(ctx context.Context, url string) (*remote, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, errors.New(resp.Status)
	}
	if resp.Header.Get("Accept-Ranges") != "bytes" || resp.ContentLength <= 0 {
		return nil, 0, errors.New("the server doesn't allow partial downloads")
	}
	return &remote{ctx: ctx, url: resp.Request.URL.String(), size: resp.ContentLength, blocks: map[int64][]byte{}}, resp.ContentLength, nil
}

// stream returns the bytes [off, off+n) as one response body.
func (r *remote) stream(off, n int64) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Range", "bytes="+strconv.FormatInt(off, 10)+"-"+strconv.FormatInt(off+n-1, 10))
	r.reads++
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusPartialContent {
		resp.Body.Close()
		return nil, fmt.Errorf("range request: %s", resp.Status)
	}
	return struct {
		io.Reader
		io.Closer
	}{io.LimitReader(resp.Body, n), resp.Body}, nil
}

func (r *remote) block(i int64) ([]byte, error) {
	if b, ok := r.blocks[i]; ok {
		return b, nil
	}
	off := i * blockSize
	n := min(int64(blockSize), r.size-off)
	body, err := r.stream(off, n)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	b := make([]byte, n)
	if _, err := io.ReadFull(body, b); err != nil {
		return nil, err
	}
	r.blocks[i] = b
	return b, nil
}

func (r *remote) ReadAt(p []byte, off int64) (int, error) {
	n := 0
	for n < len(p) {
		at := off + int64(n)
		if at >= r.size {
			return n, io.EOF
		}
		b, err := r.block(at / blockSize)
		if err != nil {
			return n, err
		}
		n += copy(p[n:], b[at%blockSize:])
	}
	return n, nil
}

// DownloadRuntimeFor fetches one platform's library to dst without the
// checksum check: it's how cmd/runtimesums makes the sums above.
func DownloadRuntimeFor(ctx context.Context, platform, dst string) error {
	f, ok := runtimeFiles[platform]
	if !ok {
		return fmt.Errorf("unknown platform %s", platform)
	}
	f.sha = "-" // no check
	return downloadRuntime(ctx, runtimeURL, f, dst, nil)
}

// RuntimePlatforms lists the platforms with a download.
func RuntimePlatforms() []string {
	var out []string
	for k := range runtimeFiles {
		out = append(out, k)
	}
	return out
}
