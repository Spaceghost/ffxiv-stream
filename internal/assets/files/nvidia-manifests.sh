#!/bin/sh
# The NVIDIA userspace libraries arrive as bind mounts from the host when the
# GPU is attached, but the manifests that make them findable do not. Without
# these two files the libraries are present and invisible: EGL silently falls
# back to the iGPU and Vulkan reports no NVIDIA device at all, which is fatal
# for gamescope and costs Sunshine NVENC.
#
# They are rewritten on every start, and removed again when the GPU has been
# handed back, so a manifest never points at a library that is not there.
set -eu
EGL=/usr/share/glvnd/egl_vendor.d/10_nvidia.json
ICD=/usr/share/vulkan/icd.d/nvidia_icd.json
# GBM loads its backend by name from this directory. libnvidia-allocator IS the
# NVIDIA backend, but nothing here creates the name GBM looks for, so every
# buffer allocation on the NVIDIA node failed with "gbm_bo_create_with_modifiers
# failed: Success" and sway could not bring its output up at all.
GBM=/usr/lib64/gbm/nvidia-drm_gbm.so

if [ ! -e /usr/lib64/libEGL_nvidia.so.0 ]; then
    rm -f "$EGL" "$ICD" "$GBM"
    exit 0
fi

install -d /usr/lib64/gbm
ln -sf /usr/lib64/libnvidia-allocator.so.1 "$GBM"

install -d /usr/share/glvnd/egl_vendor.d /usr/share/vulkan/icd.d
cat > "$EGL" <<JSON
{
    "file_format_version" : "1.0.0",
    "ICD" : { "library_path" : "libEGL_nvidia.so.0" }
}
JSON
cat > "$ICD" <<JSON
{
    "file_format_version" : "1.0.0",
    "ICD": {
        "library_path": "libGLX_nvidia.so.0",
        "api_version" : "1.3.277"
    }
}
JSON
