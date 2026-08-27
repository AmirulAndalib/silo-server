package nodemetrics

import (
	"strconv"
	"time"
)

// maxSampledDisks caps how many mounts a snapshot reports. A deployment can
// have dozens of library roots, and a health response that grows with the
// library count would eventually be the reason health requests are slow.
const maxSampledDisks = 8

// diskProbeTimeout is how long a probe may be outstanding before the entry it
// belongs to is reported stale. It is not a cancellation: statfs(2) on a dead
// NFS server is uninterruptible, so the goroutine stays parked until the mount
// recovers or the process exits. What the timeout bounds is how long a reader
// is told numbers are current.
const diskProbeTimeout = 5 * time.Second

// fsStats is one filesystem's capacity, in the portable shape this package
// needs from statfs(2).
type fsStats struct {
	UsedBytes  uint64
	TotalBytes uint64
	// FSID identifies the filesystem itself, so two paths on one volume — the
	// common case where scratch and media live on the same disk — are reported
	// once instead of twice with identical numbers. It is empty when the
	// filesystem publishes no usable id (FUSE mounts do not), in which case each
	// path is reported separately rather than collapsed onto a shared non-id.
	FSID string
}

// diskEntry is one path's probe state. Probes run detached from the sample
// loop, so this holds the last good answer for readers to fall back to.
type diskEntry struct {
	path string
	// inFlight is what keeps a permanently stuck mount from accumulating one
	// parked goroutine per sample. A path already being probed is skipped
	// entirely; there is never more than one goroutine per path.
	inFlight    bool
	startedAt   time.Time
	haveGood    bool
	good        fsStats
	goodAt      time.Time
	lastErr     bool
	unreachable bool
}

// stale reports whether this entry's last good numbers should be flagged as
// carried over. Either the current probe has outlived its budget — the wedged
// network mount case — or no probe has landed for longer than one full sampling
// cycle plus that budget.
func (e *diskEntry) stale(now time.Time, interval time.Duration) bool {
	if e.lastErr {
		return true
	}
	if e.inFlight && now.Sub(e.startedAt) > diskProbeTimeout {
		return true
	}
	return now.Sub(e.goodAt) > interval+diskProbeTimeout
}

// refreshDisks starts a probe for every path that is not already being probed
// and returns immediately. It never waits for a result: the caller is the
// sample loop, and the whole point of this package is that one bad mount cannot
// delay a node's health answer.
func (s *Sampler) refreshDisks(paths []string, now time.Time) {
	s.diskMu.Lock()
	defer s.diskMu.Unlock()

	wanted := make(map[string]bool, len(paths))
	for _, path := range paths {
		if path != "" {
			wanted[path] = true
		}
	}
	s.pruneDisksLocked(wanted)

	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		entry := s.disks[path]
		if entry == nil {
			entry = &diskEntry{path: path}
			s.disks[path] = entry
			s.diskOrder = append(s.diskOrder, path)
		}
		if entry.inFlight {
			continue
		}
		entry.inFlight = true
		entry.startedAt = now
		go s.probeDisk(entry)
	}
}

// pruneDisksLocked forgets paths no longer offered, so a server whose libraries
// churn over months does not accumulate an entry per path ever configured.
// An entry with a probe still parked is kept: dropping it would let the next
// sample start a second goroutine against the same wedged mount, which is
// exactly what the in-flight guard exists to prevent.
// Callers must hold diskMu.
func (s *Sampler) pruneDisksLocked(wanted map[string]bool) {
	kept := s.diskOrder[:0]
	for _, path := range s.diskOrder {
		entry := s.disks[path]
		if wanted[path] || (entry != nil && entry.inFlight) {
			kept = append(kept, path)
			continue
		}
		delete(s.disks, path)
	}
	s.diskOrder = kept
}

