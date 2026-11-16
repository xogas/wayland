package wayland

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"
)

type countSink struct {
	n int
}

func (s *countSink) DispatchEvent(uint16, []Arg) error {
	s.n++
	return nil
}

// TestRoundtripCancel covers roundtrip cancellation: a canceled or timed-out Roundtrip returns
// the context error, leaves the connection usable, suppresses the canceled
// callback and keeps a tombstone until delete_id.
func TestRoundtripCancel(t *testing.T) {
	a, b := unixPair(t)
	d := newDisplay(a)
	t.Cleanup(func() { _ = d.Close() })
	t.Cleanup(func() { _ = b.Close() })
	srv := newTestServer(b)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	firstErr := make(chan error, 1)
	go func() { firstErr <- d.Roundtrip(ctx) }()

	// The first Roundtrip must have sent wl_display.sync.
	_, opcode, body, _ := srv.readFrame(t)
	if opcode != 0 || len(body) != 4 {
		t.Fatalf("sync request = opcode %d body %d", opcode, len(body))
	}
	cbID := binary.LittleEndian.Uint32(body[0:4])
	if cbID == 0 {
		t.Fatal("sync new_id is 0")
	}

	select {
	case err := <-firstErr:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("roundtrip = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("roundtrip did not return after context timeout")
	}
	if d.p.conn.isClosed() {
		t.Fatal("connection closed by canceled Roundtrip")
	}

	// Replace the internal handler so a leaked delivery is observable.
	p := d.p.conn.lookup(cbID)
	if p == nil {
		t.Fatal("canceled callback was removed before delete_id")
	}
	rec := &countSink{}
	d.p.conn.mu.Lock()
	p.sink = rec
	d.p.conn.mu.Unlock()

	// The server's late done must be consumed silently, not delivered.
	writeFrame(t, b, cbID, 0, appendU32(nil, 0))
	if err := d.Dispatch(); err != nil {
		t.Fatalf("dispatch canceled callback: %v", err)
	}
	if rec.n != 0 {
		t.Fatalf("canceled callback delivered %d times", rec.n)
	}
	if d.p.conn.lookup(cbID) == nil {
		t.Fatal("tombstone removed before delete_id")
	}

	writeFrame(t, b, 1, 1, appendU32(nil, cbID))
	if err := d.Dispatch(); err != nil {
		t.Fatalf("dispatch delete_id: %v", err)
	}
	if d.p.conn.lookup(cbID) != nil {
		t.Fatal("tombstone survived delete_id")
	}
	if d.p.conn.isClosed() {
		t.Fatal("connection closed while clearing tombstone")
	}

	// The connection remains usable for a later Roundtrip; this also proves
	// the soft read deadline was reset.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	secondErr := make(chan error, 1)
	go func() { secondErr <- d.Roundtrip(ctx2) }()
	_, _, body, _ = srv.readFrame(t)
	cbID2 := binary.LittleEndian.Uint32(body[0:4])
	writeFrame(t, b, cbID2, 0, appendU32(nil, 9))
	select {
	case err := <-secondErr:
		if err != nil {
			t.Fatalf("second roundtrip: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second roundtrip hung")
	}

	// An already-canceled context returns immediately and does not consume the
	// connection.
	ctx3, cancel3 := context.WithCancel(context.Background())
	cancel3()
	if err := d.Roundtrip(ctx3); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled roundtrip = %v, want context.Canceled", err)
	}
	if d.p.conn.isClosed() {
		t.Fatal("connection closed by pre-canceled Roundtrip")
	}
}

// TestRoundtripCancelPreservesEvents checks that a cancellation that races with
// an unrelated event leaves that event for the next Dispatch.
func TestRoundtripCancelPreservesEvents(t *testing.T) {
	a, b := unixPair(t)
	d := newDisplay(a)
	t.Cleanup(func() { _ = d.Close() })
	t.Cleanup(func() { _ = b.Close() })
	iface := testInterface("test_preserve", 1, nil,
		[]Message{{Name: "e", Since: 1, Args: []ArgSpec{{Name: "n", Type: ArgUint}}}})
	var calls int
	p := newTestProxy(t, d, iface, 1, funcSink{fn: func(uint16, []Arg) error {
		calls++
		return nil
	}})
	writeFrame(t, b, p.ID(), 0, appendU32(nil, 1))

	// Simulate the window between a canceled Roundtrip reading a frame and
	// returning: the frame must be put back, not delivered.
	c := d.p.conn
	st := &roundtripState{softTimeout: true}
	st.canceled.Store(true)
	c.roundtrip = st
	if err := d.Dispatch(); !errors.Is(err, errSoftTimeout) {
		t.Fatalf("dispatch during cancellation = %v, want errSoftTimeout", err)
	}
	c.roundtrip = nil
	if calls != 0 {
		t.Fatalf("event delivered during cancellation: %d", calls)
	}
	if err := d.Dispatch(); err != nil {
		t.Fatalf("dispatch after cancellation: %v", err)
	}
	if calls != 1 {
		t.Fatalf("event delivered %d times, want 1", calls)
	}
}
