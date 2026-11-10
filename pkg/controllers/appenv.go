package controllers

import (
	"fmt"

	v1alpha1types "games-on-whales.github.io/direwolf/pkg/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

// gpuVendorEnv returns the GPU-vendor-specific variables appended to every
// app container, selected by User.spec.gpuVendor.
//
// "nvidia" (and the empty string, for Users created before the field existed)
// returns the NVIDIA container-runtime and VA-API-on-NVIDIA variables the
// operator has always injected, in the same order.
//
// "amd" returns none of them: Mesa (radeonsi for VA-API, RADV for Vulkan)
// picks its driver from the render node, and LIBVA_DRIVER_NAME=nvidia would
// force libva onto a driver that doesn't exist on the card.
func gpuVendorEnv(vendor string) []corev1.EnvVar {
	switch vendor {
	case v1alpha1types.GPUVendorAMD:
		return nil
	default:
		return []corev1.EnvVar{
			{Name: "LIBVA_DRIVER_NAME", Value: "nvidia"},
			{Name: "LD_LIBRARY_PATH", Value: "/usr/local/nvidia/lib:/usr/local/nvidia/lib64:/usr/local/lib"},
			{Name: "NVIDIA_DRIVER_CAPABILITIES", Value: "all"},
			{Name: "NVIDIA_VISIBLE_DEVICES", Value: "all"},
			{Name: "GST_VAAPI_ALL_DRIVERS", Value: "1"},
		}
	}
}

// appContainerEnv returns the app container's env: the App CR's own env
// followed by the operator's standard GOW variables, the GPU-vendor block
// and the gamescope geometry.
//
// If the App CR pins NVIDIA_VISIBLE_DEVICES (e.g. per-GPU isolation) and the
// vendor block also sets it, the App's value replaces the operator's "all" in
// place, so the env holds the key once (SSA rejects duplicate env names and the
// Deployment would never be created). When the vendor block doesn't set it
// (amd), the App's entry is left where the App put it.
func appContainerEnv(appEnv []corev1.EnvVar, gpuVendor, tz string, cfg v1alpha1types.SessionInfo) []corev1.EnvVar {
	vendorEnv := gpuVendorEnv(gpuVendor)
	vendorSets := make(map[string]bool, len(vendorEnv))
	for _, e := range vendorEnv {
		vendorSets[e.Name] = true
	}

	var pinnedVisibleDevices *corev1.EnvVar
	env := make([]corev1.EnvVar, 0, len(appEnv)+16)
	for _, e := range appEnv {
		if e.Name == "NVIDIA_VISIBLE_DEVICES" && vendorSets[e.Name] {
			v := e
			pinnedVisibleDevices = &v
			continue
		}
		env = append(env, e)
	}

	env = append(env,
		// Standard GOW envars
		corev1.EnvVar{Name: "DISPLAY", Value: ":0"},
		// Container must have extra logic to wait for this to be set up
		// unfortunately.
		corev1.EnvVar{Name: "WAYLAND_DISPLAY", Value: "wayland-1"},
		corev1.EnvVar{Name: "TZ", Value: tz},
		corev1.EnvVar{Name: "UNAME", Value: "retro"},
		corev1.EnvVar{Name: "XDG_RUNTIME_DIR", Value: "/tmp/.X11-unix"},
		// PULSE_SINK & PULSE_SOURCE set at runtime calculated based off session ID.
		corev1.EnvVar{Name: "PULSE_SERVER", Value: "unix:/tmp/.X11-unix/pulse-socket"},
	)
	env = append(env, vendorEnv...)
	env = append(env,
		corev1.EnvVar{Name: "GST_DEBUG", Value: "2"},
		// Gamescope geometry from the client's requested stream.
		corev1.EnvVar{Name: "GAMESCOPE_WIDTH", Value: fmt.Sprint(cfg.VideoWidth)},
		corev1.EnvVar{Name: "GAMESCOPE_HEIGHT", Value: fmt.Sprint(cfg.VideoHeight)},
		corev1.EnvVar{Name: "GAMESCOPE_REFRESH", Value: fmt.Sprint(cfg.VideoRefreshRate)},
	)

	if pinnedVisibleDevices != nil {
		for j := range env {
			if env[j].Name == "NVIDIA_VISIBLE_DEVICES" {
				env[j] = *pinnedVisibleDevices
			}
		}
	}
	return env
}
