package provisiontest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/threefoldtech/zos_base/pkg"
	"github.com/threefoldtech/zos_base/pkg/app"
	"github.com/threefoldtech/zos_base/pkg/environment"
	"github.com/threefoldtech/zos_base/pkg/gridtypes"
	"github.com/threefoldtech/zos_base/pkg/perf"
	"github.com/threefoldtech/zos_base/pkg/stubs"
)

// FListInfo contains virtual machine flist details
type FListInfo struct {
	ImagePath  string
	KernelPath string
	InitrdPath string
}

// IsContainer returns true if this is a container (no disk image)
func (f *FListInfo) IsContainer() bool {
	return len(f.ImagePath) == 0
}

// vmCheck deploys a test VM, waits, then decommissions it
func vmCheck(ctx context.Context) []error {
	var errs []error

	log.Debug().Msg("starting VM health check")

	cl := perf.MustGetZbusClient(ctx)
	vmd := stubs.NewVMModuleStub(cl)
	flist := stubs.NewFlisterStub(cl)

	// Create a test VM ID
	vmID := fmt.Sprintf("healthcheck-vm-%d", time.Now().Unix())
	flistURL := "https://hub.threefold.me/tf-official-apps/redis_zinit.flist"

	log.Debug().Str("vm_id", vmID).Str("flist", flistURL).Msg("deploying test VM")

	// Deploy the VM with timeout
	deployCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	// Mount cloud-container flist for kernel and initrd
	env := environment.MustGet()
	cloudContainerFlist, err := url.JoinPath(env.HubURL, "tf-autobuilder", "cloud-container-9dba60e.flist")
	if err != nil {
		errs = append(errs, errors.Wrap(err, "failed to construct cloud-container flist url"))
		if err := app.SetFlag(app.VMTestFailed); err != nil {
			log.Error().Err(err).Msg("failed to set VM test failed flag")
		}
		return errs
	}

	cloudImageID := fmt.Sprintf("healthcheck-cloud-%d", time.Now().Unix())
	cloudImage, err := flist.Mount(deployCtx, cloudImageID, cloudContainerFlist, pkg.ReadOnlyMountOptions)
	if err != nil {
		errs = append(errs, errors.Wrap(err, "failed to mount cloud container base image"))
		if err := app.SetFlag(app.VMTestFailed); err != nil {
			log.Error().Err(err).Msg("failed to set VM test failed flag")
		}
		return errs
	}

	// Ensure we unmount the cloud image when done
	defer func() {
		if unmountErr := flist.Unmount(context.Background(), cloudImageID); unmountErr != nil {
			log.Error().Err(unmountErr).Str("id", cloudImageID).Msg("failed to unmount cloud image")
		}
	}()

	// Mount the flist to inspect its contents
	log.Debug().Str("flist", flistURL).Msg("mounting flist")
	mountPath, err := flist.Mount(deployCtx, vmID, flistURL, pkg.MountOptions{
		ReadOnly: true,
	})
	if err != nil {
		errs = append(errs, errors.Wrap(err, "failed to mount flist"))
		if err := app.SetFlag(app.VMTestFailed); err != nil {
			log.Error().Err(err).Msg("failed to set VM test failed flag")
		}
		return errs
	}

	// Ensure we unmount the flist when done
	defer func() {
		if unmountErr := flist.Unmount(context.Background(), vmID); unmountErr != nil {
			log.Error().Err(unmountErr).Str("vm_id", vmID).Msg("failed to unmount flist")
		}
	}()

	log.Debug().Str("mount_path", mountPath).Msg("flist mounted successfully")

	// Get flist info (kernel, initrd, image paths)
	flistInfo, err := getFlistInfo(mountPath)
	if err != nil {
		errs = append(errs, errors.Wrap(err, "failed to get flist info"))
		if err := app.SetFlag(app.VMTestFailed); err != nil {
			log.Error().Err(err).Msg("failed to set VM test failed flag")
		}
		return errs
	}

	log.Debug().
		Bool("is_container", flistInfo.IsContainer()).
		Str("kernel", flistInfo.KernelPath).
		Str("initrd", flistInfo.InitrdPath).
		Str("image", flistInfo.ImagePath).
		Msg("flist info retrieved")

	// Create VM configuration
	vmConfig := pkg.VM{
		Name:        vmID,
		CPU:         1,
		Memory:      512 * gridtypes.Megabyte,
		Network:     pkg.VMNetworkInfo{},
		NoKeepAlive: false,
	}

	// Configure boot based on flist type
	if flistInfo.IsContainer() {
		// Container mode - boot from virtio-fs
		log.Debug().Msg("configuring as container VM")
		// Use kernel from cloud-container flist
		vmConfig.KernelImage = filepath.Join(cloudImage, "kernel")
		vmConfig.InitrdImage = filepath.Join(cloudImage, "initramfs-linux.img")

		// Can be overridden from the flist itself if exists
		if len(flistInfo.KernelPath) != 0 {
			vmConfig.KernelImage = flistInfo.KernelPath
			if len(flistInfo.InitrdPath) != 0 {
				vmConfig.InitrdImage = flistInfo.InitrdPath
			}
		}
		vmConfig.Boot = pkg.Boot{
			Type: pkg.BootVirtioFS,
			Path: mountPath,
		}
	} else {
		// VM mode - boot from disk image
		log.Debug().Msg("configuring as full VM with disk image")
		vmConfig.KernelImage = flistInfo.KernelPath
		if len(flistInfo.InitrdPath) != 0 {
			vmConfig.InitrdImage = flistInfo.InitrdPath
		}
		vmConfig.Boot = pkg.Boot{
			Type: pkg.BootDisk,
			Path: flistInfo.ImagePath,
		}
	}

	log.Debug().Str("vm_id", vmID).Str("boot_path", vmConfig.Boot.Path).Msg("deploying VM")

	// Deploy the VM
	machineInfo, err := vmd.Run(deployCtx, vmConfig)
	if err != nil {
		errs = append(errs, errors.Wrap(err, "failed to deploy VM"))
		if err := app.SetFlag(app.VMTestFailed); err != nil {
			log.Error().Err(err).Msg("failed to set VM test failed flag")
		}
		return errs
	}

	log.Debug().
		Str("vm_id", vmID).
		Str("console_url", machineInfo.ConsoleURL).
		Msg("test VM deployed successfully")

	// Wait 2 minutes to let the VM run
	time.Sleep(30 * time.Second)

	// Decommission the VM
	log.Debug().Str("vm_id", vmID).Msg("decommissioning test VM")

	decommissionCtx, cancelDecommission := context.WithTimeout(ctx, 1*time.Minute)
	defer cancelDecommission()

	if err := vmd.Delete(decommissionCtx, vmID); err != nil {
		errs = append(errs, errors.Wrap(err, "failed to decommission VM"))
		if err := app.SetFlag(app.VMTestFailed); err != nil {
			log.Error().Err(err).Msg("failed to set VM test failed flag")
		}
		return errs
	}

	log.Debug().Str("vm_id", vmID).Msg("test VM decommissioned successfully")

	// All checks passed, delete the flag if it was set
	if err := app.DeleteFlag(app.VMTestFailed); err != nil {
		log.Error().Err(err).Msg("failed to delete VM test failed flag")
	}

	return errs
}

