package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

type ReleaseInfo struct {
	Tag            string `json:"tag"`
	Version        string `json:"version"`
	DownloadURL    string `json:"downloadUrl"`
	HTMLURL        string `json:"htmlUrl"`
	ExpectedSHA256 string `json:"expectedSha256"`
	Notes          string `json:"notes"`
	IsNewer        bool   `json:"isNewer"`
}

type DownloadProgress struct {
	DownloadedBytes int64  `json:"downloadedBytes"`
	TotalBytes      int64  `json:"totalBytes"`
	Percent         int    `json:"percent"`
	SpeedKBps       int    `json:"speedKbps"`
	Route           string `json:"route"`
}

var (
	shaNoteRe = regexp.MustCompile(`(?i)SHA-256[^\r\n]{0,80}?([0-9a-f]{64})`)
	tagPathRe = regexp.MustCompile(`(?i)/releases/tag/(v?\d+\.\d+\.\d+(?:[.-][a-z0-9.]+)?)`)
)

func CompareSemVer(current, remote string) int {
	cParts, cOK := parseSemVer(current)
	rParts, rOK := parseSemVer(remote)
	if !cOK || !rOK {
		return 0
	}
	for i := 0; i < 4; i++ {
		if rParts[i] > cParts[i] {
			return 1
		}
		if rParts[i] < cParts[i] {
			return -1
		}
	}
	return 0
}

func parseSemVer(raw string) ([4]int, bool) {
	var out [4]int
	s := strings.TrimSpace(raw)
	s = strings.TrimLeft(s, "vV")
	if idx := strings.IndexAny(s, "-+"); idx >= 0 {
		s = s[:idx]
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 || len(parts) > 4 {
		return out, false
	}
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 {
			return out, false
		}
		out[i] = v
	}
	return out, true
}

func CheckLatest(ctx context.Context, repo, currentVersion, localProxyURL string) (*ReleaseInfo, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	webURL := fmt.Sprintf("https://github.com/%s/releases/latest", repo)
	info, err := checkLatestWithURLs(ctx, repo, currentVersion, apiURL, webURL, localProxyURL)
	if err != nil {
		return nil, err
	}
	dlPrefix := fmt.Sprintf("https://github.com/%s/releases/download/", repo)
	if !strings.HasPrefix(strings.ToLower(info.DownloadURL), strings.ToLower(dlPrefix)) {
		return nil, fmt.Errorf("unexpected release asset URL: %s", info.DownloadURL)
	}
	return info, nil
}

