package nodemetrics

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// writeSelfCgroup lays down a <procDir>/self/cgroup with the given body.
func writeSelfCgroup(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "self"), 0o755); err != nil {
		t.Fatalf("create self dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "self", "cgroup"), []byte(body), 0o644); err != nil {
		t.Fatalf("write self/cgroup: %v", err)
	}
	return dir
}

func TestCgroupRelativePaths(t *testing.T) {
	tests := []struct {
		name string
		body string
		want map[string]string
	}{
		{
			// A systemd unit with CPUQuota= or MemoryMax=. Its limits live
			// below the mount root, so the root files describe the machine.
			name: "systemd unit on cgroup v2",
			body: "0::/system.slice/silo.service\n",
			want: map[string]string{"": "system.slice/silo.service"},
		},
		{
			// v1 names its controllers, and the mount directory is named with
			// the whole comma-joined field, so both forms have to resolve.
			name: "cgroup v1 controllers",
			body: "9:cpu,cpuacct:/system.slice/silo.service\n5:memory:/system.slice/silo.service\n",
			want: map[string]string{
				"cpu,cpuacct": "system.slice/silo.service",
				"cpu":         "system.slice/silo.service",
				"cpuacct":     "system.slice/silo.service",
				"memory":      "system.slice/silo.service",
			},
		},
		{
			// A namespaced container is already at its own root, which is
			// exactly what the unrewritten paths read.
			name: "namespaced container",
			body: "0::/\n",
			want: nil,
		},
		{
			name: "unreadable lines are skipped",
			body: "garbage\n0::/system.slice/silo.service\n",
			want: map[string]string{"": "system.slice/silo.service"},
		},
		{
			// The path field may contain colons; only the first two separators
			// are structural.
			name: "path containing a colon",
			body: "0::/system.slice/silo:one.service\n",
			want: map[string]string{"": "system.slice/silo:one.service"},
		},
		{name: "empty file", body: "", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cgroupRelativePaths(writeSelfCgroup(t, tt.body))
			if len(got) != len(tt.want) {
				t.Fatalf("cgroupRelativePaths() = %v, want %v", got, tt.want)
			}
			for key, value := range tt.want {
				if got[key] != value {
					t.Fatalf("cgroupRelativePaths()[%q] = %q, want %q", key, got[key], value)
				}
			}
		})
	}
}

// A missing file is the ordinary case on a host that is not Linux, or one whose
// /proc is not where we looked. It must read as "no rewrite", never as an error
// that costs the root reading too.
func TestCgroupRelativePathsWithoutTheFile(t *testing.T) {
	if got := cgroupRelativePaths(t.TempDir()); got != nil {
		t.Fatalf("cgroupRelativePaths() = %v, want none when /self/cgroup is absent", got)
	}
}

func TestCgroupSelfFile(t *testing.T) {
	v2 := map[string]string{"": "system.slice/silo.service"}
	v1 := map[string]string{"cpu,cpuacct": "system.slice/silo.service", "memory": "system.slice/silo.service"}

	tests := []struct {
		name     string
		relative map[string]string
		file     string
		want     string
	}{
		{
			name: "v2 file sits at the mount root", relative: v2,
			file: cgroupCPUPaths[0].quota,
			want: "/sys/fs/cgroup/system.slice/silo.service/cpu.max",
		},
		{
			name: "v1 file sits under its controller", relative: v1,
			file: "/sys/fs/cgroup/cpu,cpuacct/cpuacct.usage",
			want: "/sys/fs/cgroup/cpu,cpuacct/system.slice/silo.service/cpuacct.usage",
		},
		{
			// v1 memory under v2 membership: no unified entry names it, so the
			// root path stands rather than being rewritten into nonsense.
			name: "controller this process has no membership for", relative: v2,
			file: "/sys/fs/cgroup/memory/memory.stat",
			want: "",
		},
		{name: "no membership at all", relative: nil, file: cgroupCPUPaths[0].quota, want: ""},
		{name: "path outside the cgroup mount", relative: v2, file: "/proc/stat", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cgroupSelfFile(tt.relative, tt.file); got != tt.want {
				t.Fatalf("cgroupSelfFile() = %q, want %q", got, tt.want)
			}
		})
	}
}

