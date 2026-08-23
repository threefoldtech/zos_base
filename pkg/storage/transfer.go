package storage

import (
	"github.com/pkg/errors"
	log "github.com/rs/zerolog/log"
	"github.com/threefoldtech/zos_base/pkg/gridtypes"
	"github.com/threefoldtech/zos_base/pkg/storagetransfer"
)

// DiskUpload streams the raw vdisk file identified by id to the given presigned
// URL using an HTTP PUT. Used to move a zmount (or a full-VM boot disk) to another
// node during a deployment transfer.
func (s *Module) DiskUpload(id string, url string) error {
	disk, err := s.DiskLookup(id)
	if err != nil {
		return errors.Wrapf(err, "failed to lookup disk '%s'", id)
	}
	log.Info().Str("id", id).Str("path", disk.Path).Msg("uploading disk")
	return storagetransfer.UploadFile(disk.Path, url)
}

// DiskDownload downloads bytes from the given presigned URL and writes them into
// the existing vdisk file identified by id. The disk must already exist (it is
// created when the zmount workload is provisioned during prepare).
func (s *Module) DiskDownload(id string, url string) error {
	disk, err := s.DiskLookup(id)
	if err != nil {
		return errors.Wrapf(err, "failed to lookup disk '%s'", id)
	}
	log.Info().Str("id", id).Str("path", disk.Path).Msg("downloading disk")
	return storagetransfer.DownloadToFile(disk.Path, url)
}

// VolumeUpload tars the subvolume named `name` and streams it to the given
// presigned URL using an HTTP PUT. Used to move a VM's writable rootfs layer
// (subvolume "rootfs:<workloadID>") to another node.
func (s *Module) VolumeUpload(name string, url string) error {
	vol, err := s.VolumeLookup(name)
	if err != nil {
		return errors.Wrapf(err, "failed to lookup volume '%s'", name)
	}
	log.Info().Str("name", name).Str("path", vol.Path).Msg("uploading volume")
	return storagetransfer.UploadDir(vol.Path, url)
}

// VolumeDownload makes sure a subvolume `name` of the given size exists, then
// downloads a tar from the presigned URL and extracts it into the subvolume.
func (s *Module) VolumeDownload(name string, size gridtypes.Unit, url string) error {
	vol, err := s.VolumeCreate(name, size)
	if err != nil {
		return errors.Wrapf(err, "failed to create volume '%s'", name)
	}
	log.Info().Str("name", name).Str("path", vol.Path).Msg("downloading volume")
	return storagetransfer.DownloadDir(vol.Path, url)
}
