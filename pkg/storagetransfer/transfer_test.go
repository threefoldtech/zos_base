package storagetransfer

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// objectStore is a minimal in-memory stand-in for a presigned S3 endpoint:
// PUT stores the body under the request path, GET serves it back.
func newObjectStore(t *testing.T) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	store := map[string][]byte{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodPut:
			// presigned PUT must not be chunked
			if r.ContentLength < 0 {
				http.Error(w, "missing content-length", http.StatusNotImplemented)
				return
			}
			b, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			store[r.URL.Path] = b
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			b, ok := store[r.URL.Path]
			if !ok {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			_, _ = w.Write(b)
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFileRoundTrip(t *testing.T) {
	srv := newObjectStore(t)
	dir := t.TempDir()

	data := bytes.Repeat([]byte("zmount-bytes-0123456789"), 5000) // ~115KB
	src := filepath.Join(dir, "disk.raw")
	if err := os.WriteFile(src, data, 0600); err != nil {
		t.Fatal(err)
	}

	if err := UploadFile(src, srv.URL+"/disk"); err != nil {
		t.Fatalf("upload: %v", err)
	}

	// DownloadToFile writes into an existing file (as a provisioned vdisk would be)
	dst := filepath.Join(dir, "dst.raw")
	if err := os.WriteFile(dst, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := DownloadToFile(dst, srv.URL+"/disk"); err != nil {
		t.Fatalf("download: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("round-trip mismatch: got %d bytes, want %d", len(got), len(data))
	}
}

func TestDirRoundTrip(t *testing.T) {
	srv := newObjectStore(t)
	dir := t.TempDir()

	// mimic a rootfs subvolume: rw/ (upperdir with user changes), wd/ (workdir)
	src := filepath.Join(dir, "vol")
	mustMkdir(t, filepath.Join(src, "rw", "etc"))
	mustMkdir(t, filepath.Join(src, "wd"))
	mustWrite(t, filepath.Join(src, "rw", "etc", "hostname"), "node-b\n")
	mustWrite(t, filepath.Join(src, "rw", "data.bin"), "user-modified")
	if err := os.Symlink("data.bin", filepath.Join(src, "rw", "link")); err != nil {
		t.Fatal(err)
	}

	if err := UploadDir(src, srv.URL+"/vol"); err != nil {
		t.Fatalf("upload dir: %v", err)
	}

	dst := filepath.Join(dir, "voldst")
	mustMkdir(t, dst)
	if err := DownloadDir(dst, srv.URL+"/vol"); err != nil {
		t.Fatalf("download dir: %v", err)
	}

	if got := mustRead(t, filepath.Join(dst, "rw", "etc", "hostname")); got != "node-b\n" {
		t.Fatalf("hostname mismatch: %q", got)
	}
	if got := mustRead(t, filepath.Join(dst, "rw", "data.bin")); got != "user-modified" {
		t.Fatalf("data.bin mismatch: %q", got)
	}
	link, err := os.Readlink(filepath.Join(dst, "rw", "link"))
	if err != nil || link != "data.bin" {
		t.Fatalf("symlink not restored: link=%q err=%v", link, err)
	}
	if fi, err := os.Stat(filepath.Join(dst, "wd")); err != nil || !fi.IsDir() {
		t.Fatalf("workdir not restored: err=%v", err)
	}
}

func TestDownloadErrorStatus(t *testing.T) {
	srv := newObjectStore(t)
	dst := filepath.Join(t.TempDir(), "x.raw")
	if err := os.WriteFile(dst, nil, 0600); err != nil {
		t.Fatal(err)
	}
	// nothing was uploaded => GET returns 404 => must error
	if err := DownloadToFile(dst, srv.URL+"/missing"); err == nil {
		t.Fatal("expected error on 404 download")
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
