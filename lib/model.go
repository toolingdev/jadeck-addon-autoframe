package lib

// The built-in model (RTMO-s, Apache-2.0, from OpenMMLab) is 40 MB, too big
// for an add-on package, so the add-on downloads it the first time it runs
// and keeps it in the per-user config folder, where add-on updates don't
// touch it. Both the download and the model inside it are checked against
// known SHA-256 sums.

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const (
	modelURL     = "https://download.openmmlab.com/mmpose/v1/projects/rtmo/onnx_sdk/rtmo-s_8xb32-600e_body7-640x640-dac2bf74_20231211.zip"
	modelZipSHA  = "3da7ad88b209f9da8be87ba5c325610639af04de3b5c8a96649b98cd9e2848a2"
	modelOnnxSHA = "d0703d40d19f3921da51ae725402d5fdae4d2478c7442072d3101bd396f370d8"
	modelZipSize = 36442532
)

// modelHome is where a downloaded model lives.
func modelHome() string {
	cfg, err := os.UserConfigDir()
	if err != nil {
		cfg = os.TempDir()
	}
	return filepath.Join(cfg, "jadeck-addon-autoframe", "models", "rtmo-s.onnx")
}

// BuiltinModelReady says whether the built-in model is already on disk.
func BuiltinModelReady() bool {
	_, err := os.Stat(builtinModel())
	return err == nil
}

// DownloadBuiltinModel fetches the built-in model into modelHome.
// progress gets the bytes so far and the total.
func DownloadBuiltinModel(ctx context.Context, progress func(done, total int64)) error {
	dst := modelHome()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), "rtmo-s-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("couldn't download the people finder: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("couldn't download the people finder: %s", resp.Status)
	}
	total := resp.ContentLength
	if total <= 0 {
		total = modelZipSize
	}
	h := sha256.New()
	pr := &progressReader{r: resp.Body, total: total, fn: progress}
	if _, err := io.Copy(io.MultiWriter(tmp, h), pr); err != nil {
		return fmt.Errorf("download stopped: %w", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != modelZipSHA {
		return errors.New("the downloaded model didn't match its checksum; try again")
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	return extractModel(tmp.Name(), dst)
}

func extractModel(zipPath, dst string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if filepath.Base(f.Name) != "end2end.onnx" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		out, err := os.CreateTemp(filepath.Dir(dst), "rtmo-s-*.onnx")
		if err != nil {
			return err
		}
		defer os.Remove(out.Name())
		h := sha256.New()
		if _, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(rc, 200<<20)); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		if got := hex.EncodeToString(h.Sum(nil)); got != modelOnnxSHA {
			return errors.New("the model inside the download didn't match its checksum")
		}
		return os.Rename(out.Name(), dst)
	}
	return errors.New("the download had no end2end.onnx in it")
}

type progressReader struct {
	r     io.Reader
	done  int64
	total int64
	fn    func(done, total int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	if p.fn != nil && n > 0 {
		p.fn(p.done, p.total)
	}
	return n, err
}
