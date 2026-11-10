package controllers

import (
	"fmt"
	"reflect"
	"testing"

	v1alpha1types "games-on-whales.github.io/direwolf/pkg/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

// legacyAppContainerEnv is the app-container env assembly exactly as it was
// in reconcilePod before User.spec.gpuVendor existed (feat/multi-user @
// 37a1dbd). The nvidia path must keep producing this, key for key and in
// order, so existing NVIDIA clusters see no change.
func legacyAppContainerEnv(appEnv []corev1.EnvVar, tz string, cfg v1alpha1types.SessionInfo) []corev1.EnvVar {
	in := append([]corev1.EnvVar(nil), appEnv...)
	var pinnedVisibleDevices *corev1.EnvVar
	keptEnv := in[:0]
	for _, e := range in {
		if e.Name == "NVIDIA_VISIBLE_DEVICES" {
			v := e
			pinnedVisibleDevices = &v
			continue
		}
		keptEnv = append(keptEnv, e)
	}
	env := keptEnv
	env = append(env, []corev1.EnvVar{
		{Name: "DISPLAY", Value: ":0"},
		{Name: "WAYLAND_DISPLAY", Value: "wayland-1"},
		{Name: "TZ", Value: tz},
		{Name: "UNAME", Value: "retro"},
		{Name: "XDG_RUNTIME_DIR", Value: "/tmp/.X11-unix"},
		{Name: "PULSE_SERVER", Value: "unix:/tmp/.X11-unix/pulse-socket"},
		{Name: "LIBVA_DRIVER_NAME", Value: "nvidia"},
		{Name: "LD_LIBRARY_PATH", Value: "/usr/local/nvidia/lib:/usr/local/nvidia/lib64:/usr/local/lib"},
		{Name: "NVIDIA_DRIVER_CAPABILITIES", Value: "all"},
		{Name: "NVIDIA_VISIBLE_DEVICES", Value: "all"},
		{Name: "GST_VAAPI_ALL_DRIVERS", Value: "1"},
		{Name: "GST_DEBUG", Value: "2"},
		{Name: "GAMESCOPE_WIDTH", Value: fmt.Sprint(cfg.VideoWidth)},
		{Name: "GAMESCOPE_HEIGHT", Value: fmt.Sprint(cfg.VideoHeight)},
		{Name: "GAMESCOPE_REFRESH", Value: fmt.Sprint(cfg.VideoRefreshRate)},
	}...)
	if pinnedVisibleDevices != nil {
		for j, e := range env {
			if e.Name == "NVIDIA_VISIBLE_DEVICES" {
				env[j] = *pinnedVisibleDevices
			}
		}
	}
	return env
}

var testSessionInfo = v1alpha1types.SessionInfo{VideoWidth: 1920, VideoHeight: 1080, VideoRefreshRate: 60}

var appEnvCases = map[string][]corev1.EnvVar{
	"no app env": nil,
	"app env":    {{Name: "STEAM_ARGS", Value: "-bigpicture"}, {Name: "FOO", Value: "bar"}},
	"app pins NVIDIA_VISIBLE_DEVICES": {
		{Name: "FOO", Value: "bar"},
		{Name: "NVIDIA_VISIBLE_DEVICES", Value: "GPU-1234"},
		{Name: "BAZ", Value: "qux"},
	},
}

func TestAppContainerEnvNVIDIAMatchesLegacy(t *testing.T) {
	for _, vendor := range []string{"", v1alpha1types.GPUVendorNVIDIA} {
		for name, appEnv := range appEnvCases {
			t.Run(fmt.Sprintf("vendor=%q/%s", vendor, name), func(t *testing.T) {
				want := legacyAppContainerEnv(appEnv, "Europe/Berlin", testSessionInfo)
				got := appContainerEnv(appEnv, vendor, "Europe/Berlin", testSessionInfo)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("nvidia env changed\n got: %v\nwant: %v", got, want)
				}
			})
		}
	}
}

func TestAppContainerEnvAMD(t *testing.T) {
	nvidiaOnly := []string{
		"LIBVA_DRIVER_NAME", "LD_LIBRARY_PATH", "NVIDIA_DRIVER_CAPABILITIES",
		"NVIDIA_VISIBLE_DEVICES", "GST_VAAPI_ALL_DRIVERS",
	}
	for name, appEnv := range appEnvCases {
		t.Run(name, func(t *testing.T) {
			got := appContainerEnv(appEnv, v1alpha1types.GPUVendorAMD, "UTC", testSessionInfo)

			counts := map[string]int{}
			for _, e := range got {
				counts[e.Name]++
			}
			for k, n := range counts {
				if n > 1 {
					t.Errorf("env %s appears %d times (SSA rejects duplicates)", k, n)
				}
			}
			for _, k := range nvidiaOnly {
				appSet := false
				for _, e := range appEnv {
					if e.Name == k {
						appSet = true
					}
				}
				if counts[k] > 0 && !appSet {
					t.Errorf("amd env carries operator-injected %s", k)
				}
			}
			for _, k := range []string{"DISPLAY", "WAYLAND_DISPLAY", "TZ", "UNAME", "XDG_RUNTIME_DIR",
				"PULSE_SERVER", "GST_DEBUG", "GAMESCOPE_WIDTH", "GAMESCOPE_HEIGHT", "GAMESCOPE_REFRESH"} {
				if counts[k] != 1 {
					t.Errorf("amd env missing common %s", k)
				}
			}
			// The App CR's own env comes first, unchanged and in order.
			if len(appEnv) > 0 && !reflect.DeepEqual(got[:len(appEnv)], appEnv) {
				t.Errorf("app env not preserved: got %v, want prefix %v", got[:len(appEnv)], appEnv)
			}
		})
	}
}

func TestAppContainerEnvDoesNotMutateInput(t *testing.T) {
	appEnv := []corev1.EnvVar{
		{Name: "A", Value: "1"},
		{Name: "NVIDIA_VISIBLE_DEVICES", Value: "GPU-1"},
		{Name: "B", Value: "2"},
	}
	orig := append([]corev1.EnvVar(nil), appEnv...)
	_ = appContainerEnv(appEnv, v1alpha1types.GPUVendorNVIDIA, "UTC", testSessionInfo)
	if !reflect.DeepEqual(appEnv, orig) {
		t.Fatalf("input env mutated: %v", appEnv)
	}
}
