//go:build linux

// Incremental damage demonstration: a small square moves along a circular
// path and only the dirty rectangles are redrawn on the wire.
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
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	dpy, reg, globals, err := shared.Connect(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = dpy.Close() }()

	core, err := shared.BindCore(reg, globals)
	if err != nil {
		return err
	}

	// wl_surface.damage_buffer exists since wl_surface v4 and uses buffer
	// coordinates (matching the scale), which is what we want.
	compVer, _ := globals.Version(wayland.InterfaceCompositor)
	useDamageBuffer := compVer >= 4
	if useDamageBuffer {
		fmt.Printf("using damage_buffer (compositor version %d)\n", compVer)
	} else {
		fmt.Println("using damage (surface coordinates)")
	}

	seat, err := shared.BindSeat(reg, globals)
	if err != nil {
		return err
	}

	toplevel, err := shared.NewToplevel(ctx, dpy, core, "damage", "damagedemo", winW, winH, nil)
	if err != nil {
		return err
	}

	keyboard, err := seat.GetKeyboard()
	if err != nil {
		return fmt.Errorf("get keyboard: %w", err)
	}

	// D toggles between incremental and full-window damage.
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
		return err
	}
	defer db.Close()

	// Each slot remembers where its square is drawn so the previous position
	// can be erased and damaged before drawing the new one.
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
			return nil
		case <-ctx.Done():
			return nil
		case err := <-errCh:
			return err
		case idx := <-db.Free():
			t := float64(frames) * 0.06
			nx := int32(cx + radius*math.Cos(t) - sqSz/2)
			ny := int32(cy + radius*math.Sin(t) - sqSz/2)

			// Erase the old position, draw the new one.
			shared.FillRect(db.Pixels[idx], int(db.Stride), winW, winH,
				int(prevX[idx]), int(prevY[idx]), sqSz, sqSz, 0x40, 0x30, 0x30)
			shared.FillRect(db.Pixels[idx], int(db.Stride), winW, winH,
				int(nx), int(ny), sqSz, sqSz, 0xff, 0xcc, 0x00)

			done, err := shared.Frame(toplevel.Surface)
			if err != nil {
				return fmt.Errorf("frame: %w", err)
			}

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
			case <-done:
			case <-toplevel.Closed:
				return nil
			case <-ctx.Done():
				return nil
			case err := <-errCh:
				return err
			}
			frames++

			if frames%120 == 0 {
				ratio := float64(accumArea) / float64(int64(frames)*fullArea)
				fmt.Printf("%d frames, damage/total area ratio: %.3f\n", frames, ratio)
			}
		}
	}
}

// damageRect marks a rectangle dirty, clipped to the window, and returns its
// area for the statistics. It uses damage_buffer when available.
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
