package wayland

import (
	"context"
	"errors"
	"fmt"
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

// Dispatch reads and dispatches one wire message.
func (d *Display) Dispatch() error {
	c := d.p.conn
	c.dispatchMu.Lock()
	defer c.dispatchMu.Unlock()
	if c.isClosed() {
		return fmt.Errorf("dispatch: %w", ErrClosed)
	}
	return d.dispatchOne()
}

// Roundtrip sends wl_display.sync and dispatches until the callback is done.
// Cancellation wakes a blocked read with a read deadline; the deadline is a
// soft timeout that leaves the connection usable.
func (d *Display) Roundtrip(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("roundtrip: nil context: %w", ErrInvalidArgument)
	}
	c := d.p.conn
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
	c.softTimeout = true
	c.roundtripCanceled.Store(false)
	cancelRead := make(chan struct{})
	uc := c.uc
	stop := context.AfterFunc(ctx, func() {
		// Mark the callback before waking the reader so an event that is
		// already in flight is discarded rather than delivered, and flag
		// the cancellation so a frame that arrives after this point is
		// unread for the next Dispatch.
		c.markTombstone(cb.Proxy())
		c.roundtripCanceled.Store(true)
		_ = uc.SetReadDeadline(time.Now())
		close(cancelRead)
	})
	defer func() {
		if !stop() {
			<-cancelRead
		}
		c.roundtripCanceled.Store(false)
		_ = uc.SetReadDeadline(time.Time{})
		c.softTimeout = false
	}()

	for {
		if err := ctx.Err(); err != nil {
			c.markTombstone(cb.Proxy())
			return err
		}
		err := d.dispatchOne()
		if errors.Is(err, errSoftTimeout) {
			c.markTombstone(cb.Proxy())
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
	c.dispatchMu.Lock()
	c.dispatchMu.Unlock()
	return err
}

func (d *Display) dispatchOne() error {
	c := d.p.conn
	objectID, opcode, body, err := c.readFrame()
	if err != nil {
		if errors.Is(err, errSoftTimeout) {
			return err
		}
		if c.isClosed() {
			return fmt.Errorf("dispatch: %w", ErrClosed)
		}
		_ = c.close()
		return err
	}
	if c.roundtripCanceled.Load() {
		c.unreadFrame(objectID, opcode, body)
		return errSoftTimeout
	}
	if objectID == 1 {
		return d.handleDisplayEvent(opcode, body)
	}
	p := c.lookup(objectID)
	if p == nil {
		c.mu.Lock()
		skip := c.skipUnknownServer && objectID >= 0xFF000000
		if skip {
			c.unknownServerSkips++
		}
		c.mu.Unlock()
		if skip {
			// A child object created by a raw-bound unknown interface. Its
			// interface signature is unavailable, so the already-consumed
			// frame is skipped by length. This test-only path is enabled by
			// bindUnknownRaw; public dispatch still reports E-07.
			return nil
		}
		_ = c.close()
		return fmt.Errorf("event object %d: %w", objectID, ErrProtocolViolation)
	}
	c.mu.Lock()
	rawName := p.rawName
	c.mu.Unlock()
	if rawName != "" {
		// Test-only raw bind: there is no generated signature for this
		// interface, so the event body cannot be decoded. The frame was
		// already consumed by readFrame, which makes skipping by message
		// length safe for the connection. Descriptors carried by such an
		// event cannot be identified without a signature; the runtime
		// documents this limitation and tests avoid fd events on raw
		// objects.
		c.mu.Lock()
		p.skippedEvents++
		c.mu.Unlock()
		return nil
	}
	if p.iface == nil {
		_ = c.close()
		return fmt.Errorf("event object %d: nil interface: %w", objectID, ErrProtocolViolation)
	}
	if int(opcode) >= len(p.iface.Events) {
		_ = c.close()
		return fmt.Errorf("event %s opcode %d: %w", p.iface.Name, opcode, ErrProtocolViolation)
	}
	spec := p.iface.Events[opcode]
	if spec.Since > p.version {
		_ = c.close()
		return fmt.Errorf("event %s.%s since %d version %d: %w", p.iface.Name, spec.Name, spec.Since, p.version, ErrProtocolViolation)
	}
	args, err := c.decodeArgs(body, spec.Args, p)
	if err != nil {
		_ = c.close()
		return err
	}

	c.mu.Lock()
	tombstone := p.tombstone
	state := p.state
	sink := p.sink
	c.mu.Unlock()

	// destroyPending events and canceled Roundtrip tombstones are consumed
	// explicitly; any received descriptors belong to the discarded event and
	// are closed by the library.
	if tombstone || state == stateDestroyPending {
		closeArgFDs(args)
		if spec.Destructor {
			c.markDestroyPending(p)
		}
		return nil
	}
	if sink == nil {
		closeArgFDs(args)
		return fmt.Errorf("event %s.%s: %w", p.iface.Name, spec.Name, ErrNoHandler)
	}
	if err := c.deliver(sink, opcode, args); err != nil {
		_ = c.close()
		return fmt.Errorf("dispatch %s.%s: %w: %v", p.iface.Name, spec.Name, ErrProtocolViolation, err)
	}
	if spec.Destructor {
		c.markDestroyPending(p)
	}
	return nil
}

// deliver marks the user sink as active for the duration of the callback so
// Close can detect reentrant or concurrent use without waiting on dispatchMu.
func (c *conn) deliver(sink EventSink, opcode uint16, args []Arg) (err error) {
	c.callbackActive.Store(true)
	defer c.callbackActive.Store(false)
	return sink.DispatchEvent(opcode, args)
}

func (d *Display) handleDisplayEvent(opcode uint16, body []byte) error {
	c := d.p.conn
	iface := mustInterface("wl_display")
	if int(opcode) >= len(iface.Events) {
		_ = c.close()
		return fmt.Errorf("wl_display opcode %d: %w", opcode, ErrProtocolViolation)
	}
	spec := iface.Events[opcode]
	switch opcode {
	case 0:
		// A fatal error may reference an object that is already gone, or
		// object id 0 for a global failure. Decode the id as a plain uint so
		// the error still surfaces as E-05 with its fields intact.
		specs := append([]ArgSpec(nil), spec.Args...)
		specs[0].Type = ArgUint
		args, err := c.decodeArgs(body, specs, d.p)
		if err != nil {
			_ = c.close()
			return err
		}
		if len(args) != 3 {
			_ = c.close()
			return fmt.Errorf("wl_display.error args: %w", ErrMalformedMessage)
		}
		pe := &ProtocolError{
			ObjectID: args[0].Uint(),
			Code:     args[1].Uint(),
			Message:  args[2].String(),
		}
		if p := c.lookup(pe.ObjectID); p != nil {
			pe.Interface = p.iface.Name
		}
		_ = c.close()
		return pe
	case 1:
		args, err := c.decodeArgs(body, spec.Args, d.p)
		if err != nil {
			_ = c.close()
			return err
		}
		if len(args) != 1 {
			_ = c.close()
			return fmt.Errorf("wl_display.delete_id args: %w", ErrMalformedMessage)
		}
		if err := c.deleteID(args[0].Uint()); err != nil {
			_ = c.close()
			return err
		}
		return nil
	default:
		_ = c.close()
		return fmt.Errorf("wl_display opcode %d: %w", opcode, ErrProtocolViolation)
	}
}
