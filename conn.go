package wayland

import (
	"net"
	"sync"
	"sync/atomic"
)

const (
	clientIDMax = 0xFEFFFFFF
	serverIDMin = 0xFF000000
	messageMax  = 65535
)

type conn struct {
	uc             *net.UnixConn
	mu             sync.Mutex
	writeMu        sync.Mutex
	dispatchMu     sync.Mutex
	closeMu        sync.Mutex
	dispatchWG     sync.WaitGroup
	callbackActive atomic.Bool
	objects        map[uint32]*Proxy
	nextID         uint32
	free           []uint32
	closed         bool
	rbuf           []byte
	fds            []int
	// roundtrip is non-nil only while a Roundtrip is active. The goroutine
	// that holds dispatchMu reads and writes it.
	roundtrip *roundtripState
}

func (c *conn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// beginDispatch registers an in-flight Dispatch/Roundtrip. It is paired with
// dispatchWG.Done by the caller; Close waits on the same WaitGroup after
// closing the socket. The Add happens under c.mu so it cannot race with the
// close transition: either the Close sees the dispatch, or beginDispatch sees
// the closed connection.
func (c *conn) beginDispatch() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}
	c.dispatchWG.Add(1)
	return nil
}
