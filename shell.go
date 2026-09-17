package main

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// shellVersion pins the chrome-headless-shell build browse runs. One
// version for every laptop that installs this commit, so two people
// looking at one page see the same renderer. Bump it in its own commit
// and run TestBrowse on a laptop; the stable line is at
// https://googlechromelabs.github.io/chrome-for-testing/.
const shellVersion = "153.0.8010.47"

// shellPlatform is the Chrome for Testing name for this OS and CPU.
func shellPlatform() (string, error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		return "mac-arm64", nil
	case "darwin/amd64":
		return "mac-x64", nil
	case "linux/amd64":
		return "linux64", nil
	case "linux/arm64":
		return "linux-arm64", nil
	}
	return "", fmt.Errorf("no chrome-headless-shell for %s/%s; pass -chrome <path>", runtime.GOOS, runtime.GOARCH)
}

// shellDir is where the pinned build lives: the user cache directory,
// under browse, named for the platform and version so a bump does not
// overwrite what an older browse still runs.
func shellDir() (string, error) {
	platform, err := shellPlatform()
	if err != nil {
		return "", err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "browse", "chrome-headless-shell-"+platform+"-"+shellVersion), nil
}

// ensureShell returns the path of the pinned chrome-headless-shell,
// downloading it on the first run. The zip is about 100 MB; a line on
// stderr says so before the wait, and the extracted tree moves into
// place in one rename, so a run interrupted mid-download leaves no
// half-installed binary for the next run to find.
func ensureShell(ctx context.Context) (string, error) {
	dir, err := shellDir()
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "chrome-headless-shell")
	if _, err := os.Stat(bin); err == nil {
		return bin, nil
	}
	platform, _ := shellPlatform()
	url := fmt.Sprintf("https://storage.googleapis.com/chrome-for-testing-public/%s/%s/chrome-headless-shell-%s.zip",
		shellVersion, platform, platform)
	fmt.Fprintf(os.Stderr, "browse: downloading chrome-headless-shell %s (about 100 MB) to %s\n", shellVersion, dir)

	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), ".download-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	zipPath := filepath.Join(tmp, "shell.zip")
	if err := download(ctx, url, zipPath); err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	// The zip holds one top directory, chrome-headless-shell-<platform>.
	// Extract under tmp, then rename that directory to dir.
	if err := unzip(zipPath, tmp); err != nil {
		return "", fmt.Errorf("unzip %s: %w", url, err)
	}
	extracted := filepath.Join(tmp, "chrome-headless-shell-"+platform)
	if _, err := os.Stat(filepath.Join(extracted, "chrome-headless-shell")); err != nil {
		return "", fmt.Errorf("unzip %s: no chrome-headless-shell inside", url)
	}
	if err := os.Rename(extracted, dir); err != nil {
		return "", err
	}
	return bin, nil
}

func download(ctx context.Context, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New(resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// unzip extracts an archive under dst, keeping file modes so the binary
// stays executable. An entry whose path escapes dst is refused.
func unzip(src, dst string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		path := filepath.Join(dst, f.Name)
		if !strings.HasPrefix(path, filepath.Clean(dst)+string(os.PathSeparator)) {
			return fmt.Errorf("entry %q escapes the archive", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := extractFile(f, path); err != nil {
			return err
		}
	}
	return nil
}

func extractFile(f *zip.File, path string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	mode := f.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
