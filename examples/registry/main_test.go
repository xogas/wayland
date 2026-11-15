package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// serveNoGlobals is a minimal Wayland server for AC-X07. It answers
// wl_display.get_registry and wl_display.sync without ever advertising a
// global, then returns once the sync callback has been delivered.
func serveNoGlobals(f *os.File) error {
	gotRegistry := false
	for {
		var header [8]byte
		if _, err := io.ReadFull(f, header[:]); err != nil {
			return fmt.Errorf("read header: %w", err)
		}
		objectID := binary.LittleEndian.Uint32(header[0:4])
		word := binary.LittleEndian.Uint32(header[4:8])
		size := int(word >> 16)
		opcode := uint16(word)
		if size < 8 || size%4 != 0 {
			return fmt.Errorf("bad frame size %d", size)
		}
		body := make([]byte, size-8)
		if _, err := io.ReadFull(f, body); err != nil {
			return fmt.Errorf("read body: %w", err)
		}
		if objectID != 1 {
			return fmt.Errorf("unexpected object %d", objectID)
		}
		switch opcode {
		case 1: // wl_display.get_registry
			if len(body) != 4 {
				return fmt.Errorf("get_registry body %d", len(body))
			}
			gotRegistry = true
		case 0: // wl_display.sync
			if len(body) != 4 {
				return fmt.Errorf("sync body %d", len(body))
			}
			if !gotRegistry {
				return errors.New("sync before get_registry")
			}
			callbackID := binary.LittleEndian.Uint32(body[0:4])
			var frame [12]byte
			binary.LittleEndian.PutUint32(frame[0:4], callbackID)
			binary.LittleEndian.PutUint32(frame[4:8], uint32(len(frame))<<16|0)
			binary.LittleEndian.PutUint32(frame[8:12], 0)
			if _, err := f.Write(frame[:]); err != nil {
				return fmt.Errorf("write callback.done: %w", err)
			}
			return nil
		default:
			return fmt.Errorf("unexpected wl_display opcode %d", opcode)
		}
	}
}

// TestRegistryNoGlobals covers AC-X07: a server that sends no global events
// makes run return a "no globals" error, without a real sway session.
func TestRegistryNoGlobals(t *testing.T) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	t.Setenv("WAYLAND_SOCKET", strconv.Itoa(fds[0]))
	t.Setenv("WAYLAND_DISPLAY", "")

	server := os.NewFile(uintptr(fds[1]), "fake-wayland-server")
	defer server.Close()

	serverErr := make(chan error, 1)
	go func() { serverErr <- serveNoGlobals(server) }()

	runErr := run()
	if runErr == nil {
		t.Fatal("run returned nil, want no-globals error")
	}
	msg := strings.ToLower(runErr.Error())
	if !strings.Contains(msg, "no globals") && !strings.Contains(msg, "zero globals") {
		t.Fatalf("run error = %q, want a no globals / zero globals message", runErr)
	}

	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("fake server: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fake server did not finish")
	}
}
