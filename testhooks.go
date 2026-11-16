package wayland

// testHooks carries state that only tests need. It is nil for every production
// path, so the public runtime never enables the raw-bind or unknown-event skip
// behavior.
type testHooks struct {
	// rawNames marks objects created by the test-only raw bind path; their
	// events are skipped by length because no generated signature exists.
	rawNames map[uint32]string
	// skippedEvents counts raw-object events consumed by the length-based
	// skip path.
	skippedEvents map[uint32]uint32
	// skipUnknownServer enables the broad skip for unregistered server-range
	// IDs created by an unknown interface.
	skipUnknownServer bool
	// unknownServerSkips counts events and delete_id messages consumed by the
	// broad skip.
	unknownServerSkips int
	// oobLen overrides the ancillary buffer size; zero uses the default.
	oobLen int
}

func newTestHooks() *testHooks {
	return &testHooks{
		rawNames:      make(map[uint32]string),
		skippedEvents: make(map[uint32]uint32),
	}
}

// testInstallHooks returns the conn hooks, creating them on first use. Test
// code calls this directly; production paths leave conn.hooks nil.
func (c *conn) testInstallHooks() *testHooks {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hooks == nil {
		c.hooks = newTestHooks()
	}
	return c.hooks
}

func (c *conn) testSetRawName(id uint32, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hooks == nil {
		c.hooks = newTestHooks()
	}
	c.hooks.rawNames[id] = name
}

// testRawName returns the raw-bind name recorded for id, or "" when the object
// is not a test-only raw object.
func (c *conn) testRawName(id uint32) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hooks == nil {
		return ""
	}
	return c.hooks.rawNames[id]
}

// testNoteSkipped counts one length-based skip for a raw object.
func (c *conn) testNoteSkipped(id uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hooks == nil {
		return
	}
	c.hooks.skippedEvents[id]++
}

func (c *conn) testSkippedEventCount(id uint32) uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hooks == nil {
		return 0
	}
	return c.hooks.skippedEvents[id]
}

func (c *conn) testSetSkipUnknownServer(on bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hooks == nil {
		c.hooks = newTestHooks()
	}
	c.hooks.skipUnknownServer = on
}

// testSkipUnknownServer reports whether the broad test-only skip consumes an
// event or delete_id for id, counting it when it does.
func (c *conn) testSkipUnknownServer(id uint32) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hooks != nil && c.hooks.skipUnknownServer && id >= serverIDMin {
		c.hooks.unknownServerSkips++
		return true
	}
	return false
}

func (c *conn) testUnknownServerSkips() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hooks == nil {
		return 0
	}
	return c.hooks.unknownServerSkips
}

func (c *conn) testSetOOBLen(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hooks == nil {
		c.hooks = newTestHooks()
	}
	c.hooks.oobLen = n
}

func (c *conn) testOOBLen() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hooks == nil {
		return 0
	}
	return c.hooks.oobLen
}
