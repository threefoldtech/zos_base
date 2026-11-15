package storage

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/g0rbe/go-chattr"
	"github.com/pkg/errors"
	log "github.com/rs/zerolog/log"
	"github.com/threefoldtech/zos_base/pkg"
	"github.com/threefoldtech/zos_base/pkg/gridtypes"
)

const (
	// vdiskVolumeName is the name of the volume used to store vdisks
	vdiskVolumeName = "vdisks"

	// newDiskXattr is set by DiskCreate. zos only formats or writes an image
	// to a disk that still has it, so an existing disk is never overwritten
	// (threefoldtech/zos#2710). A missing tag always means "leave it alone".
	newDiskXattr = "user.zos.new-disk"
	// zeroCheckSize is how much of the start and the end of a disk must be
	// zero for a tagged disk to count as unused.
	zeroCheckSize = 2 * 1024 * 1024
)

// VDiskPools return a list of all vdisk pools
func (s *Module) diskPools() ([]string, error) {

	var paths []string
	for _, pool := range s.pools(PolicySSDFirst) {
		if _, err := pool.Mounted(); err != nil {
			continue
		}

		volumes, err := pool.Volumes()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to list pool '%s' volumes", pool.Path())
		}

		for _, volume := range volumes {
			if volume.Name() == vdiskVolumeName {
				paths = append(paths, volume.Path())
			}
		}
	}

	return paths, nil
}

// VDiskFindCandidate find a suitable location for creating a vdisk of the given size
func (s *Module) diskFindCandidate(size gridtypes.Unit) (path string, err error) {
	candidates, err := s.findCandidates(size, PolicySSDFirst)
	if err != nil {
		return path, err
	}
	// does anyone have a vdisk subvol
	for _, candidate := range candidates {
		volumes, err := candidate.Pool.Volumes()
		if err != nil {
			log.Error().Str("pool", candidate.Pool.Path()).Err(err).Msg("failed to list pool volumes")
			continue
		}
		for _, volume := range volumes {
			if volume.Name() != vdiskVolumeName {
				continue
			}

			return volume.Path(), nil
		}
	}
	// none has a vdiks subvolume, we need to
	// create one.
	candidate := candidates[0]
	volume, err := candidate.Pool.AddVolume(vdiskVolumeName)
	if err != nil {
		return path, errors.Wrap(err, "failed to create vdisk pool")
	}

	return volume.Path(), nil
}

func (s *Module) findDisk(id string) (string, error) {
	vdiskVolumes, err := s.diskPools()
	if err != nil {
		return "", errors.Wrapf(err, "failed to find disk with id '%s'", id)
	}

	for _, volumePath := range vdiskVolumes {
		path, err := s.safePath(volumePath, id)
		if err != nil {
			return "", err
		}

		if _, err := os.Stat(path); err == nil {
			// file exists
			return path, nil
		}
	}

	return "", os.ErrNotExist
}

// DiskFormat ensures that the virtual disk has a valid filesystem
// currently the fs is btrfs
func (s *Module) DiskFormat(name string) error {
	path, err := s.findDisk(name)
	if err != nil {
		return errors.Wrapf(err, "couldn't find disk with id: %s", name)
	}

	isNew, err := isNewDisk(path)
	if err != nil {
		return err
	}
	if !isNew {
		log.Debug().Str("disk", path).Msg("disk is not new. no format")
		return nil
	}

	if err := s.ensureFS(path); err != nil {
		return err
	}

	return clearNewDisk(path)
}

