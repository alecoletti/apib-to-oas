// Package drafter wraps the upstream Drafter binary
// (https://github.com/apiaryio/drafter), which parses API Blueprint
// (MSON included) and emits a Refract / API Elements JSON AST.
//
// Binaries for each (GOOS, GOARCH) live under bin/ and are baked into
// the apib-to-oas binary with go:embed. On first use the right one is copied
// to a per-user cache directory and run via os/exec.
//
// This is the only package in apib-to-oas that shells out. Nothing else
// should call Drafter directly.
package drafter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
)

//go:embed all:bin
var binaries embed.FS

// ErrUnsupportedPlatform is returned when no embedded Drafter binary exists
// for the current GOOS/GOARCH combination.
var ErrUnsupportedPlatform = errors.New("no embedded drafter binary for this platform")

// Runner executes the embedded drafter binary.
type Runner struct {
	binPath string
}

var (
	once     sync.Once
	cached   *Runner
	errCache error
)

// New returns a Runner backed by the embedded drafter binary for the
// current platform. The binary is extracted on first call and cached
// for the rest of the process lifetime.
func New() (*Runner, error) {
	once.Do(func() {
		path, err := materialize()
		if err != nil {
			errCache = err
			return
		}
		cached = &Runner{binPath: path}
	})
	return cached, errCache
}

// Parse runs drafter against src (API Blueprint source) and returns the
// resulting Refract / API Elements JSON document.
func (r *Runner) Parse(ctx context.Context, src []byte) ([]byte, error) {
	if r == nil {
		return nil, errors.New("drafter runner not initialised")
	}
	// drafter -f json - (read from stdin, JSON output).
	cmd := exec.CommandContext(ctx, r.binPath, "-f", "json", "-")
	cmd.Stdin = bytes.NewReader(src)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("drafter exec: %w (stderr: %s)", err, stderr.String())
	}
	return stdout.Bytes(), nil
}

// BinaryPath returns the resolved path of the extracted binary,
// useful for diagnostics.
func (r *Runner) BinaryPath() string { return r.binPath }

// materialise copies the embedded binary for the current platform
// into the user cache dir, marks it executable, and returns the path.
func materialize() (string, error) {
	name := fmt.Sprintf("drafter-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}

	data, err := binaries.ReadFile("bin/" + name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%w: %s/%s", ErrUnsupportedPlatform, runtime.GOOS, runtime.GOARCH)
		}
		return "", fmt.Errorf("read embedded binary: %w", err)
	}

	sum := sha256.Sum256(data)
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		cacheDir = os.TempDir()
	}
	dir := filepath.Join(cacheDir, "apib-to-oas", "drafter")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create cache dir: %w", err)
	}
	dest := filepath.Join(dir, fmt.Sprintf("%s-%s", name, hex.EncodeToString(sum[:8])))

	if _, err := os.Stat(dest); err == nil {
		return dest, nil
	}

	tmp, err := os.CreateTemp(dir, "drafter-*")
	if err != nil {
		return "", fmt.Errorf("temp file: %w", err)
	}
	if _, err := io.Copy(tmp, bytes.NewReader(data)); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return "", fmt.Errorf("install binary: %w", err)
	}
	return dest, nil
}
