package nodemetrics

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// nvidiaSMITimeout bounds one query. A wedged driver makes nvidia-smi hang
// indefinitely, and a metrics sample must never inherit that.
const nvidiaSMITimeout = 3 * time.Second

// sourceFailureLimit is how many consecutive failures retire an enrichment
// source for the life of the process.
//
// A source that has failed five times running is not having a bad moment: the
// binary is missing its driver, the container lacks the device, or the query
// syntax is not supported by the installed version. Retrying it forever would
// spawn a doomed subprocess every 5 seconds on every node, which costs more
// than the signal is worth. Recovery is a node restart, which is also what
// installing or fixing the toolkit requires.
const sourceFailureLimit = 5

// nvidiaSMIFields is the query column order. utilization.gpu is whole-GPU
// busyness including other tenants; the encoder/decoder columns are the
// fixed-function video engines, which is what a transcode node is actually
// competing for.
const nvidiaSMIFields = "index,uuid,pci.bus_id,utilization.gpu,utilization.encoder,utilization.decoder,memory.used,memory.total"

// runNVIDIASMI is the execution seam. Tests replace it instead of installing a
// fake binary on PATH.
var runNVIDIASMI = func(ctx context.Context) ([]byte, error) {
	path, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return nil, err
	}
	return exec.CommandContext(ctx, path,
		"--query-gpu="+nvidiaSMIFields,
		"--format=csv,noheader,nounits").Output()
}

// nvidiaGPU is one parsed nvidia-smi row.
//
// The measurement columns are optional because a successful row can still be
// only partly measurable: a driver reports "[N/A]" or "[Not Supported]" per
// column for engines or memory it cannot see, and coercing those to zero would
// publish an unobservable video engine as idle and unsupported VRAM as 0 bytes
// under an "nvidia-smi" source that claims they were measured.
type nvidiaGPU struct {
	Index       int
	UUID        string
	PCIAddress  string
	GPUUtil     *int
	EncoderUtil *int
	DecoderUtil *int
	MemUsedMB   *int64
	MemTotalMB  *int64
}

// videoUtil is the higher of the two fixed-function video engines, or nil when
// the driver reported neither.
func (g nvidiaGPU) videoUtil() *int {
	switch {
	case g.EncoderUtil != nil && g.DecoderUtil != nil:
		return ptr(max(*g.EncoderUtil, *g.DecoderUtil))
	case g.EncoderUtil != nil:
		return g.EncoderUtil
	default:
		return g.DecoderUtil
	}
}

func ptr[T any](value T) *T { return &value }

// sourceBreaker retires an enrichment source after repeated failure.
type sourceBreaker struct {
	name     string
	failures int
	tripped  bool
	logOnce  sync.Once
}

// allow reports whether the source may be queried.
func (b *sourceBreaker) allow() bool { return !b.tripped }

func (b *sourceBreaker) succeeded() { b.failures = 0 }

// failed records one failure and trips the breaker at the limit, logging the
// retirement exactly once so a node without the toolkit does not narrate it
// every interval.
func (b *sourceBreaker) failed(err error) {
	if b.tripped {
		return
	}
	b.failures++
	if b.failures < sourceFailureLimit {
		return
	}
	b.tripped = true
	b.logOnce.Do(func() {
		slog.Info("node metrics source unavailable; not retrying until restart",
			"component", "nodemetrics", "source", b.name, "failures", b.failures, "error", err)
	})
}

// queryNVIDIA runs one bounded nvidia-smi query, honoring the breaker.
func (s *Sampler) queryNVIDIA(ctx context.Context) []nvidiaGPU {
	if !s.nvidiaBreaker.allow() {
		return nil
	}
	queryCtx, cancel := context.WithTimeout(ctx, nvidiaSMITimeout)
	defer cancel()
	output, err := s.runNVIDIASMI(queryCtx)
	if err != nil {
		s.nvidiaBreaker.failed(err)
		return nil
	}
	gpus := parseNVIDIASMI(output)
	if len(gpus) == 0 {
		// A successful command that says nothing is as useless as a failure and
		// is how a stale query syntax presents.
		s.nvidiaBreaker.failed(errNoNVIDIARows)
		return nil
	}
	s.nvidiaBreaker.succeeded()
	return gpus
}

// errNoNVIDIARows marks a query that succeeded but produced nothing parseable.
var errNoNVIDIARows = errors.New("nvidia-smi returned no parseable rows")

// parseNVIDIASMI reads "csv,noheader,nounits" rows. Malformed rows are skipped
// individually; a driver that reports "[N/A]" for one column on one GPU must
// not cost the reading for the others.
func parseNVIDIASMI(output []byte) []nvidiaGPU {
	var gpus []nvidiaGPU
	for line := range strings.Lines(string(output)) {
		fields := strings.Split(line, ",")
		if len(fields) < 8 {
			continue
		}
		index, err := strconv.Atoi(strings.TrimSpace(fields[0]))
		if err != nil {
			continue
		}
		address := NormalizePCIAddress(fields[2])
		uuid := strings.TrimSpace(fields[1])
		if address == "" && uuid == "" {
			continue
		}
		gpus = append(gpus, nvidiaGPU{
			Index:       index,
			UUID:        uuid,
			PCIAddress:  address,
			GPUUtil:     parseNVIDIAInt(fields[3]),
			EncoderUtil: parseNVIDIAInt(fields[4]),
			DecoderUtil: parseNVIDIAInt(fields[5]),
			MemUsedMB:   parseNVIDIAInt64(fields[6]),
			MemTotalMB:  parseNVIDIAInt64(fields[7]),
		})
	}
	return gpus
}

// parseNVIDIAInt reads one numeric column, or nil for the driver's "[N/A]" and
// "[Not Supported]" placeholders — which say the value was not measured, not
// that it is zero.
func parseNVIDIAInt(field string) *int {
	value, err := strconv.Atoi(strings.TrimSpace(field))
	if err != nil || value < 0 {
		return nil
	}
	return &value
}

func parseNVIDIAInt64(field string) *int64 {
	value := parseNVIDIAInt(field)
	if value == nil {
		return nil
	}
	return ptr(int64(*value))
}
