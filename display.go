package wayland

import (
	"context"
	"fmt"
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
		return ErrClosed
	}
	return d.dispatchOne()
}

// Roundtrip sends wl_display.sync and dispatches until the callback is done.
func (d *Display) Roundtrip(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("roundtrip: nil context: %w", ErrInvalidArgument)
	}
	c := d.p.conn
	c.dispatchMu.Lock()
	defer c.dispatchMu.Unlock()
	if c.isClosed() {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	done := make(chan struct{}, 1)
	if _, err := d.Sync(&roundtripHandler{done: done}); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := d.dispatchOne(); err != nil {
			return err
		}
		select {
		case <-done:
			return nil
		default:
		}
	}
}

// Close closes the connection.
func (d *Display) Close() error {
	return d.p.conn.close()
}

func (d *Display) dispatchOne() error {
	c := d.p.conn
	objectID, opcode, body, err := c.readFrame()
	if err != nil {
		if c.isClosed() {
			return ErrClosed
		}
		_ = c.close()
		return err
	}
	if objectID == 1 {
		return d.handleDisplayEvent(opcode, body)
	}
	p := c.lookup(objectID)
	if p == nil {
		_ = c.close()
		return fmt.Errorf("event object %d: %w", objectID, ErrProtocolViolation)
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
	if p.sink == nil {
		return fmt.Errorf("event %s.%s: %w", p.iface.Name, spec.Name, ErrNoHandler)
	}
	if err := p.sink.DispatchEvent(opcode, args); err != nil {
		_ = c.close()
		return fmt.Errorf("dispatch %s.%s: %w", p.iface.Name, spec.Name, err)
	}
	return nil
}

func (d *Display) handleDisplayEvent(opcode uint16, body []byte) error {
	c := d.p.conn
	iface := mustInterface("wl_display")
	if int(opcode) >= len(iface.Events) {
		_ = c.close()
		return fmt.Errorf("wl_display opcode %d: %w", opcode, ErrProtocolViolation)
	}
	spec := iface.Events[opcode]
	args, err := c.decodeArgs(body, spec.Args, d.p)
	if err != nil {
		_ = c.close()
		return err
	}
	switch opcode {
	case 0:
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
