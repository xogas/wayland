package wayland

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"time"
)

type roundtripHandler struct {
	done chan struct{}
}

func (h *roundtripHandler) Done(uint32) {
	select {
	case h.done <- struct{}{}:
	default:
	}
}

// roundtripState is the per-Roundtrip state that conn used to hold permanently.
// conn references it only while a Roundtrip is active; the goroutine that holds
// dispatchMu reads and writes it, while the cancellation hook reads canceled.
type roundtripState struct {
	callback *Proxy
	uc       *net.UnixConn
	// softTimeout marks the read deadline the cancellation hook installs as a
	// soft timeout rather than a fatal IO error.
	softTimeout bool
	// canceled is set by the context.AfterFunc goroutine and read by
	// dispatchOne, so it is atomic across those goroutines.
	canceled atomic.Bool
}

func (s *roundtripState) markTombstone(c *conn) {
	c.markTombstone(s.callback)
}

// cancel marks the callback before waking the reader so an event that is
// already in flight is discarded rather than delivered, and flags the
// cancellation so a frame that arrives after this point is unread for the next
// Dispatch.
func (s *roundtripState) cancel(c *conn) {
	s.markTombstone(c)
	s.canceled.Store(true)
	_ = s.uc.SetReadDeadline(time.Now())
}

// unread puts a frame consumed after cancellation back at the front of the read
// buffer so the next Dispatch can deliver it.
func (s *roundtripState) unread(c *conn, objectID uint32, opcode uint16, body []byte) {
	c.unreadFrame(objectID, opcode, body)
}

// Roundtrip sends wl_display.sync and dispatches until the callback is done.
// Cancellation wakes a blocked read with a read deadline; the deadline is a
// soft timeout that leaves the connection usable.
func (d *Display) Roundtrip(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("roundtrip: nil context: %w", ErrInvalidArgument)
	}
	c := d.p.conn
	if err := c.beginDispatch(); err != nil {
		return fmt.Errorf("roundtrip: %w", err)
	}
	defer c.dispatchWG.Done()
	c.dispatchMu.Lock()
	defer c.dispatchMu.Unlock()
	if c.isClosed() {
		return fmt.Errorf("roundtrip: %w", ErrClosed)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	done := make(chan struct{}, 1)
	cb, err := d.Sync(&roundtripHandler{done: done})
	if err != nil {
		return err
	}

	// Install the cancellation hook before the first blocking read. The
	// callback sets a deadline; readMore turns that deadline into
	// errSoftTimeout instead of closing the connection.
	st := &roundtripState{callback: cb.Proxy(), uc: c.uc, softTimeout: true}
	c.roundtrip = st
	cancelRead := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		st.cancel(c)
		close(cancelRead)
	})
	defer func() {
		if !stop() {
			<-cancelRead
		}
		st.canceled.Store(false)
		_ = st.uc.SetReadDeadline(time.Time{})
		st.softTimeout = false
		c.roundtrip = nil
	}()

	for {
		if err := ctx.Err(); err != nil {
			st.markTombstone(c)
			return err
		}
		err := d.dispatchOne()
		if errors.Is(err, errSoftTimeout) {
			st.markTombstone(c)
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return fmt.Errorf("roundtrip: %w", ErrIO)
		}
		if err != nil {
			return err
		}
		select {
		case <-done:
			return nil
		default:
		}
	}
}
