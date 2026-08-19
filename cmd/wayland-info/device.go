package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// deviceString renders a DRM device id, with its /dev/dri nodes when known.
func deviceString(dev uint64) string {
	if path := devPath(dev); path != "" {
		return fmt.Sprintf("0x%X (%s)", dev, path)
	}
	return fmt.Sprintf("0x%X", dev)
}

// devPath returns the /dev/dri nodes for a DRM device id, joined with
// " or ", or "" when unknown.
func devPath(dev uint64) string {
	entries, err := os.ReadDir("/dev/dri")
	if err != nil {
		return ""
	}
	var paths []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "card") && !strings.HasPrefix(name, "renderD") {
			continue
		}
		path := "/dev/dri/" + name
		var stat syscall.Stat_t
		if err := syscall.Stat(path, &stat); err != nil {
			continue
		}
		if stat.Rdev == dev {
			paths = append(paths, path)
		}
	}
	return strings.Join(paths, " or ")
}
