package storage

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// newTestDisk creates a preallocated disk file like DiskCreate does,
// optionally tagged as new.
func newTestDisk(t *testing.T, size int64, tagged bool) string {
	path := filepath.Join(t.TempDir(), "disk")
	file, err := os.Create(path)
	require.NoError(t, err)
	defer file.Close()
	require.NoError(t, syscall.Fallocate(int(file.Fd()), 0, 0, size))

	if tagged {
		if err := syscall.Setxattr(path, newDiskXattr, []byte("1"), 0); err == syscall.ENOTSUP {
			t.Skip("filesystem does not support user xattrs")
		} else {
			require.NoError(t, err)
		}
	}
	return path
}

func writeAt(t *testing.T, path string, offset int64) {
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	require.NoError(t, err)
	defer file.Close()
	_, err = file.WriteAt([]byte("data"), offset)
	require.NoError(t, err)
}

func hasTag(t *testing.T, path string) bool {
	_, err := syscall.Getxattr(path, newDiskXattr, nil)
	if err == syscall.ENODATA {
		return false
	}
	require.NoError(t, err)
	return true
}

func TestIsNewDisk(t *testing.T) {
	const size = 16 * 1024 * 1024

	t.Run("tagged and zero", func(t *testing.T) {
		path := newTestDisk(t, size, true)
		isNew, err := isNewDisk(path)
		require.NoError(t, err)
		require.True(t, isNew)
		require.True(t, hasTag(t, path))
	})

	t.Run("small tagged and zero", func(t *testing.T) {
		path := newTestDisk(t, 4096, true)
		isNew, err := isNewDisk(path)
		require.NoError(t, err)
		require.True(t, isNew)
	})

	t.Run("not tagged", func(t *testing.T) {
		path := newTestDisk(t, size, false)
		isNew, err := isNewDisk(path)
		require.NoError(t, err)
		require.False(t, isNew)
	})

	for name, offset := range map[string]int64{
		"data at start": 64 * 1024,
		"data at end":   size - 100,
	} {
		t.Run("tagged with "+name, func(t *testing.T) {
			path := newTestDisk(t, size, true)
			writeAt(t, path, offset)

			isNew, err := isNewDisk(path)
			require.NoError(t, err)
			require.False(t, isNew)
			require.False(t, hasTag(t, path), "tag must be cleared")
		})
	}

	t.Run("cleared", func(t *testing.T) {
		path := newTestDisk(t, size, true)
		require.NoError(t, clearNewDisk(path))
		require.NoError(t, clearNewDisk(path), "clearing twice is fine")

		isNew, err := isNewDisk(path)
		require.NoError(t, err)
		require.False(t, isNew)
	})
}
