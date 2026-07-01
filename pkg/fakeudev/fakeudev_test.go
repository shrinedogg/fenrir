package fakeudev

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// encodeMessage must produce the same NUL-separated KEY=VALUE payload that
// wolf's Docker runner builds via utils::map_to_string before handing the
// message to `fake-udev -m`. Keys are emitted in sorted order.
func TestEncodeMessage(t *testing.T) {
	got := encodeMessage(map[string]string{
		"DEVNAME":   "/dev/input/event3",
		"ACTION":    "add",
		"SUBSYSTEM": "input",
	})
	want := []byte("ACTION=add\x00DEVNAME=/dev/input/event3\x00SUBSYSTEM=input\x00")
	if !bytes.Equal(got, want) {
		t.Errorf("encodeMessage = %q, want %q", got, want)
	}
}

func TestResolveHwDbFilename(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		major    string
		minor    string
		want     string
	}{
		{name: "recompute c0:0", filename: "c0:0", major: "13", minor: "67", want: "c13:67"},
		{name: "leave real filename", filename: "c13:67", major: "13", minor: "67", want: "c13:67"},
		{name: "no resolved major", filename: "c0:0", major: "0", minor: "0", want: "c0:0"},
		{name: "non-device filename", filename: "+input:event", major: "13", minor: "67", want: "+input:event"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ResolveHwDbFilename(c.filename, c.major, c.minor); got != c.want {
				t.Errorf("ResolveHwDbFilename(%q, %q, %q) = %q, want %q",
					c.filename, c.major, c.minor, got, c.want)
			}
		})
	}
}

// ResolveDevNumbers should fall back to /sys/$DEVPATH/dev when MAJOR is missing
// or "0", and mutate the map in place.
func TestResolveDevNumbers(t *testing.T) {
	tmp := t.TempDir()
	sysfsDev := filepath.Join(tmp, "devices", "virtual", "input", "event3", "dev")
	if err := os.MkdirAll(filepath.Dir(sysfsDev), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sysfsDev, []byte("13:67"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Temporarily relocate /sys onto the test tree so the resolver reads it.
	restore := withSysfsRoot(tmp)
	defer restore()

	props := map[string]string{
		"DEVPATH": "/devices/virtual/input/event3",
		"MAJOR":   "0",
		"MINOR":   "0",
	}
	major, minor := ResolveDevNumbers(props)
	if major != "13" || minor != "67" {
		t.Fatalf("ResolveDevNumbers = (%q, %q), want (13, 67)", major, minor)
	}
	if props["MAJOR"] != "13" || props["MINOR"] != "67" {
		t.Errorf("props not updated: MAJOR=%q MINOR=%q", props["MAJOR"], props["MINOR"])
	}
}

// withSysfsRoot points the package-level sysfsRoot at root for the duration
// of a test and returns a function restoring the previous value.
func withSysfsRoot(root string) func() {
	prev := sysfsRoot
	sysfsRoot = root
	return func() { sysfsRoot = prev }
}

// When MAJOR is already populated, sysfs should not be consulted.
func TestResolveDevNumbersAlreadyKnown(t *testing.T) {
	props := map[string]string{
		"DEVPATH": "/does/not/exist",
		"MAJOR":   "244",
		"MINOR":   "1",
	}
	major, minor := ResolveDevNumbers(props)
	if major != "244" || minor != "1" {
		t.Fatalf("ResolveDevNumbers = (%q, %q), want (244, 1)", major, minor)
	}
}
