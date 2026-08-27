package nodemetrics

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

// cgroup CPU correction.
//
// /proc/stat describes the host even inside a container, exactly as
// /proc/meminfo does. A transcode node limited to two cores on a 64-core host
// would otherwise report the host's busyness against the host's core count —
// pinned at its quota and dropping segments while the dashboard shows a few
// percent idle. So CPU is corrected the same way memory is: the cgroup's own
// cumulative usage is the busy signal, and its quota is what that usage is
// normalized against.
//
// A host with no cgroup limit reads its root cgroup, which accounts for every
// process on the machine, so an unconstrained deployment reports what it always
// did.

// cgroupCPUPath locates one cgroup version's CPU accounting.
type cgroupCPUPath struct {
	// usage is the file holding cumulative CPU time consumed by the cgroup.
	usage string
	// usageKey names the row to read when usage is a "key value" table; empty
	// when the file holds a bare integer.
	usageKey string
	// usageUnit is how long one unit in that file is.
	usageUnit time.Duration
	// quota holds the CPU budget: cgroup v2's "<quota|max> <period>" pair, or
	// cgroup v1's quota alone.
	quota string
	// period is v1's separate period file; empty when quota carries both.
	period string
}

// cgroupCPUUsageKey names the cumulative-usage row of cgroup v2's cpu.stat.
const cgroupCPUUsageKey = "usage_usec"

// cgroupCPUPaths lists where to read CPU accounting, v2 first. cgroup v1 mounts
// cpu and cpuacct together on most distributions and separately on some, so
// both layouts are tried.
var cgroupCPUPaths = []cgroupCPUPath{
	{
		usage:     "/sys/fs/cgroup/cpu.stat",
		usageKey:  cgroupCPUUsageKey,
		usageUnit: time.Microsecond,
		quota:     "/sys/fs/cgroup/cpu.max",
	},
	{
		usage:     "/sys/fs/cgroup/cpu,cpuacct/cpuacct.usage",
		usageUnit: time.Nanosecond,
		quota:     "/sys/fs/cgroup/cpu,cpuacct/cpu.cfs_quota_us",
		period:    "/sys/fs/cgroup/cpu,cpuacct/cpu.cfs_period_us",
	},
	{
		usage:     "/sys/fs/cgroup/cpuacct/cpuacct.usage",
		usageUnit: time.Nanosecond,
		quota:     "/sys/fs/cgroup/cpu/cpu.cfs_quota_us",
		period:    "/sys/fs/cgroup/cpu/cpu.cfs_period_us",
	},
}

// cgroupCPUSample is one cumulative CPU-time reading. Only differences between
// two readings mean anything.
type cgroupCPUSample struct {
	usageNS int64
	at      time.Time
	valid   bool
}

// cgroupCPU returns this process's cgroup CPU reading for the given instant,
// and the CPU budget in cores that reading must be normalized against (0 when
// the cgroup imposes no quota).
func (s *Sampler) cgroupCPU(now time.Time) (cgroupCPUSample, float64) {
	for _, paths := range s.cgroupCPUPaths {
		usage, err := readCgroupCPUUsage(paths)
		if err != nil {
			continue
		}
		quota, err := readCgroupCPUQuota(paths)
		if err != nil {
			quota = 0
		}
		return cgroupCPUSample{usageNS: usage, at: now, valid: true}, quota
	}
	return cgroupCPUSample{}, 0
}

// readCgroupCPUUsage returns cumulative cgroup CPU time in nanoseconds.
func readCgroupCPUUsage(paths cgroupCPUPath) (int64, error) {
	var value int64
	var err error
	if paths.usageKey != "" {
		value, err = readCgroupStatKey(paths.usage, paths.usageKey)
	} else {
		value, err = readCgroupSingleValue(paths.usage)
	}
	if err != nil {
		return 0, err
	}
	if value < 0 {
		return 0, fmt.Errorf("negative cgroup cpu usage")
	}
	return value * int64(paths.usageUnit), nil
}

// readCgroupCPUQuota returns the cgroup's CPU budget in cores, or an error when
// it imposes none.
//
// "No quota" is spelled "max" in v2 and "-1" in v1, and both must read as "this
// cgroup may use the whole host", never as a budget of zero cores.
func readCgroupCPUQuota(paths cgroupCPUPath) (float64, error) {
	raw, err := os.ReadFile(paths.quota)
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return 0, fmt.Errorf("empty cgroup cpu quota")
	}
	if fields[0] == "max" {
		return 0, fmt.Errorf("no cgroup cpu quota")
	}
	quota, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, err
	}
	if quota <= 0 {
		return 0, fmt.Errorf("no cgroup cpu quota")
	}

	period := int64(0)
	if len(fields) > 1 {
		// cgroup v2 prints the period beside the quota.
		period, err = strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, err
		}
	} else if paths.period != "" {
		period, err = readCgroupSingleValue(paths.period)
		if err != nil {
			return 0, err
		}
	}
	if period <= 0 {
		return 0, fmt.Errorf("no cgroup cpu period")
	}
	return float64(quota) / float64(period), nil
}

// cgroupCPUPercent converts two cgroup CPU readings into a busy percentage of
// the given core budget.
//
// A usage counter that went backwards means the readings do not describe one
// continuous run (the container was restarted or migrated), so the pair is
// unusable rather than negative.
func cgroupCPUPercent(previous, current cgroupCPUSample, cores float64) (int, bool) {
	if !previous.valid || !current.valid || cores <= 0 {
		return 0, false
	}
	elapsedNS := current.at.Sub(previous.at).Nanoseconds()
	if elapsedNS <= 0 || current.usageNS < previous.usageNS {
		return 0, false
	}
	busy := float64(current.usageNS-previous.usageNS) * 100 / (float64(elapsedNS) * cores)
	return clampPercent(int(busy + 0.5)), true
}

// cgroupQuotaCores rounds a fractional CPU quota up to whole cores, which is
// how many CPUs the workload can actually be running on at one instant.
func cgroupQuotaCores(quota float64, hostCores int) int {
	cores := int(math.Ceil(quota))
	if cores < 1 {
		cores = 1
	}
	if hostCores > 0 && cores > hostCores {
		// A quota above the host's core count is not a limit worth reporting.
		return hostCores
	}
	return cores
}
