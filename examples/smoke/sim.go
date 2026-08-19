package main

import (
	"math"
	"math/rand"
)

// Simulation parameters of the Jos Stam stable-fluids solver.
const (
	simW       = 200
	simH       = 200
	simScale   = winW / simW
	simCells   = simW * simH
	diffuseN   = 2 // Gauss-Seidel iterations per diffuse step
	projectN   = 4 // Gauss-Seidel iterations per projection step
	simDt      = 0.12
	simVisc    = 0.000001
	simDiff    = 0.000001
	autoPeriod = 15 // frames between random smoke injections
)

// simIX flattens a 2D grid coordinate.
func simIX(x, y int) int { return x + y*simW }

// clamp bounds v to [lo, hi].
func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Sim is the fluid state: density plus the two velocity components, each
// with a scratch copy, plus the pressure and divergence fields and a buffer
// for the sources of the current step.
type Sim struct {
	dens, u, v          []float64
	dens0, u0, v0       []float64
	p, div              []float64
	srcDens, srcU, srcV []float64
	autoTick            int
}

func newSim() *Sim {
	n := simCells
	return &Sim{
		dens:    make([]float64, n),
		u:       make([]float64, n),
		v:       make([]float64, n),
		dens0:   make([]float64, n),
		u0:      make([]float64, n),
		v0:      make([]float64, n),
		p:       make([]float64, n),
		div:     make([]float64, n),
		srcDens: make([]float64, n),
		srcU:    make([]float64, n),
		srcV:    make([]float64, n),
	}
}

// setBnd mirrors the field at the walls: b==0 is a free boundary, b==1
// reflects the x-velocity, b==2 reflects the y-velocity.
func (s *Sim) setBnd(b int, x []float64) {
	for i := 1; i < simW-1; i++ {
		if b == 1 {
			x[simIX(0, i)] = -x[simIX(1, i)]
			x[simIX(simW-1, i)] = -x[simIX(simW-2, i)]
		} else {
			x[simIX(0, i)] = x[simIX(1, i)]
			x[simIX(simW-1, i)] = x[simIX(simW-2, i)]
		}
	}
	for i := 1; i < simH-1; i++ {
		if b == 2 {
			x[simIX(i, 0)] = -x[simIX(i, 1)]
			x[simIX(i, simH-1)] = -x[simIX(i, simH-2)]
		} else {
			x[simIX(i, 0)] = x[simIX(i, 1)]
			x[simIX(i, simH-1)] = x[simIX(i, simH-2)]
		}
	}
	x[simIX(0, 0)] = 0.5 * (x[simIX(1, 0)] + x[simIX(0, 1)])
	x[simIX(0, simH-1)] = 0.5 * (x[simIX(1, simH-1)] + x[simIX(0, simH-2)])
	x[simIX(simW-1, 0)] = 0.5 * (x[simIX(simW-2, 0)] + x[simIX(simW-1, 1)])
	x[simIX(simW-1, simH-1)] = 0.5 * (x[simIX(simW-2, simH-1)] + x[simIX(simW-1, simH-2)])
}

// addSource accumulates the sources of one step into the field.
func (s *Sim) addSource(x, s0 []float64, dt float64) {
	for i := range x {
		x[i] += dt * s0[i]
	}
}

// diffuse solves the diffusion equation by Gauss-Seidel relaxation.
func (s *Sim) diffuse(b int, x, x0 []float64, diff, dt float64) {
	a := dt * diff * float64(simW*simH)
	for range diffuseN {
		for i := 1; i < simW-1; i++ {
			for j := 1; j < simH-1; j++ {
				idx := simIX(i, j)
				x[idx] = (x0[idx] + a*(x[simIX(i-1, j)]+x[simIX(i+1, j)]+x[simIX(i, j-1)]+x[simIX(i, j+1)])) / (1 + 4*a)
			}
		}
		s.setBnd(b, x)
	}
}

// advect moves the field along the velocity, with bilinear interpolation.
func (s *Sim) advect(b int, d, d0, u, v []float64, dt float64) {
	dt0 := dt * float64(simW)
	for i := 1; i < simW-1; i++ {
		for j := 1; j < simH-1; j++ {
			idx := simIX(i, j)
			x := clamp(float64(i)-dt0*u[idx], 0.5, float64(simW)-1.5)
			y := clamp(float64(j)-dt0*v[idx], 0.5, float64(simH)-1.5)
			i0 := int(x)
			j0 := int(y)
			s1 := x - float64(i0)
			s0 := 1.0 - s1
			t1 := y - float64(j0)
			t0 := 1.0 - t1
			d[idx] = s0*(t0*d0[simIX(i0, j0)]+t1*d0[simIX(i0, j0+1)]) +
				s1*(t0*d0[simIX(i0+1, j0)]+t1*d0[simIX(i0+1, j0+1)])
		}
	}
	s.setBnd(b, d)
}

