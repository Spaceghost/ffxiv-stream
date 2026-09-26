// Package gpuprep makes a GPU passed into a container usable to the session
// (`xivstream prepare-gpu`, run at every container start as root).
package gpuprep

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"

	"github.com/Spaceghost/xivstream-dalamud/internal/sys"
)

// The NVIDIA userspace libraries arrive as bind mounts from the host when the
// GPU is attached, but the manifests that make them findable do not. Without
// them the libraries are present and invisible: EGL silently falls back to
// another GPU and Vulkan reports no NVIDIA device at all. They are rewritten
// at every start and removed when the GPU is gone, so a manifest never points
// at a library that is not there.
const (
	eglManifest    = "/usr/share/glvnd/egl_vendor.d/10_nvidia.json"
	vulkanManifest = "/usr/share/vulkan/icd.d/nvidia_icd.json"
)

const eglJSON = `{
    "file_format_version" : "1.0.0",
    "ICD" : { "library_path" : "libEGL_nvidia.so.0" }
}
`

const vulkanJSON = `{
    "file_format_version" : "1.0.0",
    "ICD": {
        "library_path": "libGLX_nvidia.so.0",
        "api_version" : "1.3.277"
    }
}
`

// Prepare fixes the NVIDIA manifests and the DRM node permissions.
func Prepare() error {
	lib := libDir()
	gbm := filepath.Join(lib, "gbm", "nvidia-drm_gbm.so")
	if sys.Exists(filepath.Join(lib, "libEGL_nvidia.so.0")) {
		// GBM loads its backend by name; libnvidia-allocator is the NVIDIA
		// backend, but nothing creates the name GBM looks for, so every
		// buffer allocation on the NVIDIA node failed and sway could not
		// bring its output up.
		if err := os.MkdirAll(filepath.Dir(gbm), 0o755); err != nil {
			return err
		}
		_ = os.Remove(gbm)
		if err := os.Symlink(filepath.Join(lib, "libnvidia-allocator.so.1"), gbm); err != nil {
			return err
		}
		for path, content := range map[string]string{eglManifest: eglJSON, vulkanManifest: vulkanJSON} {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return err
			}
		}
	} else {
		for _, p := range []string{eglManifest, vulkanManifest, gbm} {
			_ = os.Remove(p)
		}
	}
	// Incus makes the DRM nodes root:root 0660, so the session user cannot
	// open the card even though it is in the video group.
	video, err := user.LookupGroup("video")
	if err != nil {
		return fmt.Errorf("group video: %w", err)
	}
	gid, _ := strconv.Atoi(video.Gid)
	cards, _ := filepath.Glob("/dev/dri/card*")
	for _, n := range cards {
		if err := os.Chown(n, -1, gid); err != nil {
			return err
		}
		if err := os.Chmod(n, 0o660); err != nil {
			return err
		}
	}
	return nil
}

// libDir is the system library directory holding libc (lib64 on Fedora,
// lib/x86_64-linux-gnu on Debian).
func libDir() string {
	for _, d := range []string{"/usr/lib64", "/usr/lib/x86_64-linux-gnu", "/usr/lib"} {
		if sys.Exists(filepath.Join(d, "libc.so.6")) {
			return d
		}
	}
	return "/usr/lib"
}