// DiskWrite writes image to disk. Disk will not be changed
// if it already has a filesystem or partition table.
func (s *Module) DiskWrite(name string, image string) error {
	path, err := s.findDisk(name)
	if err != nil {
		return errors.Wrapf(err, "couldn't find disk with id: %s", name)
	}

	isNew, err := isNewDisk(path)
	if err != nil {
		return err
	}
	if !isNew {
		log.Debug().Str("disk", path).Msg("disk is not new. no write")
		return nil
	}

	if !s.isEmptyDisk(path) {
		log.Warn().Str("disk", path).Msg("new disk already has a filesystem. no write")
		return clearNewDisk(path)
	}

	source, err := os.Open(image)
	if err != nil {
		return errors.Wrap(err, "failed to open image")
	}
	defer source.Close()
	file, err := os.OpenFile(path, os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	imgStat, err := source.Stat()
	if err != nil {
		return errors.Wrap(err, "failed to stat image")
	}
	fileStat, err := file.Stat()
	if err != nil {
		return errors.Wrap(err, "failed to state disk")
	}

	if imgStat.Size() > fileStat.Size() {
		return fmt.Errorf("image size is bigger than disk")
	}

	_, err = io.Copy(file, source)
	if err != nil {
		return errors.Wrap(err, "failed to write disk image")
	}

	log.Info().Str("disk", path).Str("image", image).Msg("wrote image to disk")
	return clearNewDisk(path)
}

// DiskCreate with given size, return path to virtual disk (size in MB)
func (s *Module) DiskCreate(name string, size gridtypes.Unit) (disk pkg.VDisk, err error) {
	path, err := s.findDisk(name)
	if err == nil {
		return disk, errors.Wrapf(os.ErrExist, "disk with id '%s' already exists", name)
	}

	base, err := s.diskFindCandidate(size)
	if err != nil {
		return disk, errors.Wrapf(err, "failed to find a candidate to host vdisk of size '%d'", size)
	}

	path, err = s.safePath(base, name)
	if err != nil {
		return disk, err
	}

	defer func() {
		// clean up disk file if error
		if err != nil {
			os.RemoveAll(path)
		}
	}()

	defer syscall.Sync()

	var file *os.File
	file, err = os.Create(path)
	if err != nil {
		return disk, err
	}

	defer file.Close()
	if err = syscall.Setxattr(path, newDiskXattr, []byte("1"), 0); err != nil {
		return disk, errors.Wrap(err, "failed to tag new disk")
	}

	if err = chattr.SetAttr(file, chattr.FS_NOCOW_FL); err != nil {
		return disk, err
	}

	if err = syscall.Fallocate(int(file.Fd()), 0, 0, int64(size)); err != nil {
		return disk, errors.Wrap(err, "failed to truncate disk to size")
	}

	return pkg.VDisk{Path: path, Size: int64(size)}, nil
}

// DiskCreate with given size, return path to virtual disk (size in MB)
func (s *Module) DiskResize(name string, size gridtypes.Unit) (disk pkg.VDisk, err error) {
	path, err := s.findDisk(name)
	if err != nil {
		return disk, errors.Wrapf(os.ErrNotExist, "disk with id '%s' does not exists", name)
	}

	file, err := os.OpenFile(path, os.O_RDWR, 0666)
	if err != nil {
		return pkg.VDisk{}, err
	}

	defer file.Close()

	if err = syscall.Fallocate(int(file.Fd()), 0, 0, int64(size)); err != nil {
		return disk, errors.Wrap(err, "failed to truncate disk to size")
	}

	return pkg.VDisk{Path: path, Size: int64(size)}, nil
}

func (s *Module) ensureFS(disk string) error {
	output, err := exec.Command("mkfs.btrfs", disk).CombinedOutput()
	if err == nil {
		log.Info().Str("disk", disk).Msg("formatted disk with btrfs")
		return nil
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		return errors.Wrapf(err, "failed to format disk '%s'", string(output))
	}

	if exitErr.ExitCode() == 1 &&
		strings.Contains(string(output), "ERROR: use the -f option to force overwrite") {
		// disk already have filesystem
		return nil
	}

	return errors.Wrapf(err, "unknown btrfs error '%s'", string(output))
}

// isNewDisk reports whether zos may format or write an image to the disk: it
// must still carry the tag set by DiskCreate, and its first and last
// zeroCheckSize bytes must be zero. A tagged disk that has data loses its tag.
func isNewDisk(path string) (bool, error) {
	if _, err := syscall.Getxattr(path, newDiskXattr, nil); err == syscall.ENODATA {
		return false, nil
	} else if err != nil {
		return false, errors.Wrapf(err, "failed to read tag of disk '%s'", path)
	}

	zeroed, err := isZeroed(path)
	if err != nil {
		return false, errors.Wrapf(err, "failed to read disk '%s'", path)
	}
	if !zeroed {
		log.Warn().Str("disk", path).Msg("new disk already has data. not touching it")
		return false, clearNewDisk(path)
	}

	return true, nil
}

func clearNewDisk(path string) error {
	err := syscall.Removexattr(path, newDiskXattr)
	if err != nil && err != syscall.ENODATA {
		return errors.Wrapf(err, "failed to clear tag of disk '%s'", path)
	}
	return nil
}

// isZeroed returns true if the first and last zeroCheckSize bytes of the file
// are all zero.
func isZeroed(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return false, err
	}

	size := min(stat.Size(), zeroCheckSize)
	buf := make([]byte, size)
	zero := make([]byte, size)
	for _, offset := range []int64{0, stat.Size() - size} {
		if _, err := file.ReadAt(buf, offset); err != nil {
			return false, err
		}
		if !bytes.Equal(buf, zero) {
			return false, nil
		}
	}

	return true, nil
}

// isEmptyDisk return true, if disk file has no partition table or filesystem
// else, returns false
func (s *Module) isEmptyDisk(disk string) bool {
	err := exec.Command("blkid", disk).Run()

	return err != nil
}

func (s *Module) safePath(base, id string) (string, error) {
	path := filepath.Join(base, id)
	// this to avoid passing an `injection` id like '../name'
	// and end up deleting a file on the system. so only delete
	// allocated disks
	location := filepath.Dir(path)
	if filepath.Clean(location) != base {
		return "", fmt.Errorf("invalid disk id: '%s'", id)
	}

	return path, nil
}

// DiskDelete removes a virtual disk
func (s *Module) DiskDelete(name string) error {
	path, err := s.findDisk(name)
	if os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

// DiskExists checks if a disk exists
func (s *Module) DiskExists(id string) bool {
	_, err := s.findDisk(id)

	return err == nil
}

// DiskLookup return info about the disk
func (s *Module) DiskLookup(id string) (disk pkg.VDisk, err error) {
	path, err := s.findDisk(id)

	if err != nil {
		return disk, err
	}

	disk.Path = path
	stat, err := os.Stat(path)
	if err != nil {
		return disk, err
	}

	disk.Size = stat.Size()
	return
}

// DiskList list all created disks
func (s *Module) DiskList() ([]pkg.VDisk, error) {
	pools, err := s.diskPools()
	if err != nil {
		return nil, err
	}
	var disks []pkg.VDisk
	for _, pool := range pools {

		items, err := os.ReadDir(pool)
		if err != nil {
			return nil, errors.Wrap(err, "failed to list virtual disks")
		}

		for _, item := range items {
			if item.IsDir() {
				continue
			}

			info, err := item.Info()
			if err != nil {
				return nil, errors.Wrapf(err, "failed to get file info for '%s'", item.Name())
			}

			disks = append(disks, pkg.VDisk{
				Path: filepath.Join(pool, item.Name()),
				Size: info.Size(),
			})
		}

		return disks, nil
	}

	return disks, nil
}
