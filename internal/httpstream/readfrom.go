package httpstream

import (
	"io"
	"net/http"
)

// ReadFromChunkDefault bounds one zero-copy slice so a wrapper's byte counters
// and write deadlines stay current during a multi-gigabyte transfer.
const ReadFromChunkDefault int64 = 4 << 20

// ReaderFromOf reports w's own io.ReaderFrom, by direct assertion only. It
// deliberately does not traverse Unwrap: skipping an intermediate wrapper
// would bypass that wrapper's byte accounting.
func ReaderFromOf(w http.ResponseWriter) (io.ReaderFrom, bool) {
	rf, ok := w.(io.ReaderFrom)
	return rf, ok
}

// CopyChunked drives rf.ReadFrom in slices of chunk bytes, calling record after
// each slice. A non-positive chunk performs a single unbounded transfer.
func CopyChunked(rf io.ReaderFrom, src io.Reader, chunk int64, record func(n int64, err error)) (int64, error) {
	if chunk <= 0 {
		n, err := rf.ReadFrom(src)
		if record != nil {
			record(n, err)
		}
		return n, err
	}

	// A nested *io.LimitedReader defeats the kernel sendfile path, which unwraps
	// exactly one limiter before it looks for the *os.File. When src is already
	// limited, slice it by handing down a limiter over the same underlying reader
	// and decrementing the caller's budget, so the innermost ReaderFrom still sees
	// one limiter over the file.
	if lr, ok := src.(*io.LimitedReader); ok {
		var total int64
		for lr.N > 0 {
			sliceSize := min(chunk, lr.N)
			slice := &io.LimitedReader{R: lr.R, N: sliceSize}
			n, err := rf.ReadFrom(slice)
			lr.N -= n
			total += n
			if record != nil {
				record(n, err)
			}
			if err != nil {
				return total, err
			}
			if n < sliceSize {
				return total, nil
			}
		}
		return total, nil
	}

	var total int64
	for {
		n, err := rf.ReadFrom(io.LimitReader(src, chunk))
		total += n
		if record != nil {
			record(n, err)
		}
		if err != nil {
			return total, err
		}
		if n < chunk {
			return total, nil
		}
	}
}

// WriterOnly hides every method except Write, so io.Copy cannot rediscover the
// caller's own ReadFrom and recurse into it.
func WriterOnly(w io.Writer) io.Writer { return struct{ io.Writer }{w} }
