package main

import (
	"fmt"

	"github.com/xogas/wayland"
)

// keyName maps evdev keycodes (linux/input-event-codes.h) to short names.
func keyName(code uint32) string {
	names := map[uint32]string{
		1: "ESC", 2: "1", 3: "2", 4: "3", 5: "4", 6: "5", 7: "6", 8: "7",
		9: "8", 10: "9", 11: "0", 12: "-", 13: "=", 14: "BACKSPACE", 15: "TAB",
		16: "Q", 17: "W", 18: "E", 19: "R", 20: "T", 21: "Y", 22: "U",
		23: "I", 24: "O", 25: "P", 26: "[", 27: "]", 28: "ENTER", 29: "LCTRL",
		30: "A", 31: "S", 32: "D", 33: "F", 34: "G", 35: "H", 36: "J",
		37: "K", 38: "L", 39: ";", 40: "'", 41: "`", 42: "LSHIFT", 43: "\\",
		44: "Z", 45: "X", 46: "C", 47: "V", 48: "B", 49: "N", 50: "M",
		51: ",", 52: ".", 53: "/", 54: "RSHIFT", 56: "LALT", 57: "SPACE",
		58: "CAPS", 59: "F1", 60: "F2", 61: "F3", 62: "F4", 63: "F5",
		64: "F6", 65: "F7", 66: "F8", 67: "F9", 68: "F10", 87: "F11",
		88: "F12", 97: "RCTRL", 100: "RALT", 105: "LEFT", 106: "RIGHT",
		107: "DOWN", 103: "UP", 110: "INSERT", 111: "DELETE", 119: "PAUSE",
	}
	if n, ok := names[code]; ok {
		return n
	}
	return fmt.Sprintf("key(%d)", code)
}

// btnName maps evdev button codes to short names.
func btnName(code uint32) string {
	names := map[uint32]string{
		272: "left", 273: "right", 274: "middle", 275: "side1", 276: "side2",
	}
	if n, ok := names[code]; ok {
		return n
	}
	return fmt.Sprintf("btn(%d)", code)
}

// axisName describes a wl_pointer axis event direction.
func axisName(code wayland.PointerAxis) string {
	switch code {
	case wayland.PointerAxisVerticalScroll:
		return "vertical"
	case wayland.PointerAxisHorizontalScroll:
		return "horizontal"
	}
	return "?"
}
