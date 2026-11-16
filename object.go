package wayland

import "fmt"

func (c *conn) lookup(id uint32) *Proxy {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.objects[id]
}

func (c *conn) newObject(iface *Interface, version uint32, serverOwned bool) (*Proxy, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrClosed
	}
	if iface == nil {
		return nil, fmt.Errorf("new object: nil interface: %w", ErrInvalidArgument)
	}
	if version < 1 || version > iface.Version {
		return nil, fmt.Errorf("new %s v%d: %w", iface.Name, version, ErrVersion)
	}
	id, err := c.allocIDLocked()
	if err != nil {
		return nil, fmt.Errorf("new %s v%d: %w", iface.Name, version, err)
	}
	p := &Proxy{
		conn:        c,
		id:          id,
		version:     version,
		iface:       iface,
		state:       stateAlive,
		serverOwned: serverOwned,
	}
	c.objects[id] = p
	return p, nil
}

func (c *conn) newIncomingObject(iface *Interface, version uint32, id uint32) (*Proxy, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrClosed
	}
	if iface == nil || id == 0 || version < 1 || version > iface.Version {
		return nil, fmt.Errorf("incoming %v id %d v%d: %w", iface, id, version, ErrProtocolViolation)
	}
	if _, exists := c.objects[id]; exists {
		return nil, fmt.Errorf("incoming object %d: %w", id, ErrProtocolViolation)
	}
	p := &Proxy{
		conn:        c,
		id:          id,
		version:     version,
		iface:       iface,
		state:       stateAlive,
		serverOwned: true,
	}
	c.objects[id] = p
	return p, nil
}

func (c *conn) allocIDLocked() (uint32, error) {
	for n := len(c.free); n > 0; n = len(c.free) {
		id := c.free[n-1]
		c.free = c.free[:n-1]
		if id >= 1 && id <= clientIDMax {
			return id, nil
		}
	}
	// ID 0 is never an object and ID 1 is reserved for wl_display.
	if c.nextID < 2 {
		c.nextID = 2
	}
	if c.nextID > clientIDMax {
		return 0, ErrIDExhausted
	}
	id := c.nextID
	c.nextID++
	return id, nil
}

func (c *conn) release(p *Proxy) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p == nil || p.conn != c {
		return fmt.Errorf("release: %w", ErrInvalidArgument)
	}
	if p.sent {
		return fmt.Errorf("release %d: %w", p.id, ErrInvalidOperation)
	}
	if p.state == stateDestroyed {
		return nil
	}
	delete(c.objects, p.id)
	if p.id >= 1 && p.id <= clientIDMax {
		c.free = append(c.free, p.id)
	}
	p.state = stateDestroyed
	return nil
}

// deleteID handles wl_display.delete_id. A live client-owned object may not be
// deleted: only destroyPending objects and server-owned objects (for example a
// wl_callback that received done) are eligible.
func (c *conn) deleteID(id uint32) error {
	c.mu.Lock()
	p, ok := c.objects[id]
	if !ok {
		c.mu.Unlock()
		// Unknown server-range children belong to an interface this client has
		// no signature for; delete_id for them is safe to ignore. Unknown
		// client-range IDs remain protocol errors.
		if id >= serverIDMin {
			return nil
		}
		return fmt.Errorf("delete_id %d: %w", id, ErrProtocolViolation)
	}
	if !p.serverOwned && p.state != stateDestroyPending {
		name := p.iface.Name
		c.mu.Unlock()
		return fmt.Errorf("delete_id %d for live %s: %w", id, name, ErrProtocolViolation)
	}
	delete(c.objects, id)
	p.state = stateDestroyed
	if id >= 1 && id <= clientIDMax {
		c.free = append(c.free, id)
	}
	c.mu.Unlock()
	return nil
}

func (c *conn) markTombstone(p *Proxy) {
	if p == nil {
		return
	}
	c.mu.Lock()
	if c.objects[p.id] == p {
		p.tombstone = true
	}
	c.mu.Unlock()
}

func (c *conn) markDestroyPending(p *Proxy) {
	if p == nil {
		return
	}
	c.mu.Lock()
	if c.objects[p.id] == p && p.state == stateAlive {
		p.state = stateDestroyPending
	}
	c.mu.Unlock()
}
