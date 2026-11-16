package wayland

import "syscall"

// Close closes the connection. It closes the socket first, then waits for an
// in-flight Dispatch or Roundtrip to exit. Calling Close from a callback is an
// invalid reentrant operation and returns ErrInvalidOperation without waiting.
func (d *Display) Close() error {
	c := d.p.conn
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if c.callbackActive.Load() {
		// Close from a callback, or concurrent with one, is illegal. Close
		// the socket so the dispatch loop stops, but never wait on the
		// dispatchMu held by the callback.
		_ = c.close()
		return ErrInvalidOperation
	}
	var err error
	if !c.isClosed() {
		err = c.close()
	}
	// Even a repeat Close waits for a dispatch that is still unwinding.
	c.dispatchWG.Wait()
	return err
}

// close tears the socket down without waiting for Dispatch. Display.Close waits;
// error paths call this directly while dispatchMu may already be held.
func (c *conn) close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	uc := c.uc
	fds := c.fds
	c.fds = nil
	c.mu.Unlock()

	var err error
	if uc != nil {
		err = uc.Close()
	}
	closeFDs(fds)
	return err
}

func closeFDs(fds []int) {
	for _, fd := range fds {
		if fd >= 0 {
			_ = syscall.Close(fd)
		}
	}
}

func closeArgFDs(args []Arg) {
	for _, arg := range args {
		if arg.typ == ArgFD && arg.fd >= 0 {
			_ = syscall.Close(arg.fd)
		}
	}
}

func closeOOBFds(oob []byte) {
	if len(oob) == 0 {
		return
	}
	msgs, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return
	}
	for i := range msgs {
		rights, err := syscall.ParseUnixRights(&msgs[i])
		if err != nil {
			continue
		}
		closeFDs(rights)
	}
}
