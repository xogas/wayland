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
		if res.err != nil {
			c.setReadErr(res.err)
			return c.stickyErr()
		}
		c.dispatch(uint32(res.obj), res.opcode, res.r)
		// A handler may have turned the connection fatal (wl_display.error
		// or an event decode failure): surface that error now instead of
		// dispatching more events from a dead stream.
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
		// in-flight events. Drain and close any fds they carry; the zombie
		// entry itself is removed when delete_id arrives (removeProxy).
		c.objectsMu.RLock()
		n, isZombie := c.zombies[objID][opcode]
		c.objectsMu.RUnlock()
		if !isZombie {
			return
		}
		if n > 0 {
			for _, fd := range c.wc.TakeFDs(n) {
				_ = syscall.Close(fd)
			}
		}
		c.connMu.Lock()
		logger := c.logger
		c.connMu.Unlock()
		logger.Warn("receiving event for unknown object", "id", objID, "opcode", opcode)
		return
	}
	n := p.fdCountForOpcode(opcode)
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
