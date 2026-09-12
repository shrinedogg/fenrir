package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"games-on-whales.github.io/direwolf/pkg/fakeudev"
	"games-on-whales.github.io/direwolf/pkg/wolfapi"
	"golang.org/x/sys/unix"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog/v2"
)

// UdevDataPath is where udev database entries are written for hotplugged
// devices. It must be a volume shared with the app container (mounted at
// /run/udev there) for libudev consumers (SDL/Steam) to see them.
const UdevDataPath = "/run/udev/data"

// DevInputPath is the pod-private /dev/input directory (an emptyDir shared
// between the app container and this agent). The agent creates device nodes
// here only for the devices wolf hotplugs for THIS session. No session pod
// mounts the host's /dev/input, so uinput devices belonging to other
// sessions on the same node (other wolves' pads, Steam Input virtual
// controllers) have no node here and are invisible to SDL/libinput/Steam.
const DevInputPath = "/dev/input"

// Represents the controller that runs inside the Pod itself
type Agent struct {
	WolfClient wolfapi.Client
}

func NewAgent(
	wolfClient wolfapi.Client,
) *Agent {
	res := &Agent{
		WolfClient: wolfClient,
	}

	return res
}

func (a *Agent) Run(ctx context.Context) error {
	klog.Infof("Starting Agent")

	// Sanitize the pod-private /dev/input. For privileged containers runc
	// walks the host /dev AFTER applying volume mounts and copies every
	// host device node into the container's /dev (verified: a bare
	// privileged busybox with an emptyDir at /dev/input receives the whole
	// host input node set, including other sessions' wolf pads if they
	// exist at container start). Everything present before the first
	// PlugDevice event is such a coldplug copy; this session's devices
	// only ever arrive here afterwards via createDeviceNode. Remove them
	// all so the pod starts with an input namespace that contains exactly
	// this session's devices.
	if err := sanitizeDevInput(); err != nil {
		utilruntime.HandleError(fmt.Errorf("failed to sanitize %s: %w", DevInputPath, err))
	}

	if err := a.watchEvents(ctx); err != nil {
		return err
	}

	klog.Infof("Agent started")
	return nil
}

// sanitizeDevInput removes every entry from the pod-private /dev/input,
// including the by-id/by-path symlinks runc's coldplug leaves behind.
func sanitizeDevInput() error {
	entries, err := os.ReadDir(DevInputPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	removed := 0
	for _, e := range entries {
		if err := os.Remove(filepath.Join(DevInputPath, e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", e.Name(), err)
		}
		removed++
	}
	klog.InfoS("Sanitized pod-private /dev/input", "removed", removed)
	return nil
}

func (a *Agent) watchEvents(ctx context.Context) error {
	ch, err := a.WolfClient.SubscribeToEvents(ctx)
	if err != nil {
		return err
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-ch:
				if !ok || ev == nil {
					klog.Infof("Event channel closed")
					return
				}

				klog.Infof("Received event: %s", ev.Event)
				klog.Infof("Event ID: %s", ev.ID)
				klog.Infof("Event Data: %s", ev.Data)
				klog.Infof("Event Retry: %d", ev.Retry)
				klog.Infof("Event Comment: %v", ev.Comment)

				switch wolfapi.WolfEventType(ev.Event) {
				// Wolf handles a moonlight disconnect as a "Pause".
				// When moonlight disconnects from Wolf we should reflect that
				// into the state in Kubernetes so things can be cleaned up.
				case wolfapi.PauseStreamEventType:
					var pauseEvent wolfapi.PauseStreamEvent
					if err := json.Unmarshal(ev.Data, &pauseEvent); err != nil {
						utilruntime.HandleError(fmt.Errorf("failed to unmarshal pause stream event: %w", err))
						continue
					}

					if err := a.WolfClient.StopSession(ctx, pauseEvent.SessionID); err != nil {
						utilruntime.HandleError(fmt.Errorf("failed to stop session: %w", err))
						continue
					}
				// Wolf hotplugged a virtual input device. Play the role of
				// fake-udev: publish the udev db entry and broadcast a
				// synthetic udev netlink event in the pod's network
				// namespace so the app container's SDL/Steam notices it.
				case wolfapi.PlugDeviceEventType:
					var plugEvent wolfapi.PlugDeviceEvent
					if err := json.Unmarshal(ev.Data, &plugEvent); err != nil {
						utilruntime.HandleError(fmt.Errorf("failed to unmarshal plug device event: %w", err))
						continue
					}
					a.handleDevicePlug(plugEvent)
				case wolfapi.UnplugDeviceEventType:
					var unplugEvent wolfapi.UnplugDeviceEvent
					if err := json.Unmarshal(ev.Data, &unplugEvent); err != nil {
						utilruntime.HandleError(fmt.Errorf("failed to unmarshal unplug device event: %w", err))
						continue
					}
					a.handleDeviceUnplug(unplugEvent)
				default:
					continue
				}
			}
		}
	}()

	return nil
}

