package transcodenode

import "sync"

// gpuGate keeps an operator-triggered capability re-probe and the node's own
// GPU work off the encoder at the same time.
//
// Every hardware probe ends in a real smoke encode, which opens an encoder
// session. A card at its concurrent-session cap fails that encode with an error
// nothing can tell apart from a missing device or a broken driver, so a probe
// that races a transcode publishes a hardware regression for a GPU that is
// fine — and the API persists it, latches it, and routes the node to software
// until a clean report arrives.
//
// A point-in-time "are there active jobs" check cannot prevent that: a node
// idle at the check accepts a start milliseconds later, while the probe still
// has minutes to run. What is needed is one exclusion both sides consult, held
// from before the probe begins until after it ends.
//
// The gate is deliberately asymmetric. Work never waits: it is admitted or
// refused immediately, because a viewer pressing play must not queue behind a
// multi-minute probe. The re-probe never waits either: it refuses with 409 and
// tells the operator to retry when the node is idle, because blocking would
// hold an admin HTTP connection open for the length of a stream.
type gpuGate struct {
	mu sync.Mutex
	// workers counts GPU work that has been admitted and has not finished. It
	// is separate from Server.activeJobs because that counter is incremented
	// only once ffmpeg is already running: the window this gate exists to close
	// is precisely the one between admitting work and it becoming visible
	// there.
	workers int
	// reprobing is set for the whole capability rebuild, including the cache
	// invalidation that precedes it.
	reprobing bool
}

// beginWork admits one unit of GPU work, or reports false while a re-probe
// holds the encoder. A caller that is admitted must call endWork exactly once.
func (g *gpuGate) beginWork() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.reprobing {
		return false
	}
	g.workers++
	return true
}

// endWork releases one unit of admitted GPU work.
func (g *gpuGate) endWork() {
	g.mu.Lock()
	if g.workers > 0 {
		g.workers--
	}
	g.mu.Unlock()
}

// beginReprobe claims the encoder exclusively, or reports the work in progress
// that stopped it. activeJobs is the node's own running-session count, passed in
// so both halves of "is this node busy" are read under one lock rather than
// sampled at two different instants.
func (g *gpuGate) beginReprobe(activeJobs int) (busy int, ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	busy = g.workers + activeJobs
	if g.reprobing || busy > 0 {
		return busy, false
	}
	g.reprobing = true
	return 0, true
}

// endReprobe releases the exclusive claim.
func (g *gpuGate) endReprobe() {
	g.mu.Lock()
	g.reprobing = false
	g.mu.Unlock()
}
