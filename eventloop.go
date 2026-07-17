package wayland

import (
	"context"
	"syscall"

	"github.com/xogas/wayland/wire"
)

func (c *Conn) startReader() {
	c.readerOnce.Do(func() {
		c.readCh = make(chan readResult, 16)
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
		select {
		case c.readCh <- readResult{obj, opcode, r, err}:
		case <-c.done:
			return
		}
		if err != nil {
			return
		}
	}
}

func (c *Conn) Dispatch(ctx context.Context) error {
	c.startReader()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return ErrConnClosed
	case res := <-c.readCh:
		if res.err != nil {
			if c.closed.Load() {
				return ErrConnClosed
			}
			return res.err
		}
		c.dispatch(uint32(res.obj), res.opcode, res.r)
		return nil
	}
}

func (c *Conn) DispatchPending() error {
	if c.closed.Load() {
		return ErrConnClosed
	}
	c.startReader()
	for {
		select {
		case res := <-c.readCh:
			if res.err != nil {
				if c.closed.Load() {
					return ErrConnClosed
				}
				return res.err
			}
			c.dispatch(uint32(res.obj), res.opcode, res.r)
		default:
			return nil
		}
	}
}

func (c *Conn) Flush() error {
	return nil
}

func (c *Conn) dispatch(objID uint32, opcode uint16, r *wire.Reader) {
	p := c.LookupProxy(objID)
	if p == nil {
		c.objectsMu.Lock()
		zombieFdCounts, isZombie := c.zombies[objID]
		var n int
		if isZombie {
			n = zombieFdCounts[opcode]
			delete(zombieFdCounts, opcode)
			if len(zombieFdCounts) == 0 {
				delete(c.zombies, objID)
			}
		}
		c.objectsMu.Unlock()
		if isZombie {
			if n > 0 {
				fds := c.wc.TakeFDs(n)
				for _, fd := range fds {
					_ = syscall.Close(fd)
				}
			}
			c.connMu.Lock()
			logger := c.logger
			c.connMu.Unlock()
			logger.Warn("receiving event for unknown object", "id", objID, "opcode", opcode)
		}
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
