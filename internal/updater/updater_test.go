package updater

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCompareSemVer(t *testing.T) {
	if CompareSemVer("1.7.1", "v2.0.0") <= 0 {
		t.Fatal("expected v2.0.0 > 1.7.1")
	}
	if CompareSemVer("2.0.0", "v2.0.0") != 0 {
		t.Fatal("expected 2.0.0 == v2.0.0")
	}
	if CompareSemVer("2.1.0", "v2.0.9") >= 0 {
		t.Fatal("expected 2.1.0 > v2.0.9")
	}
}

func TestDownloadAndVerifyChunkedAndSHA(t *testing.T) {
	payload := append([]byte{'M', 'Z'}, bytes.Repeat([]byte{0x90}, 90000)...)
	sum := sha256.Sum256(payload)
	hexSHA := hex.EncodeToString(sum[:])

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag_name": "v2.0.1",
				"html_url": srv.URL + "/rel",
				"body":     "release",
				"assets": []map[string]any{
					{
						"name":                 "GravProxy.exe",
						"browser_download_url": srv.URL + "/bin/GravProxy.exe",
						"digest":               "sha256:" + hexSHA,
					},
				},
			})
			return
		}
		if r.URL.Path == "/bin/GravProxy.exe" {
			w.Header().Set("Content-Length", "90002")
			_, _ = w.Write(payload)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	info, err := checkLatestWithURLs(context.Background(), "qlfu/grav-proxy", "2.0.0", srv.URL+"/api", srv.URL+"/web", "")
	if err != nil || !info.IsNewer || !strings.EqualFold(info.ExpectedSHA256, hexSHA) {
		t.Fatalf("unexpected CheckLatest result: %+v err=%v", info, err)
	}

	var calls int32
	outPath, err := DownloadAndVerify(context.Background(), info, "", 1024, func(p DownloadProgress) {
		atomic.AddInt32(&calls, 1)
	})
	if err != nil {
		t.Fatalf("DownloadAndVerify failed: %v", err)
	}
	defer os.Remove(outPath)
	if atomic.LoadInt32(&calls) == 0 {
		t.Fatal("expected progress callback to fire at least once")
	}

	info.ExpectedSHA256 = strings.Repeat("0", 64)
	if _, err := DownloadAndVerify(context.Background(), info, "", 1024, nil); err == nil {
		t.Fatal("expected SHA-256 mismatch error")
	}
}