func checkLatestWithURLs(ctx context.Context, repo, currentVersion, apiURL, webURL, localProxyURL string) (*ReleaseInfo, error) {
	info := &ReleaseInfo{}
	var lastErr string

	fetch := func(targetURL string, followRedirect, useProxy bool) (int, string, []byte, error) {
		tr := &http.Transport{}
		if useProxy && localProxyURL != "" {
			if pu, err := url.Parse(localProxyURL); err == nil {
				tr.Proxy = http.ProxyURL(pu)
			}
		}
		client := &http.Client{Transport: tr, Timeout: 8 * time.Second}
		if !followRedirect {
			client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
		if err != nil {
			return 0, "", nil, err
		}
		req.Header.Set("User-Agent", "GravProxy/"+currentVersion)
		req.Header.Set("Accept", "application/vnd.github+json")
		resp, err := client.Do(req)
		if err != nil {
			return 0, "", nil, err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
		return resp.StatusCode, resp.Header.Get("Location"), body, nil
	}

	proxyUp := isProxyOpen(localProxyURL)
	code, _, body, err := fetch(apiURL, true, false)
	if (err != nil || code != http.StatusOK) && proxyUp {
		code, _, body, err = fetch(apiURL, true, true)
	}
	if err != nil {
		lastErr = err.Error()
	}

	if code == http.StatusOK && len(body) > 0 {
		var parsed struct {
			TagName string `json:"tag_name"`
			HTMLURL string `json:"html_url"`
			Body    string `json:"body"`
			Assets  []struct {
				Name               string `json:"name"`
				BrowserDownloadURL string `json:"browser_download_url"`
				Digest             string `json:"digest"`
			} `json:"assets"`
		}
		if json.Unmarshal(body, &parsed) == nil {
			info.Tag = strings.TrimSpace(parsed.TagName)
			info.HTMLURL = strings.TrimSpace(parsed.HTMLURL)
			info.Notes = strings.TrimSpace(parsed.Body)
			for _, a := range parsed.Assets {
				if strings.EqualFold(strings.TrimSpace(a.Name), "GravProxy.exe") {
					info.DownloadURL = strings.TrimSpace(a.BrowserDownloadURL)
					if strings.HasPrefix(strings.ToLower(a.Digest), "sha256:") && len(a.Digest) == 71 {
						info.ExpectedSHA256 = strings.ToLower(a.Digest[7:])
					}
					break
				}
			}
			if info.ExpectedSHA256 == "" && info.Notes != "" {
				if m := shaNoteRe.FindStringSubmatch(info.Notes); len(m) > 1 {
					info.ExpectedSHA256 = strings.ToLower(m[1])
				}
			}
		}
	}

	if info.Tag == "" || info.DownloadURL == "" {
		wCode, loc, _, wErr := fetch(webURL, false, false)
		if (wErr != nil || (loc == "" && wCode == 0)) && proxyUp {
			_, loc, _, wErr = fetch(webURL, false, true)
		}
		if wErr != nil && lastErr == "" {
			lastErr = wErr.Error()
		}
		if m := tagPathRe.FindStringSubmatch(loc); len(m) > 1 {
			info.Tag = m[1]
			info.HTMLURL = fmt.Sprintf("https://github.com/%s/releases/tag/%s", repo, info.Tag)
			info.DownloadURL = fmt.Sprintf("https://github.com/%s/releases/download/%s/GravProxy.exe", repo, info.Tag)
		}
	}

	if info.Tag == "" || info.DownloadURL == "" {
		if lastErr == "" {
			lastErr = "не удалось получить информацию о релизе GitHub"
		}
		return nil, fmt.Errorf("%s", lastErr)
	}
	info.Version = strings.TrimLeft(info.Tag, "vV")
	if info.HTMLURL == "" {
		info.HTMLURL = fmt.Sprintf("https://github.com/%s/releases/tag/%s", repo, info.Tag)
	}
	info.IsNewer = CompareSemVer(currentVersion, info.Tag) > 0
	return info, nil
}

func CleanupStalePartFiles() {
	tmp := os.TempDir()
	matches, err := filepath.Glob(filepath.Join(tmp, "GravProxy-update-*.exe.part"))
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-10 * time.Minute)
	for _, p := range matches {
		if st, err := os.Stat(p); err == nil && st.ModTime().Before(cutoff) {
			_ = os.Remove(p)
		}
	}
}

func DownloadAndVerify(ctx context.Context, info *ReleaseInfo, localProxyURL string, minSizeBytes int64, progressFn func(DownloadProgress)) (string, error) {
	if info == nil || strings.TrimSpace(info.DownloadURL) == "" {
		return "", fmt.Errorf("empty download URL")
	}
	if minSizeBytes <= 0 {
		minSizeBytes = 1048576
	}
	CleanupStalePartFiles()

	safeVer := regexp.MustCompile(`[^0-9A-Za-z._-]`).ReplaceAllString(info.Version, "")
	if safeVer == "" {
		safeVer = "latest"
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano()&0xffffff)
	partPath := filepath.Join(os.TempDir(), fmt.Sprintf("GravProxy-update-%s-%s.exe.part", safeVer, suffix))
	finalPath := filepath.Join(os.TempDir(), fmt.Sprintf("GravProxy-update-%s-%s.exe", safeVer, suffix))

	proxyOpen := isProxyOpen(localProxyURL)
	err := downloadChunked(ctx, info.DownloadURL, partPath, "", proxyOpen, progressFn)
	if err != nil {
		_ = os.Remove(partPath)
		if !proxyOpen {
			proxyOpen = isProxyOpen(localProxyURL)
		}
		if !proxyOpen {
			return "", err
		}
		err = downloadChunked(ctx, info.DownloadURL, partPath, localProxyURL, false, progressFn)
		if err != nil {
			_ = os.Remove(partPath)
			return "", err
		}
	}

	st, err := os.Stat(partPath)
	if err != nil || st.Size() < minSizeBytes {
		_ = os.Remove(partPath)
		return "", fmt.Errorf("скачанный файл повреждён или слишком мал (%d байт)", st.Size())
	}

	f, err := os.Open(partPath)
	if err != nil {
		_ = os.Remove(partPath)
		return "", err
	}
	hdr := make([]byte, 2)
	_, readErr := io.ReadFull(f, hdr)
	if readErr != nil || hdr[0] != 'M' || hdr[1] != 'Z' {
		_ = f.Close()
		_ = os.Remove(partPath)
		return "", fmt.Errorf("скачанный файл не является PE/EXE")
	}
	_, _ = f.Seek(0, io.SeekStart)
	h := sha256.New()
	_, hashErr := io.Copy(h, f)
	_ = f.Close()
	if hashErr != nil {
		_ = os.Remove(partPath)
		return "", hashErr
	}
	actualSHA := hex.EncodeToString(h.Sum(nil))
	if info.ExpectedSHA256 != "" && !strings.EqualFold(actualSHA, strings.TrimSpace(info.ExpectedSHA256)) {
		_ = os.Remove(partPath)
		return "", fmt.Errorf("несовпадение SHA-256 (%s)", actualSHA)
	}

	if err := os.Rename(partPath, finalPath); err != nil {
		_ = os.Remove(partPath)
		return "", err
	}
	return finalPath, nil
}

func downloadChunked(ctx context.Context, rawURL, destPath, proxyURL string, allowSlowSwitch bool, progressFn func(DownloadProgress)) error {
	routeName := "direct"
	timeout := 12 * time.Second
	tr := &http.Transport{ResponseHeaderTimeout: timeout}
	if proxyURL != "" {
		routeName = "proxy"
		timeout = 25 * time.Second
		tr.ResponseHeaderTimeout = timeout
		if pu, err := url.Parse(proxyURL); err == nil {
			tr.Proxy = http.ProxyURL(pu)
		}
	}
	client := &http.Client{Transport: tr}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "GravProxy-Updater")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	total := resp.ContentLength
	if total < 0 {
		total = 0
	}
	buf := make([]byte, 64*1024)
	start := time.Now()
	var lastEmit time.Time
	var readTotal int64

	for {
		n, rErr := resp.Body.Read(buf)
		if n > 0 {
			if _, wErr := out.Write(buf[:n]); wErr != nil {
				return wErr
			}
			readTotal += int64(n)
			elapsed := time.Since(start)
			if allowSlowSwitch && elapsed >= 5*time.Second {
				kbps := (float64(readTotal) / 1024.0) / elapsed.Seconds()
				if kbps < 40.0 && (total == 0 || readTotal < total) {
					return fmt.Errorf("слишком низкая скорость напрямую (%d КБ/с)", int(kbps))
				}
			}
			if progressFn != nil && (lastEmit.IsZero() || time.Since(lastEmit) >= 200*time.Millisecond || rErr == io.EOF) {
				lastEmit = time.Now()
				sec := elapsed.Seconds()
				if sec < 0.1 {
					sec = 0.1
				}
				pct := 0
				if total > 0 {
					pct = int((readTotal * 100) / total)
					if pct > 100 {
						pct = 100
					}
				}
				progressFn(DownloadProgress{
					DownloadedBytes: readTotal,
					TotalBytes:      total,
					Percent:         pct,
					SpeedKBps:       int((float64(readTotal) / 1024.0) / sec),
					Route:           routeName,
				})
			}
		}
		if rErr == io.EOF {
			break
		}
		if rErr != nil {
			return rErr
		}
	}
	return out.Sync()
}