// project enforces incompressibility by subtracting the pressure gradient.
func (s *Sim) project(u, v, p, div []float64) {
	h := 1.0 / float64(simW)
	for i := 1; i < simW-1; i++ {
		for j := 1; j < simH-1; j++ {
			idx := simIX(i, j)
			div[idx] = -0.5 * h * (u[simIX(i+1, j)] - u[simIX(i-1, j)] + v[simIX(i, j+1)] - v[simIX(i, j-1)])
			p[idx] = 0
		}
	}
	s.setBnd(0, div)
	s.setBnd(0, p)
	for range projectN {
		for i := 1; i < simW-1; i++ {
			for j := 1; j < simH-1; j++ {
				idx := simIX(i, j)
				p[idx] = (div[idx] + p[simIX(i-1, j)] + p[simIX(i+1, j)] + p[simIX(i, j-1)] + p[simIX(i, j+1)]) / 4.0
			}
		}
		s.setBnd(0, p)
	}
	for i := 1; i < simW-1; i++ {
		for j := 1; j < simH-1; j++ {
			idx := simIX(i, j)
			u[idx] -= 0.5 * (p[simIX(i+1, j)] - p[simIX(i-1, j)]) / h
			v[idx] -= 0.5 * (p[simIX(i, j+1)] - p[simIX(i, j-1)]) / h
		}
	}
	s.setBnd(1, u)
	s.setBnd(2, v)
}

// step advances the simulation by dt: add sources, diffuse, project, advect,
// then inject a random smoke cloud every autoPeriod frames.
func (s *Sim) step(dt float64) {
	s.addSource(s.u, s.srcU, dt)
	s.addSource(s.v, s.srcV, dt)
	clear(s.srcU)
	clear(s.srcV)

	copy(s.u0, s.u)
	copy(s.v0, s.v)
	s.diffuse(1, s.u, s.u0, simVisc, dt)
	s.diffuse(2, s.v, s.v0, simVisc, dt)
	s.project(s.u, s.v, s.p, s.div)

	copy(s.u0, s.u)
	copy(s.v0, s.v)
	s.advect(1, s.u, s.u0, s.u0, s.v0, dt)
	s.advect(2, s.v, s.v0, s.u0, s.v0, dt)
	s.project(s.u, s.v, s.p, s.div)

	s.addSource(s.dens, s.srcDens, dt)
	clear(s.srcDens)
	copy(s.dens0, s.dens)
	s.diffuse(0, s.dens, s.dens0, simDiff, dt)
	copy(s.dens0, s.dens)
	s.advect(0, s.dens, s.dens0, s.u, s.v, dt)

	s.autoTick++
	if s.autoTick >= autoPeriod {
		s.autoTick = 0
		s.injectRandom()
	}
}

// injectMotion adds smoke and velocity around (px, py), scaled by the
// pointer movement delta.
func (s *Sim) injectMotion(px, py int, dx, dy float64) {
	r := 4
	spd := math.Sqrt(dx*dx+dy*dy) * 3.0
	for dy2 := -r; dy2 <= r; dy2++ {
		for dx2 := -r; dx2 <= r; dx2++ {
			nx := px + dx2
			ny := py + dy2
			if nx < 0 || nx >= simW || ny < 0 || ny >= simH {
				continue
			}
			f := math.Exp(-float64(dx2*dx2+dy2*dy2) / 12.0)
			idx := simIX(nx, ny)
			s.srcDens[idx] += f * spd * 3
			s.srcU[idx] += dx * f * 4
			s.srcV[idx] += dy * f * 4
		}
	}
}

// injectRandom spawns a random smoke cloud with a random velocity.
func (s *Sim) injectRandom() {
	cx := rand.Intn(simW/2) + simW/4
	cy := rand.Intn(simH/2) + simH/4
	rr := 5 + rand.Intn(18)
	spd := 2.0 + rand.Float64()*3.0
	u0 := (rand.Float64() - 0.5) * 80
	v0 := (rand.Float64() - 0.5) * 80
	rsq := float64(rr*rr) / 3.0
	for dy := -rr; dy <= rr; dy++ {
		for dx := -rr; dx <= rr; dx++ {
			nx := cx + dx
			ny := cy + dy
			if nx < 0 || nx >= simW || ny < 0 || ny >= simH {
				continue
			}
			f := math.Exp(-float64(dx*dx+dy*dy) / rsq)
			idx := simIX(nx, ny)
			s.srcDens[idx] += f * spd
			s.srcU[idx] += u0 * f * 0.5
			s.srcV[idx] += v0 * f * 0.5
		}
	}
}