// getFlistInfo inspects a mounted flist and extracts kernel, initrd, and image paths
func getFlistInfo(flistPath string) (flist FListInfo, err error) {
	files := map[string]*string{
		"/image.raw":       &flist.ImagePath,
		"/boot/vmlinuz":    &flist.KernelPath,
		"/boot/initrd.img": &flist.InitrdPath,
	}

	for rel, ptr := range files {
		path := filepath.Join(flistPath, rel)

		stat, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		} else if err != nil {
			return flist, errors.Wrapf(err, "couldn't stat %s", rel)
		}

		if stat.IsDir() {
			return flist, fmt.Errorf("path '%s' cannot be a directory", rel)
		}

		mod := stat.Mode()
		switch mod.Type() {
		case 0:
			// regular file, do nothing
		case os.ModeSymlink:
			// this is a symlink, validate it points inside the flist
			link, err := os.Readlink(path)
			if err != nil {
				return flist, errors.Wrapf(err, "failed to read link at '%s", rel)
			}
			// the link if joined with path (and cleaned) must point to somewhere under flistPath
			abs := filepath.Clean(filepath.Join(flistPath, link))
			if !strings.HasPrefix(abs, flistPath) {
				return flist, fmt.Errorf("path '%s' points to invalid location", rel)
			}
		default:
			return flist, fmt.Errorf("path '%s' is of invalid type: %s", rel, mod.Type().String())
		}

		// set the value
		*ptr = path
	}

	return flist, nil
}
