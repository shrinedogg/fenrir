// Package fakeudev plays wolf's fake-udev role for hotplugged virtual input
// devices in a Kubernetes pod.
//
// Wolf creates virtual input devices (joypads, mice, keyboards) through
// /dev/uinput. The kernel emits the corresponding uevents only in the initial
// network namespace, so processes inside the session pod never hear about
// them, and libudev consumers (SDL, Steam) ignore devices that have no entry
// in the udev database (/run/udev/data).
//
// In wolf's Docker runner this is solved by exec'ing the `fake-udev` binary
// inside the app container, which builds the libudev monitor_netlink_header
// (magic, MurmurHash2 subsystem/devtype hashes, endianness) and broadcasts it
// over NETLINK_KOBJECT_UEVENT. Containers in a Kubernetes pod share their
// network namespace, so the wolf-agent sidecar can run that same binary and
// the broadcast lands in the right place.
//
// Rather than reimplementing the systemd wire format here (and owning a second
// copy that has to byte-match device-monitor.c forever), this package exec's
// wolf's own statically-linked `fake-udev` binary. It only handles the parts
// that are genuinely new in Kubernetes: resolving the real device numbers from
// sysfs, fixing up the hwdb filenames that were built from those numbers, and
// writing the udev database entries to a volume shared with the app container.
package fakeudev

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// FakeUdevBinaryPath is where the bundled fake-udev binary lives in the image.
// Overridable for tests.
var FakeUdevBinaryPath = "/usr/local/bin/fake-udev"

// sysfsRoot is the mount point used when resolving device numbers from sysfs.
// Overridable for tests; defaults to /sys.
var sysfsRoot = "/sys"

// SendEvent broadcasts a synthetic libudev netlink event by exec'ing wolf's
// fake-udev binary. The binary owns the monitor_netlink_header construction
// (MurmurHash2 filter hashes, magic, endianness, property framing) and the
// netlink socket, keeping us in lockstep with upstream systemd. Requires
// CAP_NET_ADMIN in the current network namespace.
//
// props is the map of udev properties for a single device event. It is
// serialized as NUL-separated KEY=VALUE pairs (keys sorted, mirroring wolf's
// utils::map_to_string) and base64-encoded, exactly as the Docker runner does
// before calling `fake-udev -m <msg>`.
func SendEvent(props map[string]string) error {
	msg := base64.StdEncoding.EncodeToString(encodeMessage(props))

	args := []string{"-m", msg}
	// fake-udev defaults its subsystem filter to "input" and its devtype
	// filter to empty; derive both from the event so non-input devices
	// (e.g. hidraw) get the correct filter hashes in the header.
	if subsystem := props["SUBSYSTEM"]; subsystem != "" {
		args = append(args, "--udev-subsystem", subsystem)
	}
	if devtype := props["DEVTYPE"]; devtype != "" {
		args = append(args, "--udev-devtype", devtype)
	}

	cmd := exec.Command(FakeUdevBinaryPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("fake-udev failed: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

// encodeMessage serializes udev properties as NUL-separated KEY=VALUE pairs
// with keys in sorted order, mirroring wolf's utils::map_to_string (used by
// the Docker runner before base64-encoding and handing the message to
// fake-udev). This is plain data encoding, not the libudev wire format.
func encodeMessage(props map[string]string) []byte {
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	for _, k := range keys {
		buf.WriteString(k)
		buf.WriteByte('=')
		buf.WriteString(props[k])
		buf.WriteByte(0)
	}
	return buf.Bytes()
}

// WriteHwDbEntry writes a udev database entry (e.g. "c13:67") under baseDir
// (normally /run/udev/data) so libudev enumeration picks up the device
// properties (ID_INPUT_JOYSTICK etc.). baseDir must be a volume shared with
// the app container (mounted at /run/udev there).
func WriteHwDbEntry(baseDir, filename string, content []string) error {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(baseDir, filepath.Base(filename))
	return os.WriteFile(path, []byte(strings.Join(content, "\n")), 0o644)
}

// RemoveHwDbEntry deletes a previously written udev database entry. A missing
// file is not an error (e.g. unplug for a device whose entry was never
// written).
func RemoveHwDbEntry(baseDir, filename string) error {
	err := os.Remove(filepath.Join(baseDir, filepath.Base(filename)))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ResolveDevNumbers fills in MAJOR/MINOR from sysfs when wolf could not stat
// the device node itself (it reports "0"/"0" in that case, because /dev/input
// is not mounted in the wolf container). DEVPATH is always present and sysfs
// is readable from any container, so /sys/$DEVPATH/dev holds the real
// "major:minor". Returns the resolved (or original) major/minor.
func ResolveDevNumbers(props map[string]string) (major, minor string) {
	major, minor = props["MAJOR"], props["MINOR"]
	if major != "" && major != "0" {
		return major, minor
	}
	devpath := props["DEVPATH"]
	if devpath == "" {
		return major, minor
	}
	data, err := os.ReadFile(filepath.Join(sysfsRoot, devpath, "dev"))
	if err != nil {
		return major, minor
	}
	parts := strings.SplitN(strings.TrimSpace(string(data)), ":", 2)
	if len(parts) != 2 {
		return major, minor
	}
	props["MAJOR"], props["MINOR"] = parts[0], parts[1]
	return parts[0], parts[1]
}

// ResolveHwDbFilename recomputes a "cMAJOR:MINOR" hwdb filename from the
// resolved device numbers. Wolf builds these filenames from the (zeroed)
// MAJOR/MINOR it reports when it can't stat the device node, so they arrive as
// "c0:0" and must be fixed up here. major/minor are the values returned by
// ResolveDevNumbers for the corresponding udev event.
func ResolveHwDbFilename(filename, major, minor string) string {
	if filename == "c0:0" && major != "" && major != "0" {
		return "c" + major + ":" + minor
	}
	return filename
}
