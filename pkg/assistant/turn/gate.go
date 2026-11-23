package turn

import (
	"sync"
	"time"
)

const releaseChunk = voiceRate

type gate struct {
	heard  time.Time
	phrase string
	send   func([]byte)

	mu   sync.Mutex
	open bool
	held []byte
}

func (g *gate) pass(b []byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.open {
		g.send(b)
		return
	}
	g.held = append(g.held, b...)
}

func (g *gate) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for b := g.held; len(b) > 0; {
		n := min(len(b), releaseChunk)
		g.send(b[:n])
		b = b[n:]
	}
	g.held, g.open = nil, true
}

func (g *gate) opened() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.open
}
