package store

import (
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/pkg/errors"
	"github.com/threefoldtech/zos_base/pkg/versioned"
	"github.com/tyler-smith/go-bip39"
)

// Version History:
//   1.0.0: seed binary directly encoded
//   1.1.0: json with key mnemonic and threebot id

var (
	// SeedVersion1 (binary seed)
	SeedVersion1 = versioned.MustParse("1.0.0")
	// SeedVersion11 (json mnemonic)
	SeedVersion11 = versioned.MustParse("1.1.0")
	// SeedVersionLatest link to latest seed version
	SeedVersionLatest = SeedVersion11
)

type FileStore struct {
	path string
}

var _ Store = (*FileStore)(nil)

func NewFileStore(path string) *FileStore {
	return &FileStore{path}
}

func (f *FileStore) Kind() string {
	return "file-store"
}

func (f *FileStore) Set(key ed25519.PrivateKey) error {
	seed := key.Seed()
	// write to primary location first
	if err := versioned.WriteFile(f.path, SeedVersion1, seed, 0400); err != nil {
		return err
	}

	// also mirror the seed to all mounted disks under /mnt/*/seed.txt
	mirrors, _ := f.mirrorPaths()
	for _, p := range mirrors {
		_ = versioned.WriteFile(p, SeedVersion1, seed, 0400)
	}
	return nil
}

func (f *FileStore) Annihilate() error {
	return os.Remove(f.path)
}

func (f *FileStore) Get() (ed25519.PrivateKey, error) {
	key, err := f.readKeyFrom(f.path)
	if errors.Is(err, ErrKeyDoesNotExist) {
		// try to recover from any mirrored copies on mounted disks
		mirrors, _ := f.mirrorPaths()
		for _, p := range mirrors {
			if k, merr := f.readKeyFrom(p); merr == nil {
				// write back to primary location for future boots
				_ = os.MkdirAll(filepath.Dir(f.path), 0o700)
				_ = versioned.WriteFile(f.path, SeedVersion1, k.Seed(), 0400)
				// ensure the seed is present across all mirrors
				_ = f.ensureMirrors(k.Seed())
				return k, nil
			}
		}
	}
	if err == nil {
		// ensure the seed is present across all mirrors on every successful read
		_ = f.ensureMirrors(key.Seed())
	}
	return key, err
}

func (f *FileStore) Exists() (bool, error) {
	if _, err := os.Stat(f.path); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, errors.Wrap(err, "failed to check seed file")
	}

	return true, nil
}

// ensureMirrors guarantees that the seed file exists on all mirror locations under /mnt/*
// It is best-effort; failures for individual mirrors are ignored.
func (f *FileStore) ensureMirrors(seed []byte) error {
	mirrors, err := f.mirrorPaths()
	if err != nil {
		return err
	}
	for _, p := range mirrors {
		if _, statErr := os.Stat(p); os.IsNotExist(statErr) {
			_ = versioned.WriteFile(p, SeedVersion1, seed, 0400)
		}
	}
	return nil
}

// readKeyFrom reads and decodes an identity seed from a given path
// supporting both 1.0.0 (raw seed) and 1.1.0 (mnemonic json) formats.
func (f *FileStore) readKeyFrom(path string) (ed25519.PrivateKey, error) {
	version, data, err := versioned.ReadFile(path)
	if versioned.IsNotVersioned(err) {
		// compatibility for old non-versioned seed files
		if err := versioned.WriteFile(path, SeedVersionLatest, data, 0400); err != nil {
			return nil, err
		}
		version = SeedVersion1
	} else if os.IsNotExist(err) {
		return nil, ErrKeyDoesNotExist
	} else if err != nil {
		return nil, err
	}

	if version.NE(SeedVersion1) && version.NE(SeedVersion11) {
		return nil, errors.Wrap(ErrInvalidKey, "unknown seed version")
	}

	if version.EQ(SeedVersion1) {
		return keyFromSeed(data)
	}

	// it means we read json data instead of the secret
	type Seed110Struct struct {
		Mnemonics string `json:"mnemonic"`
	}
	var seed110 Seed110Struct
	if err = json.Unmarshal(data, &seed110); err != nil {
		return nil, errors.Wrapf(ErrInvalidKey, "failed to decode seed: %s", err)
	}

	seed, err := bip39.EntropyFromMnemonic(seed110.Mnemonics)
	if err != nil {
		return nil, errors.Wrapf(ErrInvalidKey, "failed to decode mnemonics: %s", err)
	}

	return keyFromSeed(seed)
}

// mirrorPaths lists candidate seed paths under all mounted disks in /mnt/*
// The path is mirrored at: /mnt/<disk>/seed.txt (flat, no original directories)
func (f *FileStore) mirrorPaths() ([]string, error) {
	entries, err := os.ReadDir("/mnt")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := filepath.Base(f.path) // usually "seed.txt"
		paths = append(paths, filepath.Join("/mnt", e.Name(), name))
	}
	return paths, nil
}
