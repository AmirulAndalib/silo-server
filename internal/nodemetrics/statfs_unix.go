//go:build linux || darwin

package nodemetrics

import (
	"golang.org/x/sys/unix"
)

// osStatfs reports one path's filesystem capacity.
//
// Used space is computed from blocks the filesystem considers free, not from
// the free space available to an unprivileged user (Bavail): the reserved
// margin is genuinely occupied capacity, and reporting it as used is what makes
// the number match what an operator sees in `df`.
//
// This call can block indefinitely on an unresponsive network mount. Callers
// must treat it as such — see probeDisk, which runs it on a goroutine nothing
// waits for.
func osStatfs(path string) (fsStats, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return fsStats{}, err
	}
	blockSize := uint64(st.Bsize)
	return fsStats{
		UsedBytes:  (st.Blocks - st.Bfree) * blockSize,
		TotalBytes: st.Blocks * blockSize,
		FSID:       formatFSID(int64(st.Fsid.Val[0]), int64(st.Fsid.Val[1])),
	}, nil
}