func (a *Agent) handleDevicePlug(ev wolfapi.PlugDeviceEvent) {
	// Wolf reports MAJOR/MINOR as "0" when it cannot stat the device node
	// in its own container; resolve the real numbers from sysfs so both the
	// netlink event and the hwdb filename are correct. Each event is
	// resolved independently; the (resolved) first event's numbers feed the
	// hwdb filename recompute below.
	for _, udevEvent := range ev.UdevEvents {
		fakeudev.ResolveDevNumbers(udevEvent)
	}

	hwdbMajor, hwdbMinor := "", ""
	if len(ev.UdevEvents) > 0 {
		hwdbMajor, hwdbMinor = ev.UdevEvents[0]["MAJOR"], ev.UdevEvents[0]["MINOR"]
	}

	for _, entry := range ev.UdevHwDbEntries {
		filename := fakeudev.ResolveHwDbFilename(entry.Filename, hwdbMajor, hwdbMinor)
		if err := fakeudev.WriteHwDbEntry(UdevDataPath, filename, entry.Content); err != nil {
			utilruntime.HandleError(fmt.Errorf("failed to write udev hwdb entry %q: %w", filename, err))
		} else {
			klog.InfoS("Wrote udev hwdb entry", "file", filename, "session", ev.SessionID)
		}
	}

	// Create the device node in the pod-private /dev/input BEFORE the fake
	// udev event is sent, so consumers reacting to the event can open the
	// node immediately. This is what scopes input to this session: only the
	// devices wolf reports for this session ever get a node in the pod.
	for _, udevEvent := range ev.UdevEvents {
		createDeviceNode(udevEvent)
	}

	for _, udevEvent := range ev.UdevEvents {
		if err := fakeudev.SendEvent(udevEvent); err != nil {
			utilruntime.HandleError(fmt.Errorf("failed to send udev event for %q: %w", udevEvent["DEVNAME"], err))
		} else {
			klog.InfoS("Sent fake udev event", "action", udevEvent["ACTION"], "devname", udevEvent["DEVNAME"], "session", ev.SessionID)
		}
	}
}

func (a *Agent) handleDeviceUnplug(ev wolfapi.UnplugDeviceEvent) {
	for _, udevEvent := range ev.UdevEvents {
		fakeudev.ResolveDevNumbers(udevEvent)
	}

	hwdbMajor, hwdbMinor := "", ""
	if len(ev.UdevEvents) > 0 {
		hwdbMajor, hwdbMinor = ev.UdevEvents[0]["MAJOR"], ev.UdevEvents[0]["MINOR"]
	}

	for _, entry := range ev.UdevHwDbEntries {
		filename := fakeudev.ResolveHwDbFilename(entry.Filename, hwdbMajor, hwdbMinor)
		if err := fakeudev.RemoveHwDbEntry(UdevDataPath, filename); err != nil {
			utilruntime.HandleError(fmt.Errorf("failed to remove udev hwdb entry %q: %w", filename, err))
		}
	}

	for _, udevEvent := range ev.UdevEvents {
		udevEvent["ACTION"] = "remove"
		// Remove the device node from the pod-private /dev/input so the
		// app stops seeing the device (mirrors the real udev behaviour).
		removeDeviceNode(udevEvent)
		if err := fakeudev.SendEvent(udevEvent); err != nil {
			utilruntime.HandleError(fmt.Errorf("failed to send udev remove event for %q: %w", udevEvent["DEVNAME"], err))
		} else {
			klog.InfoS("Sent fake udev remove event", "devname", udevEvent["DEVNAME"], "session", ev.SessionID)
		}
	}
}

// deviceNodePath returns the path of the device node inside the pod-private
// /dev/input for a udev event, or "" if the event has no DEVNAME or the
// DEVNAME is not under /dev/input.
func deviceNodePath(udevEvent map[string]string) string {
	devname := udevEvent["DEVNAME"]
	if devname == "" {
		return ""
	}
	if !strings.HasPrefix(devname, "/dev/input/") {
		// Not an input device node (e.g. /dev/uinput itself); nothing to do.
		return ""
	}
	return filepath.Join(DevInputPath, strings.TrimPrefix(devname, "/dev/input/"))
}

// createDeviceNode mknods the device into the pod-private /dev/input using
// the (sysfs-resolved) MAJOR/MINOR carried by the udev event. The node is
// created world-accessible (0666) because the app runs as a different,
// non-root uid than the agent.
func createDeviceNode(udevEvent map[string]string) {
	node := deviceNodePath(udevEvent)
	if node == "" {
		return
	}
	major, err1 := strconv.Atoi(udevEvent["MAJOR"])
	minor, err2 := strconv.Atoi(udevEvent["MINOR"])
	if err1 != nil || err2 != nil || (major == 0 && minor == 0) {
		// Without usable numbers we cannot create a node; skip (the fake
		// udev event is still sent, so the app may notice the device via
		// sysfs, but it won't be able to open it).
		klog.InfoS("Skipping device node creation: no MAJOR/MINOR", "devname", udevEvent["DEVNAME"])
		return
	}
	mode := uint32(unix.S_IFCHR | 0o666)
	dev := unix.Mkdev(uint32(major), uint32(minor))
	if err := unix.Mknod(node, mode, int(dev)); err != nil {
		if errors.Is(err, unix.EEXIST) {
			// Node already exists (e.g. session restart re-plug); ensure
			// permissions in case the previous owner was more restrictive.
			if chmodErr := os.Chmod(node, 0o666); chmodErr != nil {
				utilruntime.HandleError(fmt.Errorf("failed to chmod %q: %w", node, chmodErr))
			}
			return
		}
		utilruntime.HandleError(fmt.Errorf("failed to mknod %q: %w", node, err))
		return
	}
	klog.InfoS("Created device node", "node", node, "major", major, "minor", minor)
}

// removeDeviceNode unlinks the device node from the pod-private /dev/input.
func removeDeviceNode(udevEvent map[string]string) {
	node := deviceNodePath(udevEvent)
	if node == "" {
		return
	}
	if err := os.Remove(node); err != nil && !errors.Is(err, os.ErrNotExist) {
		utilruntime.HandleError(fmt.Errorf("failed to remove %q: %w", node, err))
		return
	}
	klog.InfoS("Removed device node", "node", node)
}