// The rewritten path goes first and the root stays behind it, so a container
// whose /proc names a host path it cannot open still falls through to the read
// that has always worked.
func TestWithCgroupSelfPathsKeepsTheRootFallback(t *testing.T) {
	relative := map[string]string{"": "system.slice/silo.service", "memory": "system.slice/silo.service"}
	got := withCgroupSelfPaths(relative, CgroupMemoryLimitPaths())
	// Every cgroup between this process and the root, then the root path the
	// list started with. The intermediate levels are the point: a leaf that says
	// "max" inside a slice that does not is exactly the case being covered.
	want := []string{
		"/sys/fs/cgroup/system.slice/silo.service/memory.max",
		"/sys/fs/cgroup/system.slice/memory.max",
		"/sys/fs/cgroup/memory.max",
		"/sys/fs/cgroup/memory/system.slice/silo.service/memory.limit_in_bytes",
		"/sys/fs/cgroup/memory/system.slice/memory.limit_in_bytes",
		"/sys/fs/cgroup/memory/memory.limit_in_bytes",
		"/sys/fs/cgroup/memory.limit_in_bytes",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("withCgroupSelfPaths() =\n%v\nwant\n%v", got, want)
	}

	// With no membership the list is exactly what it was.
	if got := withCgroupSelfPaths(nil, CgroupMemoryLimitPaths()); !slices.Equal(got, CgroupMemoryLimitPaths()) {
		t.Fatalf("withCgroupSelfPaths(nil) = %v, want the original list", got)
	}
}

// A CPU layout names several files that have to move together: this process's
// usage normalized against the root's quota would divide the service's own CPU
// time by the whole machine's budget.
func TestWithCgroupSelfCPUPathsRewritesEveryFileOrNone(t *testing.T) {
	relative := map[string]string{"": "system.slice/silo.service"}
	got := withCgroupSelfCPUPaths(relative, cgroupCPUPaths)

	if len(got) != len(cgroupCPUPaths)+1 {
		t.Fatalf("got %d layouts, want only the v2 one rewritten alongside the originals", len(got))
	}
	own := got[0]
	if own.usage != "/sys/fs/cgroup/system.slice/silo.service/cpu.stat" {
		t.Fatalf("usage = %q, want this process's own cpu.stat", own.usage)
	}
	if own.quota != "/sys/fs/cgroup/system.slice/silo.service/cpu.max" {
		t.Fatalf("quota = %q, want this process's own cpu.max", own.quota)
	}
	if got[1].usage != cgroupCPUPaths[0].usage {
		t.Fatalf("got[1].usage = %q, want the root layout kept behind it", got[1].usage)
	}
	// The v1 layouts have no unified membership to resolve, so they are carried
	// through unrewritten rather than half-rewritten.
	for _, layout := range got[1:] {
		if layout.usage != "" && layout.quota == "" {
			t.Fatalf("layout %+v has a usage file with no quota file", layout)
		}
	}
}

func TestWithCgroupSelfUsagePathsRewritesEveryFileOrNone(t *testing.T) {
	relative := map[string]string{"memory": "system.slice/silo.service"}
	got := withCgroupSelfUsagePaths(relative, cgroupMemoryUsagePaths)

	if len(got) != len(cgroupMemoryUsagePaths)+1 {
		t.Fatalf("got %d layouts, want only the v1 one rewritten alongside the originals", len(got))
	}
	var rewritten *cgroupUsagePath
	for i, layout := range got {
		if layout.usage == "/sys/fs/cgroup/memory/system.slice/silo.service/memory.usage_in_bytes" {
			rewritten = &got[i]
		}
	}
	if rewritten == nil {
		t.Fatalf("no rewritten v1 layout in %+v", got)
	}
	if rewritten.stat != "/sys/fs/cgroup/memory/system.slice/silo.service/memory.stat" {
		t.Fatalf("stat = %q, want it rewritten beside its usage file", rewritten.stat)
	}
	if rewritten.inactiveFile != cgroupInactiveFileKeyV1 {
		t.Fatalf("inactiveFile = %q, want the layout's own key preserved", rewritten.inactiveFile)
	}
}

