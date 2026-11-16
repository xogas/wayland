package wayland

import (
	"encoding/binary"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestDispatchSerial covers AC-D01: callbacks are serial, run on the Dispatch
// caller's goroutine, and may send requests without deadlocking.
func TestDispatchSerial(t *testing.T) {
	a, b := unixPair(t)
	d := newDisplay(a)
	t.Cleanup(func() { _ = d.Close() })
	t.Cleanup(func() { _ = b.Close() })
	iface := testInterface("s03_serial", 1,
		[]Message{{Name: "ping", Since: 1, Args: []ArgSpec{{Name: "n", Type: ArgUint}}}},
		[]Message{{Name: "pong", Since: 1, Args: []ArgSpec{{Name: "n", Type: ArgUint}}}})
	p := newTestProxy(t, d, iface, 1, nil)

	const count = 6
	got := make([]uint32, 0, count)
	var sendErr error
	var inCallback atomic.Int32
	parent := goroutineID()
	if err := p.SetSink(funcSink{fn: func(opcode uint16, args []Arg) error {
		if gid := goroutineID(); gid != parent {
			t.Errorf("callback on goroutine %d, want Dispatch caller %d", gid, parent)
		}
		if inCallback.Add(1) != 1 {
			t.Errorf("callbacks overlapped")
		}
		defer inCallback.Add(-1)
		n := args[0].Uint()
		got = append(got, n)
		if err := p.Send(0, Uint(n)); err != nil {
			sendErr = err
		}
		return nil
	}}); err != nil {
		t.Fatalf("set sink: %v", err)
	}

	for i := 0; i < count; i++ {
		writeFrame(t, b, p.ID(), 0, appendU32(nil, uint32(i)))
	}
	for i := 0; i < count; i++ {
		if err := d.Dispatch(); err != nil {
			t.Fatalf("dispatch %d: %v", i, err)
		}
	}
	if sendErr != nil {
		t.Fatalf("send from callback: %v", sendErr)
	}
	if len(got) != count {
		t.Fatalf("callbacks = %d, want %d", len(got), count)
	}
	for i, n := range got {
		if n != uint32(i) {
			t.Fatalf("callback %d = %d, order broken", i, n)
		}
	}

	srv := newTestServer(b)
	for i := 0; i < count; i++ {
		objectID, opcode, body, _ := srv.readFrame(t)
		if objectID != p.ID() || opcode != 0 {
			t.Fatalf("request %d = id %d op %d", i, objectID, opcode)
		}
		if len(body) != 4 || binary.LittleEndian.Uint32(body) != uint32(i) {
			t.Fatalf("request %d body = % x", i, body)
		}
	}
}

// TestConcurrentSend covers AC-D02: eight goroutines send concurrently,
// messages stay atomic and per-goroutine order is preserved.
func TestConcurrentSend(t *testing.T) {
	a, b := unixPair(t)
	d := newDisplay(a)
	t.Cleanup(func() { _ = d.Close() })
	t.Cleanup(func() { _ = b.Close() })
	iface := testInterface("s03_concurrent", 1, []Message{
		{Name: "msg", Since: 1, Args: []ArgSpec{{Name: "worker", Type: ArgInt}, {Name: "seq", Type: ArgInt}}},
	}, nil)
	p := newTestProxy(t, d, iface, 1, nil)

	const (
		workers = 8
		perWork = 100
	)
	total := workers * perWork

	type readResult struct {
		seqs [workers][]int32
		err  error
	}
	srv := newTestServer(b)
	readCh := make(chan readResult, 1)
	go func() {
		var res readResult
		for i := 0; i < total; i++ {
			objectID, opcode, body, _, err := srv.nextFrame()
			if err != nil {
				res.err = err
				readCh <- res
				return
			}
			if objectID != p.ID() || opcode != 0 || len(body) != 8 {
				res.err = errors.New("malformed request frame")
				readCh <- res
				return
			}
			worker := int(int32(binary.LittleEndian.Uint32(body[0:4])))
			seq := int32(binary.LittleEndian.Uint32(body[4:8]))
			if worker < 0 || worker >= workers {
				res.err = errors.New("worker index out of range")
				readCh <- res
				return
			}
			res.seqs[worker] = append(res.seqs[worker], seq)
		}
		readCh <- res
	}()

	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for seq := 0; seq < perWork; seq++ {
				if err := p.Send(0, Int(int32(worker)), Int(int32(seq))); err != nil {
					errCh <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("send: %v", err)
	}

	var res readResult
	select {
	case res = <-readCh:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not read all requests")
	}
	if res.err != nil {
		t.Fatalf("server: %v", res.err)
	}
	for worker, seqs := range res.seqs {
		if len(seqs) != perWork {
			t.Fatalf("worker %d sent %d messages, want %d", worker, len(seqs), perWork)
		}
		for i, seq := range seqs {
			if seq != int32(i) {
				t.Fatalf("worker %d order broken at %d: %d", worker, i, seq)
			}
		}
	}
}

// TestCloseUnblocksDispatch covers AC-D03.
func TestCloseUnblocksDispatch(t *testing.T) {
	t.Run("blocked_dispatch", func(t *testing.T) {
		a, b := unixPair(t)
		d := newDisplay(a)
		t.Cleanup(func() { _ = b.Close() })
		iface := testInterface("s03_close", 1, nil,
			[]Message{{Name: "e", Since: 1, Args: []ArgSpec{{Name: "n", Type: ArgUint}}}})
		_ = newTestProxy(t, d, iface, 1, funcSink{fn: func(uint16, []Arg) error { return nil }})

		dispatchErr := make(chan error, 1)
		go func() { dispatchErr <- d.Dispatch() }()
		time.Sleep(50 * time.Millisecond)
		if err := d.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		select {
		case err := <-dispatchErr:
			if !errors.Is(err, ErrClosed) {
				t.Fatalf("dispatch = %v, want ErrClosed", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("dispatch was not unblocked by Close")
		}
		if err := d.Close(); err != nil {
			t.Fatalf("second close: %v", err)
		}
	})

	t.Run("concurrent_close", func(t *testing.T) {
		a, b := unixPair(t)
		d := newDisplay(a)
		t.Cleanup(func() { _ = b.Close() })
		const goroutines = 8
		errCh := make(chan error, goroutines)
		var wg sync.WaitGroup
		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errCh <- d.Close()
			}()
		}
		wg.Wait()
		close(errCh)
		for err := range errCh {
			if err != nil {
				t.Fatalf("concurrent close: %v", err)
			}
		}
		if !d.p.conn.isClosed() {
			t.Fatal("connection not closed")
		}
	})

	t.Run("concurrent_close_during_callback", func(t *testing.T) {
		a, b := unixPair(t)
		d := newDisplay(a)
		t.Cleanup(func() { _ = b.Close() })
		iface := testInterface("s03_close_callback", 1, nil,
			[]Message{{Name: "e", Since: 1, Args: []ArgSpec{{Name: "n", Type: ArgUint}}}})
		entered := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		p := newTestProxy(t, d, iface, 1, funcSink{fn: func(uint16, []Arg) error {
			once.Do(func() { close(entered) })
			<-release
			return nil
		}})
		writeFrame(t, b, p.ID(), 0, appendU32(nil, 1))
		dispatchErr := make(chan error, 1)
		go func() { dispatchErr <- d.Dispatch() }()
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("callback did not start")
		}
		closeErr := make(chan error, 1)
		go func() { closeErr <- d.Close() }()
		select {
		case err := <-closeErr:
			if !errors.Is(err, ErrInvalidOperation) {
				t.Fatalf("close during callback = %v, want ErrInvalidOperation", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Close waited for the callback")
		}
		close(release)
		select {
		case err := <-dispatchErr:
			if err != nil {
				t.Fatalf("dispatch after close: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("dispatch did not finish")
		}
		if err := d.Close(); err != nil {
			t.Fatalf("close after callback: %v", err)
		}
	})

	t.Run("reentrant_close", func(t *testing.T) {
		a, b := unixPair(t)
		d := newDisplay(a)
		t.Cleanup(func() { _ = b.Close() })
		iface := testInterface("s03_reentrant", 1, nil,
			[]Message{{Name: "e", Since: 1, Args: []ArgSpec{{Name: "n", Type: ArgUint}}}})
		var closeErr error
		p := newTestProxy(t, d, iface, 1, funcSink{fn: func(uint16, []Arg) error {
			closeErr = d.Close()
			return nil
		}})
		writeFrame(t, b, p.ID(), 0, appendU32(nil, 1))
		if err := d.Dispatch(); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		if !errors.Is(closeErr, ErrInvalidOperation) {
			t.Fatalf("close from callback = %v, want ErrInvalidOperation", closeErr)
		}
		if !d.p.conn.isClosed() {
			t.Fatal("socket not closed by reentrant Close")
		}
		if err := d.Close(); err != nil {
			t.Fatalf("close after reentrant close: %v", err)
		}
	})
}

// TestDispatchProtocolError covers AC-D04.
func TestDispatchProtocolError(t *testing.T) {
	a, b := unixPair(t)
	d := newDisplay(a)
	t.Cleanup(func() { _ = d.Close() })
	t.Cleanup(func() { _ = b.Close() })
	iface := testInterface("s03_dispatch_error", 1, nil,
		[]Message{{Name: "e", Since: 1, Args: []ArgSpec{{Name: "n", Type: ArgUint}}}})
	var calls int
	p := newTestProxy(t, d, iface, 1, funcSink{fn: func(uint16, []Arg) error {
		calls++
		return nil
	}})

	writeFrame(t, b, p.ID(), 0, appendU32(nil, 7))
	if err := d.Dispatch(); err != nil {
		t.Fatalf("normal dispatch: %v", err)
	}
	if calls != 1 {
		t.Fatalf("callbacks = %d, want 1", calls)
	}

	msg := "boom"
	spec := mustInterface("wl_display").Events[0]
	data, _, err := encodeRequest(d.p, 0, spec, []Arg{Object(p.ID()), Uint(3), String(&msg)})
	if err != nil {
		t.Fatalf("encode display error: %v", err)
	}
	writeFrame(t, b, 1, 0, data[8:])

	err = d.Dispatch()
	var pe *ProtocolError
	if !errors.As(err, &pe) {
		t.Fatalf("dispatch = %v, want *ProtocolError", err)
	}
	if pe.ObjectID != p.ID() || pe.Interface != iface.Name || pe.Code != 3 || pe.Message != "boom" {
		t.Fatalf("protocol error = %+v", pe)
	}
	if !errors.Is(err, ErrProtocolViolation) {
		t.Fatalf("protocol error does not match ErrProtocolViolation")
	}
	if !d.p.conn.isClosed() {
		t.Fatal("connection not closed after protocol error")
	}
	if err := d.Dispatch(); !errors.Is(err, ErrClosed) {
		t.Fatalf("dispatch after error = %v, want ErrClosed", err)
	}
	if calls != 1 {
		t.Fatalf("callbacks after error = %d, want 1", calls)
	}
}

// TestConcurrentDispatch covers AC-D05.
func TestConcurrentDispatch(t *testing.T) {
	a, b := unixPair(t)
	d := newDisplay(a)
	t.Cleanup(func() { _ = d.Close() })
	iface := testInterface("s03_concurrent_dispatch", 1, nil,
		[]Message{{Name: "e", Since: 1, Args: []ArgSpec{{Name: "n", Type: ArgUint}}}})
	var active atomic.Int32
	var count atomic.Int32
	p := newTestProxy(t, d, iface, 1, funcSink{fn: func(uint16, []Arg) error {
		if active.Add(1) != 1 {
			t.Errorf("callbacks overlapped")
		}
		defer active.Add(-1)
		count.Add(1)
		time.Sleep(500 * time.Microsecond)
		return nil
	}})

	const events = 32
	for i := 0; i < events; i++ {
		writeFrame(t, b, p.ID(), 0, appendU32(nil, uint32(i)))
	}
	_ = b.Close()

	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				err := d.Dispatch()
				if err != nil {
					errCh <- err
					return
				}
			}
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent Dispatch deadlocked")
	}
	close(errCh)
	for err := range errCh {
		if !errors.Is(err, ErrClosed) && !errors.Is(err, ErrIO) {
			t.Fatalf("unexpected dispatch error: %v", err)
		}
	}
	if got := count.Load(); got != events {
		t.Fatalf("callbacks = %d, want %d", got, events)
	}
}

// TestSendBlocking covers AC-R03.
func TestSendBlocking(t *testing.T) {
	a, b := unixPair(t)
	d := newDisplay(a)
	t.Cleanup(func() { _ = d.Close() })
	t.Cleanup(func() { _ = b.Close() })
	iface := testInterface("s03_send_blocking", 1, []Message{
		{Name: "big", Since: 1, Args: []ArgSpec{{Name: "data", Type: ArgArray}}},
	}, nil)
	p := newTestProxy(t, d, iface, 1, nil)
	if err := a.SetWriteBuffer(1024); err != nil {
		t.Fatalf("set write buffer: %v", err)
	}
	payload := make([]byte, 60000)
	sendErr := make(chan error, 1)
	go func() {
		sendErr <- p.Send(0, Array(payload))
	}()
	select {
	case err := <-sendErr:
		t.Fatalf("send completed without blocking: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	closeErr := make(chan error, 1)
	go func() {
		closeErr <- d.Close()
	}()
	select {
	case err := <-sendErr:
		if !errors.Is(err, ErrClosed) && !errors.Is(err, ErrIO) {
			t.Fatalf("send after close = %v, want ErrClosed or ErrIO", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not unblock Send")
	}
	select {
	case err := <-closeErr:
		if err != nil {
			t.Fatalf("close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close hung while Send was blocked")
	}
}
