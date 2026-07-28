package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateVerifiesAndReplacesExecutable(t *testing.T) {
	binary := []byte("new janus binary")
	sum := sha256.Sum256(binary)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0.1.3/checksums.txt":
			fmt.Fprintf(w, "%s  janus-linux-amd64\n", hex.EncodeToString(sum[:]))
		case "/v0.1.3/janus-linux-amd64":
			_, _ = w.Write(binary)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	executable := filepath.Join(dir, "janus")
	if err := os.WriteFile(executable, []byte("old janus binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := Update(context.Background(), Options{
		Version:    "v0.1.3",
		Executable: executable,
		BaseURL:    server.URL,
		Client:     server.Client(),
		GOOS:       "linux",
		GOARCH:     "amd64",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(binary) {
		t.Fatalf("updated executable = %q, want %q", got, binary)
	}
}

func TestUpdateRejectsChecksumMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v0.1.3/checksums.txt" {
			fmt.Fprintln(w, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  janus-linux-amd64")
			return
		}
		_, _ = w.Write([]byte("tampered"))
	}))
	defer server.Close()

	err := Update(context.Background(), Options{
		Version:    "0.1.3",
		Executable: filepath.Join(t.TempDir(), "janus"),
		BaseURL:    server.URL,
		Client:     server.Client(),
		GOOS:       "linux",
		GOARCH:     "amd64",
	})
	if err == nil || err.Error() != "checksum mismatch for janus-linux-amd64" {
		t.Fatalf("Update error = %v, want checksum mismatch", err)
	}
}
