package wire

import "testing"

// An error is the only acceptable outcome: a panic here is reachable from any
// peer.
func FuzzReader(f *testing.F) {
	f.Add([]byte{0, 0, 0, 0})
	f.Add([]byte{1, 0, 0, 0, 2, 0, 0, 0, 3, 0, 0, 0})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})
	f.Add([]byte{2, 0, 0, 0, 'a', 0, 0, 0})
	f.Add([]byte{0x00, 0x00, 0x00, 0x80})
	// One seed per selector, so plain `go test` hits every decoder.
	f.Add([]byte{4, 0, 0, 0, 0})
	f.Add([]byte{5, 2, 0, 0, 0, 'a', 0, 0, 0})
	f.Add([]byte{6, 2, 0, 0, 0, 'a', 0, 0, 0})
	f.Add([]byte{7, 1, 0, 0, 0, 1, 0, 0, 0})
	f.Add([]byte{8, 0, 0, 0, 0})

	f.Fuzz(func(t *testing.T, data []byte) {
		r := NewReader(data, nil)
		for i := 0; i < len(data); i++ {
			var err error
			switch data[i] % 9 {
			case 0:
				_, err = r.Int32()
			case 1:
				_, err = r.Uint32()
			case 2:
				_, err = r.Fixed()
			case 3:
				_, err = r.Object()
			case 4:
				_, err = r.NewID()
			case 5:
				_, err = r.String()
			case 6:
				_, err = r.StringNullable()
			case 7:
				_, err = r.Array()
			case 8:
				_, err = r.Fd()
			}

			// Breaking this panics in the slice expression.
			if r.pos < 0 || r.pos > len(r.buf) {
				t.Fatalf("cursor %d outside %d-byte buffer", r.pos, len(r.buf))
			}
			if r.Remaining() < 0 {
				t.Fatalf("negative remaining: pos=%d len=%d", r.pos, len(r.buf))
			}
			if err != nil {
				break
			}
		}
	})
}

// Either asks for more bytes, rejects the header, or returns a frame contained
// in the input.
func FuzzSplitFrame(f *testing.F) {
	f.Add([]byte{1, 0, 0, 0, 8, 0, 0, 0})
	f.Add([]byte{1, 0, 0, 0, 12, 0, 0, 0, 1, 2, 3, 4})
	f.Add([]byte{1, 0, 0, 0, 9, 0, 0, 0})
	f.Add([]byte{1, 0, 0, 0})

	f.Fuzz(func(t *testing.T, data []byte) {
		buf := data
		for range len(data) + 1 {
			fr, rest, ok, err := splitFrame(buf)
			if err != nil {
				return
			}
			if !ok {
				if len(rest) != len(buf) {
					t.Fatalf("partial frame consumed %d bytes", len(buf)-len(rest))
				}
				return
			}
			if len(fr.payload)%4 != 0 {
				t.Fatalf("payload of %d bytes is not aligned", len(fr.payload))
			}
			if len(fr.payload)+8 > len(buf) {
				t.Fatalf("frame of %d bytes claimed from a %d-byte buffer", len(fr.payload)+8, len(buf))
			}
			if want := len(buf) - 8 - len(fr.payload); len(rest) != want {
				t.Fatalf("rest = %d bytes, want %d", len(rest), want)
			}
			buf = rest
		}
	})
}

// The peer closes after writing, so each run must end with an error instead of
// looping, panicking, or reading the same bytes twice.
func FuzzReceiveMessageStream(f *testing.F) {
	f.Add([]byte{1, 0, 0, 0, 8, 0, 0, 0})
	f.Add([]byte{1, 0, 0, 0, 12, 0, 0, 0, 1, 2, 3, 4, 1, 0, 0, 0, 8, 0, 0, 0})
	f.Add([]byte{1, 0, 0, 0, 9, 0, 0, 0, 0, 0, 0, 0})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64<<10 {
			t.Skip("keep the fuzzer fast")
		}
		client, server := socketPair(t)
		conn := NewConn(server)

		if _, err := client.Write(data); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := client.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}

		for range len(data) + 2 {
			if _, _, _, err := conn.ReceiveMessage(); err != nil {
				return
			}
		}
		t.Fatal("framing loop did not terminate")
	})
}
