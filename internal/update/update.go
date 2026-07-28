package update

import (
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
	"strings"
)

const repository = "theaiinc/janus"

// Options controls a self-update operation.
type Options struct {
	Version    string
	Executable string
	BaseURL    string
	Client     *http.Client
	GOOS       string
	GOARCH     string
}

// Update downloads and verifies a release, then replaces the executable.
func Update(ctx context.Context, options Options) error {
	version := strings.TrimPrefix(strings.TrimSpace(options.Version), "v")
	if version == "" {
		return errors.New("release version is required (for example, --version 0.1.3)")
	}
	executable := options.Executable
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return fmt.Errorf("resolve current executable: %w", err)
		}
	}
	goos, goarch := options.GOOS, options.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	asset, err := assetName(goos, goarch)
	if err != nil {
		return err
	}
	baseURL := strings.TrimRight(options.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://github.com/" + repository + "/releases/download"
	}
	client := options.Client
	if client == nil {
		client = http.DefaultClient
	}

	checksumsURL := fmt.Sprintf("%s/v%s/checksums.txt", baseURL, version)
	checksums, err := fetch(ctx, client, checksumsURL)
	if err != nil {
		return fmt.Errorf("download release checksums: %w", err)
	}
	expected, err := checksumFor(checksums, asset)
	if err != nil {
		return err
	}

	binaryURL := fmt.Sprintf("%s/v%s/%s", baseURL, version, asset)
	binary, err := fetch(ctx, client, binaryURL)
	if err != nil {
		return fmt.Errorf("download release binary: %w", err)
	}
	actual := sha256.Sum256(binary)
	if !strings.EqualFold(expected, hex.EncodeToString(actual[:])) {
		return fmt.Errorf("checksum mismatch for %s", asset)
	}

	if runtime.GOOS == "windows" {
		return errors.New("self-update cannot replace a running executable on Windows; download the release and restart Janus")
	}
	mode := os.FileMode(0o755)
	if info, statErr := os.Stat(executable); statErr == nil {
		mode = info.Mode()
	}
	temp, err := os.CreateTemp(filepath.Dir(executable), ".janus-update-*")
	if err != nil {
		return fmt.Errorf("create update file: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return fmt.Errorf("set update permissions: %w", err)
	}
	if _, err := temp.Write(binary); err != nil {
		temp.Close()
		return fmt.Errorf("write update file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close update file: %w", err)
	}
	if err := os.Rename(tempName, executable); err != nil {
		return fmt.Errorf("replace executable: %w", err)
	}
	return nil
}

func assetName(goos, goarch string) (string, error) {
	var osName string
	switch goos {
	case "linux", "darwin":
		osName = goos
	case "windows":
		osName = "windows"
	default:
		return "", fmt.Errorf("unsupported update platform: %s/%s", goos, goarch)
	}
	var archName string
	switch goarch {
	case "amd64", "arm64":
		archName = goarch
	default:
		return "", fmt.Errorf("unsupported update platform: %s/%s", goos, goarch)
	}
	extension := ""
	if osName == "windows" {
		extension = ".exe"
	}
	return "janus-" + osName + "-" + archName + extension, nil
}

func fetch(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %s", response.Status)
	}
	return io.ReadAll(response.Body)
}

func checksumFor(contents []byte, asset string) (string, error) {
	for _, line := range strings.Split(string(contents), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimLeft(fields[1], "*") == asset {
			if len(fields[0]) != sha256.Size*2 {
				return "", fmt.Errorf("invalid checksum for %s", asset)
			}
			if _, err := hex.DecodeString(fields[0]); err != nil {
				return "", fmt.Errorf("invalid checksum for %s", asset)
			}
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("release checksums do not include %s", asset)
}