func ApplyUpdateAndRestart(downloadedExePath string, forwardArgs ...string) error {
	selfExe, err := os.Executable()
	if err != nil {
		return err
	}
	selfPtr, _ := windows.UTF16PtrFromString(selfExe)
	oldPath := fmt.Sprintf("%s.old.%d", selfExe, os.Getpid())
	_ = os.Remove(oldPath)
	oldPtr, _ := windows.UTF16PtrFromString(oldPath)
	newPtr, _ := windows.UTF16PtrFromString(downloadedExePath)

	if err := windows.MoveFileEx(selfPtr, oldPtr, windows.MOVEFILE_REPLACE_EXISTING); err != nil {
		// Cross-volume or locked fallback: just launch the downloaded installer directly.
		cmd := exec.Command(downloadedExePath, forwardArgs...)
		return cmd.Start()
	}
	if err := windows.MoveFileEx(newPtr, selfPtr, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_COPY_ALLOWED|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		_ = windows.MoveFileEx(oldPtr, selfPtr, windows.MOVEFILE_REPLACE_EXISTING)
		return err
	}
	cmd := exec.Command(selfExe, forwardArgs...)
	return cmd.Start()
}

func isProxyOpen(proxyURL string) bool {
	if strings.TrimSpace(proxyURL) == "" {
		return false
	}
	u, err := url.Parse(proxyURL)
	if err != nil || u.Host == "" {
		return false
	}
	c, err := net.DialTimeout("tcp", u.Host, 150*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}
