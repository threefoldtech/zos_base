// Package storagetransfer provides HTTP streaming of vdisk files and subvolumes
// to/from presigned S3 URLs. It is used to move a deployment's persistent bytes
// (zmount disks and VM rootfs layers) between nodes during a contract move.
// The helpers are shared by both the storage and storage_light modules.
package storagetransfer

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/pkg/errors"
	log "github.com/rs/zerolog/log"
)

// httpTimeout bounds a single upload/download. vdisks/rootfs images can be large,
// so this is generous; the caller's presigned URL usually expires sooner.
const httpTimeout = 6 * time.Hour

// UploadFile PUTs a seekable file so net/http sets a definite Content-Length
// (presigned S3/MinIO PUT rejects chunked transfer encoding).
func UploadFile(path, url string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return err
	}
	return httpPut(url, f, st.Size())
}

// DownloadToFile GETs url and writes the body into an existing file, truncating it.
func DownloadToFile(path, url string) error {
	body, err := httpGet(url)
	if err != nil {
		return err
	}
	defer body.Close()

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := io.Copy(f, body); err != nil {
		return err
	}
	return f.Sync()
}

// UploadDir streams a tar of `dir` to url. It uses two passes so it needs no
// temp file (nodes run from a small tmpfs): pass 1 tars to a byte counter to get
// the exact Content-Length (presigned PUT rejects chunked encoding), pass 2 tars
// straight into the request body via an io.Pipe. The source is a paused VM's
// rootfs, so it is static between the two passes.
func UploadDir(dir, url string) error {
	var counter countWriter
	if err := writeTar(&counter, dir); err != nil {
		return errors.Wrap(err, "failed to size volume tar")
	}

	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(writeTar(pw, dir))
	}()
	defer pr.Close()

	return httpPut(url, pr, counter.n)
}

// countWriter counts bytes written to it and discards them.
type countWriter struct{ n int64 }

func (c *countWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}

// DownloadDir GETs url and extracts the tar stream into `dir`.
func DownloadDir(dir, url string) error {
	body, err := httpGet(url)
	if err != nil {
		return err
	}
	defer body.Close()
	return extractTar(body, dir)
}

func httpPut(url string, body io.Reader, size int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), httpTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, body)
	if err != nil {
		return err
	}
	// explicit length => no chunked transfer-encoding, which presigned PUT rejects
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("upload failed with status %s: %s", resp.Status, string(b))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// httpGet returns the response body of a GET request. The returned ReadCloser
// owns the request context and cancels it on Close.
func httpGet(url string) (io.ReadCloser, error) {
	ctx, cancel := context.WithTimeout(context.Background(), httpTimeout)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("download failed with status %s: %s", resp.Status, string(b))
	}
	return &cancelReadCloser{ReadCloser: resp.Body, cancel: cancel}, nil
}

type cancelReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelReadCloser) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

// writeTar walks `dir` and writes every entry (relative to dir) to w. It preserves
// regular files, directories, symlinks and character devices. Character devices
// matter because an overlayfs upper dir represents deleted files as 0/0 whiteout
// char devices; losing them would resurrect deleted files on the target node.
func writeTar(w io.Writer, dir string) error {
	tw := tar.NewWriter(w)
	defer tw.Close()

	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		var link string
		if info.Mode()&os.ModeSymlink != 0 {
			if link, err = os.Readlink(path); err != nil {
				return err
			}
		}

		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = rel

		// preserve device numbers for char/block devices (e.g. overlay whiteouts)
		if st, ok := info.Sys().(*syscall.Stat_t); ok &&
			(info.Mode()&os.ModeCharDevice != 0 || info.Mode()&os.ModeDevice != 0) {
			hdr.Devmajor = int64(unixMajor(uint64(st.Rdev)))
			hdr.Devminor = int64(unixMinor(uint64(st.Rdev)))
		}

		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}

		if info.Mode().IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			if _, err := io.Copy(tw, f); err != nil {
				return err
			}
		}
		return nil
	})
}

// extractTar extracts a tar stream into `dir`, recreating files, directories,
// symlinks and character/block devices.
func extractTar(r io.Reader, dir string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		target := filepath.Join(dir, filepath.Clean("/"+hdr.Name))
		if err := extractEntry(tr, hdr, target); err != nil {
			return errors.Wrapf(err, "failed to extract '%s'", hdr.Name)
		}
	}
}

func extractEntry(tr *tar.Reader, hdr *tar.Header, target string) error {
	mode := hdr.FileInfo().Mode()
	switch hdr.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(target, mode.Perm())
	case tar.TypeReg:
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm())
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(f, tr)
		return err
	case tar.TypeSymlink:
		_ = os.Remove(target)
		return os.Symlink(hdr.Linkname, target)
	case tar.TypeLink:
		return os.Link(filepath.Join(filepath.Dir(target), hdr.Linkname), target)
	case tar.TypeChar, tar.TypeBlock:
		kind := uint32(syscall.S_IFCHR)
		if hdr.Typeflag == tar.TypeBlock {
			kind = syscall.S_IFBLK
		}
		dev := unixMkdev(uint32(hdr.Devmajor), uint32(hdr.Devminor))
		_ = os.Remove(target)
		return syscall.Mknod(target, kind|uint32(mode.Perm()), int(dev))
	default:
		// fifo/socket and anything else are not expected in a rootfs overlay; skip
		log.Debug().Str("name", hdr.Name).Uint8("type", hdr.Typeflag).Msg("skipping unsupported tar entry")
		return nil
	}
}

// Linux dev_t helpers (glibc encoding), avoiding a dependency on x/sys/unix.
func unixMajor(dev uint64) uint32 {
	return uint32((dev>>8)&0xfff) | uint32((dev>>32)&^uint64(0xfff))
}

func unixMinor(dev uint64) uint32 {
	return uint32(dev&0xff) | uint32((dev>>12)&^uint64(0xff))
}

func unixMkdev(major, minor uint32) uint64 {
	return (uint64(major&0xfff) << 8) |
		uint64(minor&0xff) |
		(uint64(minor&^uint32(0xff)) << 12)
}
