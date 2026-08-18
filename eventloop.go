package wayland

import (
	"context"
	"syscall"

	"github.com/xogas/wayland/wire"
)

func (c *Conn) startReader() {
	c.readerOnce.Do(func() {
		go c.readLoop()
	})
}

type readResult struct {
	obj    wire.ObjectID
	opcode uint16
	r      *wire.Reader
	err    error
}

func (c *Conn) readLoop() {
	for {
		obj, opcode, r, err := c.wc.ReceiveMessage()
		if err != nil {
			c.setReadErr(err)
			select {
			case c.readCh <- readResult{err: err}:
			case <-c.done:
			}
			return
		}
		select {
		case <-c.done:
			return
		default:
		}
		select {
		case c.readCh <- readResult{obj: obj, opcode: opcode, r: r}:
		case <-c.done:
			return
		}
	}
}

// Dispatch blocks until a single event is received and dispatched, the
// connection fails, or ctx is done. Event handlers run in the calling
// goroutine; callers should dispatch from a single goroutine so that events
// for a given object are handled in order.
func (c *Conn) Dispatch(ctx context.Context) error {
	c.startReader()
	if err := c.stickyErr(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return ErrConnClosed
	case res := <-c.readCh:
		if c.closed.Load() {
			// Buffered before Close: the object table is gone, so dispatching
			// it would misreport a stream violation. Report the close instead.
			return ErrConnClosed
		}
		if res.err != nil {
			c.setReadErr(res.err)
			return c.stickyErr()
		}
		c.dispatch(uint32(res.obj), res.opcode, res.r)
		// A handler may have turned the connection fatal (wl_display.error or
		// an event decode failure): surface it instead of dispatching more
		// events from a dead stream.
		if err := c.stickyErr(); err != nil {
			return err
		}
		return nil
	}
}

func (c *Conn) DispatchPending() error {
	if err := c.stickyErr(); err != nil {
		return err
	}
	c.startReader()
	for {
		select {
		case res := <-c.readCh:
			if c.closed.Load() {
				return ErrConnClosed
			}
			if res.err != nil {
				c.setReadErr(res.err)
				return c.stickyErr()
			}
			c.dispatch(uint32(res.obj), res.opcode, res.r)
			if err := c.stickyErr(); err != nil {
				return err
			}
		default:
			return c.stickyErr()
		}
	}
}

func (c *Conn) Flush() error {
	// NOTE: no buffer, SendMessage writes directly to the socket
	return nil
}

func (c *Conn) dispatch(objID uint32, opcode uint16, r *wire.Reader) {
	p := c.LookupProxy(objID)
	if p == nil {
		// Zombie object: destroyed by the client but still receiving
		// in-flight events. The zombie keeps the destroyed interface's event
		// table so fds can be drained and closed; the zombie entry itself is
		// removed when delete_id arrives (removeProxy).
		c.objectsMu.RLock()
		fdCounts, isZombie := c.zombies[objID]
		c.objectsMu.RUnlock()
		if !isZombie {
			// Never existed or already confirmed deleted by delete_id: the
			// protocol guarantees no further events reference it.
			c.failStream("event for unknown object", objID, opcode)
			return
		}
		if fdCounts != nil {
			n, known := fdCounts[opcode]
			if !known {
				// Unknown opcode on a destroyed interface: same
				// fd-queue-safety reason as failStream.
				c.failStream("event for unknown opcode on destroyed object", objID, opcode)
				return
			}
			if n > 0 {
				for _, fd := range c.wc.TakeFDs(n) {
					_ = syscall.Close(fd)
				}
			}
			c.Logger().Warn("receiving event for destroyed object", "id", objID, "opcode", opcode)
		}
		return
	}
	counts := p.FDCounts()
	if counts == nil {
		// No registered fd table (raw proxy for a custom protocol): fd counts
		// are unknown, so hand the event to any registered handler leniently.
		// RegisterInterfaceFDCounts restores strict fd accounting.
		if p.hasEvent(opcode) {
			p.dispatchEvent(opcode, r)
		}
		return
	}
	n, known := counts[opcode]
	if !known {
		// The bound interface does not define this opcode (version-skewed or
		// bogus event): fatal for the same fd-queue-safety reason as
		// failStream.
		c.failStream("event for unknown opcode", objID, opcode)
		return
	}
	if n > 0 {
		fds := c.wc.TakeFDs(n)
		r.SetFDs(fds)
		if !p.hasEvent(opcode) {
			for _, fd := range fds {
				_ = syscall.Close(fd)
			}
			return
		}
	} else if !p.hasEvent(opcode) {
		return
	}
	p.dispatchEvent(opcode, r)
}
