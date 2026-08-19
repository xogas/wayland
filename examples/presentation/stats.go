package main

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"github.com/xogas/wayland/protocol/stable/presentationtime"
)

// latencyStats accumulates commit-to-present delays. It is written by the
// dispatch goroutine (feedback callbacks) and read by the main loop, hence
// the mutex.
type latencyStats struct {
	mu        sync.Mutex
	n         int
	minNS     int64
	maxNS     int64
	totalNS   int64
	refresh   uint32
	flags     presentationtime.PresentationFeedbackKind
	discarded int
}

func (s *latencyStats) record(ns int64, refresh uint32, flags presentationtime.PresentationFeedbackKind) {
	s.mu.Lock()
	s.n++
	s.totalNS += ns
	if s.minNS == 0 || ns < s.minNS {
		s.minNS = ns
	}
	if ns > s.maxNS {
		s.maxNS = ns
	}
	s.refresh = refresh
	s.flags = flags
	s.mu.Unlock()
}

func (s *latencyStats) discard() {
	s.mu.Lock()
	s.discarded++
	s.mu.Unlock()
}

// report prints the accumulated statistics and resets them.
func (s *latencyStats) report() {
	s.mu.Lock()
	if s.n == 0 {
		s.mu.Unlock()
		return
	}
	avgMS := float64(s.totalNS/int64(s.n)) / 1e6
	minMS := float64(s.minNS) / 1e6
	maxMS := float64(s.maxNS) / 1e6
	refresh := s.refresh
	flags := s.flags
	discarded := s.discarded
	s.n = 0
	s.minNS = 0
	s.maxNS = 0
	s.totalNS = 0
	s.refresh = 0
	s.flags = 0
	s.discarded = 0
	s.mu.Unlock()

	fmt.Printf("presentation: avg %.2f ms min %.2f ms max %.2f ms | refresh %d ns | flags %s",
		avgMS, minMS, maxMS, refresh, flagsString(flags))
	if discarded > 0 {
		fmt.Printf(" | discarded %d", discarded)
	}
	fmt.Println()
}

// flagsString renders the presentation feedback kind flags as a list.
func flagsString(f presentationtime.PresentationFeedbackKind) string {
	s := ""
	if f&presentationtime.PresentationFeedbackKindVsync != 0 {
		s += "vsync "
	}
	if f&presentationtime.PresentationFeedbackKindHwClock != 0 {
		s += "hw_clock "
	}
	if f&presentationtime.PresentationFeedbackKindHwCompletion != 0 {
		s += "hw_completion "
	}
	if f&presentationtime.PresentationFeedbackKindZeroCopy != 0 {
		s += "zero_copy "
	}
	if s == "" {
		return "none"
	}
	return s[:len(s)-1]
}

// monotonicNow reads the compositor's presentation clock (CLOCK_MONOTONIC by
// default; use the clock_id reported by wp_presentation for the exact clock).
func monotonicNow(clkID int32) int64 {
	var ts syscall.Timespec
	_, _, e1 := syscall.Syscall(syscall.SYS_CLOCK_GETTIME, uintptr(clkID), uintptr(unsafe.Pointer(&ts)), 0)
	if e1 != 0 {
		return 0
	}
	return ts.Sec*1e9 + ts.Nsec
}