// probeDisk runs one statfs and records the outcome. It runs on its own
// goroutine and may never return; that is expected and is why nothing waits on
// it.
func (s *Sampler) probeDisk(entry *diskEntry) {
	stats, err := s.statfs(entry.path)

	s.diskMu.Lock()
	entry.inFlight = false
	if err != nil {
		entry.lastErr = true
		// A path that has never been measured and just failed is not a mount
		// this node can see at all — a media root that exists on another node,
		// or a scratch dir that has not been created yet.
		entry.unreachable = !entry.haveGood
	} else {
		entry.lastErr = false
		entry.unreachable = false
		entry.haveGood = true
		entry.good = stats
		entry.goodAt = s.now()
	}
	s.diskMu.Unlock()

	if s.diskProbeDone != nil {
		s.diskProbeDone <- entry.path
	}
}

// diskStats reports the latest known state of every tracked path, in the order
// the paths were first offered — scratch dir first, since that is the volume a
// full disk breaks first — deduplicated by filesystem and capped.
func (s *Sampler) diskStats(paths []string, now time.Time) []DiskStats {
	s.diskMu.Lock()
	defer s.diskMu.Unlock()

	wanted := make(map[string]bool, len(paths))
	for _, path := range paths {
		wanted[path] = true
	}

	out := make([]DiskStats, 0, min(len(s.diskOrder), maxSampledDisks))
	seenFS := make(map[string]bool, len(s.diskOrder))
	libraries := 0
	for _, path := range s.diskOrder {
		if !wanted[path] {
			continue
		}
		entry := s.disks[path]
		if entry == nil {
			continue
		}
		scratch := s.scratchDir != "" && path == s.scratchDir
		// The role is assigned before the measurability check, so the index
		// belongs to the mount rather than to its luck this pass. Numbering
		// only the measurable ones would slide every library root up a place
		// the moment one went unavailable, and a Prometheus alert keyed on
		// library-1 would silently follow a different volume.
		role := ScratchDiskRole
		if !scratch {
			libraries++
			role = "library-" + strconv.Itoa(libraries)
		}
		if !entry.haveGood {
			// Report it rather than hiding it: a media root this node cannot
			// see is a deployment fact an operator needs, and silently dropping
			// it looks identical to the path not being configured.
			out = append(out, DiskStats{Path: path, Role: role, Unavailable: true, Scratch: scratch})
		} else {
			if entry.good.FSID != "" {
				if seenFS[entry.good.FSID] {
					continue
				}
				seenFS[entry.good.FSID] = true
			}
			out = append(out, DiskStats{
				Path:    path,
				Role:    role,
				UsedGB:  bytesToGB(entry.good.UsedBytes),
				TotalGB: bytesToGB(entry.good.TotalBytes),
				Stale:   entry.stale(now, s.interval),
				Scratch: scratch,
			})
		}
		// The cap applies to every entry, measured or not. A host whose library
		// roots all live on other nodes produces nothing but unavailable
		// entries, and those grow with the library count just as measured ones
		// would.
		if len(out) >= maxSampledDisks {
			break
		}
	}
	return out
}

// formatFSID renders a statfs f_fsid, or "" when the filesystem published none.
//
// A zero f_fsid is not an identity: the FUSE protocol has no fsid field at all,
// so every rclone, mergerfs and s3fs mount reports zero — and those are exactly
// the mounts a media server uses as library roots. Formatting that as "0:0"
// would make two unrelated mounts look like one filesystem, and the second one
// would be silently dropped from the disk panel, from Prometheus, and from the
// fullest-mount warning. A mount at 98% nobody can see is worse than a
// duplicated row.
func formatFSID(a, b int64) string {
	if a == 0 && b == 0 {
		return ""
	}
	return strconv.FormatInt(a, 16) + ":" + strconv.FormatInt(b, 16)
}

// bytesToGB converts to gibibytes with two decimals kept, which is the
// precision a capacity readout is read at.
func bytesToGB(value uint64) float64 {
	const bytesPerGB = float64(1024 * 1024 * 1024)
	gb := float64(value) / bytesPerGB
	return float64(int64(gb*100+0.5)) / 100
}
