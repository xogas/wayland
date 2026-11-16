package wayland

import (
	"errors"
	"fmt"
)

// Dispatch reads and dispatches one wire message.
func (d *Display) Dispatch() error {
	c := d.p.conn
	if err := c.beginDispatch(); err != nil {
		return fmt.Errorf("dispatch: %w", err)
	}
	defer c.dispatchWG.Done()
	c.dispatchMu.Lock()
	defer c.dispatchMu.Unlock()
	if c.isClosed() {
		return fmt.Errorf("dispatch: %w", ErrClosed)
	}
	return d.dispatchOne()
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
	if st := c.roundtrip; st != nil && st.canceled.Load() {
		st.unread(c, objectID, opcode, body)
		return errSoftTimeout
	}
	if objectID == 1 {
		return d.handleDisplayEvent(opcode, body)
	}
	p := c.lookup(objectID)
	if p == nil {
		// A child object created by a raw-bound unknown interface has no
		// signature, so the test-only hook consumes the already-read frame.
		// Public dispatch still reports E-07.
		if c.testSkipUnknownServer(objectID) {
			return nil
		}
		_ = c.close()
		return fmt.Errorf("event object %d: %w", objectID, ErrProtocolViolation)
	}
	// Test-only raw bind: there is no generated signature for this
	// interface, so the event body cannot be decoded. The frame was already
	// consumed by readFrame, which makes skipping by message length safe for
	// the connection. Descriptors carried by such an event cannot be
	// identified without a signature; the runtime documents this limitation
	// and tests avoid fd events on raw objects.
	if c.testRawName(objectID) != "" {
		c.testNoteSkipped(objectID)
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
		return fmt.Errorf("dispatch %s.%s: %w: %w", p.iface.Name, spec.Name, ErrProtocolViolation, err)
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
