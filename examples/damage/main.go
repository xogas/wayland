//go:build linux

// Incremental damage demonstration with a small square moving along a circular path.
package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/examples/internal/shared"
)

const (
	winW = 400
	winH = 400
	sqSz = 24
	keyD = 32
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	dpy, reg, globals, err := shared.Connect(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer dpy.Close() //nolint: errcheck

	dpy.SetOnError(func(pe *wayland.ProtocolError) {
		fmt.Fprintf(os.Stderr, "protocol error: object=%d code=%d message=%q\n", pe.ObjectID, pe.Code, pe.Message)
	})

	core, err := shared.BindCore(reg, globals)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	compVer, _ := globals.Version(wayland.InterfaceCompositor)

	useDamageBuffer := compVer >= 4
	if useDamageBuffer {
		fmt.Printf("using DamageBuffer (compositor version %d)\n", compVer)
	} else {
		fmt.Println("using Damage (surface coordinates)")
	}

	seat, err := shared.BindSeat(reg, globals)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	toplevel, err := shared.NewToplevel(ctx, dpy, core, "damage", "damagedemo", winW, winH, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	keyboard, err := seat.GetKeyboard()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get_keyboard: %v\n", err)
		os.Exit(1)
	}

	incDamage := true
	keyboard.OnKey(func(ev wayland.KeyboardKeyEvent) {
		if ev.Key == keyD && ev.State == wayland.KeyboardKeyStatePressed {
			incDamage = !incDamage
			if incDamage {
				fmt.Println("switched to incremental damage")
			} else {
				fmt.Println("switched to full damage")
			}
		}
	})

	db, err := shared.NewDoubleBuffer(core.Shm, winW, winH)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Each slot remembers where the square was drawn so the previous position
	// can be erased before drawing the new one.
	var prevX, prevY [2]int32
	for i := 0; i < 2; i++ {
		prevX[i], prevY[i] = -sqSz, -sqSz
		shared.FillRect(db.Pixels[i], int(db.Stride), winW, winH, 0, 0, winW, winH, 0x40, 0x30, 0x30)
	}

	errCh := shared.DispatchLoop(ctx, dpy)
	frames := 0
	var accumArea int64
	fullArea := int64(winW) * int64(winH)
	cx := float64(winW) * 0.5
	cy := float64(winH) * 0.5
	radius := 150.0

	for {
		select {
		case <-toplevel.Closed:
			return
		case <-ctx.Done():
			return
		case err := <-errCh:
			if err != nil {
				fmt.Fprintf(os.Stderr, "dispatch: %v\n", err)
			}
			return
		case idx := <-db.Free():
			t := float64(frames) * 0.06
			nx := int32(cx + radius*math.Cos(t) - sqSz/2)
			ny := int32(cy + radius*math.Sin(t) - sqSz/2)

			shared.FillRect(db.Pixels[idx], int(db.Stride), winW, winH,
				int(prevX[idx]), int(prevY[idx]), sqSz, sqSz, 0x40, 0x30, 0x30)
			shared.FillRect(db.Pixels[idx], int(db.Stride), winW, winH,
				int(nx), int(ny), sqSz, sqSz, 0xff, 0xcc, 0x00)

			done := make(chan struct{})
			cb, err := toplevel.Surface.Frame()
			if err != nil {
				fmt.Fprintf(os.Stderr, "frame: %v\n", err)
				return
			}
			cb.OnDone(func(ev wayland.CallbackDoneEvent) {
				close(done)
			})

			_ = toplevel.Surface.Attach(db.IDs[idx], 0, 0)

			if incDamage {
				a1 := damageRect(toplevel, useDamageBuffer, prevX[idx], prevY[idx], sqSz, sqSz)
				a2 := damageRect(toplevel, useDamageBuffer, nx, ny, sqSz, sqSz)
				accumArea += int64(a1) + int64(a2)
			} else {
				damageRect(toplevel, useDamageBuffer, 0, 0, winW, winH)
				accumArea += fullArea
			}

			_ = toplevel.Surface.Commit()

			prevX[idx] = nx
			prevY[idx] = ny

			select {
			case <-toplevel.Closed:
				return
			case <-ctx.Done():
				return
			case err := <-errCh:
				if err != nil {
					fmt.Fprintf(os.Stderr, "dispatch: %v\n", err)
				}
				return
			case <-done:
			}
			frames++

			if frames%120 == 0 {
				ratio := float64(accumArea) / float64(int64(frames)*fullArea)
				fmt.Printf("%d frames, damage/total area ratio: %.3f\n", frames, ratio)
			}
		}
	}
}

func damageRect(t *shared.Toplevel, useDB bool, x, y, w, h int32) int32 {
	if x < 0 {
		w += x
		x = 0
	}
	if y < 0 {
		h += y
		y = 0
	}
	if x+w > winW {
		w = winW - x
	}
	if y+h > winH {
		h = winH - y
	}
	if w <= 0 || h <= 0 {
		return 0
	}
	if useDB {
		_ = t.Surface.DamageBuffer(x, y, w, h)
	} else {
		_ = t.Surface.Damage(x, y, w, h)
	}
	return w * h
}