// A limit is not always written where the process sits: a systemd unit inherits
// its quota from the slice containing it, and a container from its pod cgroup.
// The leaf reads "max" and the kernel throttles anyway, so a walk that stops at
// the leaf reports the whole host to a process that has two cores.
func TestCgroupAncestorPaths(t *testing.T) {
	got := cgroupAncestorPaths("/sys/fs/cgroup/kubepods/burstable/podabc/container1/cpu.max")
	want := []string{
		"/sys/fs/cgroup/kubepods/burstable/podabc/container1/cpu.max",
		"/sys/fs/cgroup/kubepods/burstable/podabc/cpu.max",
		"/sys/fs/cgroup/kubepods/burstable/cpu.max",
		"/sys/fs/cgroup/kubepods/cpu.max",
		cgroupCPUPaths[0].quota,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("cgroupAncestorPaths() =\n%v\nwant\n%v", got, want)
	}

	// The mount root itself has nowhere to climb to.
	if got := cgroupAncestorPaths(cgroupCPUPaths[0].quota); !slices.Equal(got, []string{cgroupCPUPaths[0].quota}) {
		t.Fatalf("cgroupAncestorPaths(root) = %v, want just the file", got)
	}
	// A path outside the hierarchy still reads itself: a test harness pointing
	// these at a temp directory has no ancestors, and must not lose its file.
	if got := cgroupAncestorPaths("/tmp/fake/cpu.max"); !slices.Equal(got, []string{"/tmp/fake/cpu.max"}) {
		t.Fatalf("cgroupAncestorPaths(outside) = %v, want just the file", got)
	}
	if got := cgroupAncestorPaths(""); got != nil {
		t.Fatalf("cgroupAncestorPaths(\"\") = %v, want none", got)
	}
}

// The quota a process is throttled against is the tightest anywhere above it,
// and it has to be paired with the period from the same cgroup — a quota from
// one level over a period from another describes no real budget.
func TestEffectiveCgroupCPUQuotaTakesTheTightestAncestor(t *testing.T) {
	root := t.TempDir()
	leaf := filepath.Join(root, "system.slice", "silo.service")
	slice := filepath.Join(root, "system.slice")
	for _, dir := range []string{leaf, slice} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	write := func(dir, name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s/%s: %v", dir, name, err)
		}
	}

	// cgroupAncestorPaths only climbs inside the real cgroup mount, so the walk
	// is exercised here by handing effectiveCgroupCPUQuota each level directly.
	quotaAt := func(dir string) float64 {
		return effectiveCgroupCPUQuota(cgroupCPUPath{quota: filepath.Join(dir, "cpu.max")})
	}

	// The service says "max" while its slice allows two cores.
	write(leaf, "cpu.max", "max 100000\n")
	write(slice, "cpu.max", "200000 100000\n")
	if got := quotaAt(leaf); got != 0 {
		t.Fatalf("leaf alone = %v cores, want 0 — it imposes none", got)
	}
	if got := quotaAt(slice); got != 2 {
		t.Fatalf("slice = %v cores, want 2", got)
	}

	// A leaf tighter than its slice wins, and a looser one loses.
	write(leaf, "cpu.max", "100000 100000\n")
	if got := effectiveCgroupCPUQuota(cgroupCPUPath{quota: filepath.Join(leaf, "cpu.max")}); got != 1 {
		t.Fatalf("tighter leaf = %v cores, want 1", got)
	}
}

// v1 keeps quota and period in separate files, so both have to move together as
// the walk climbs.
func TestEffectiveCgroupCPUQuotaPairsQuotaWithItsOwnPeriod(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cpu.cfs_quota_us"), []byte("400000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cpu.cfs_period_us"), []byte("100000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := effectiveCgroupCPUQuota(cgroupCPUPath{
		quota:  filepath.Join(dir, "cpu.cfs_quota_us"),
		period: filepath.Join(dir, "cpu.cfs_period_us"),
	})
	if got != 4 {
		t.Fatalf("v1 quota = %v cores, want 4", got)
	}
}
