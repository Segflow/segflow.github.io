// Package v3chunked reads the file in big chunks (1 MiB) into a reusable
// buffer and scans each chunk as []uint64. That kills two birds:
//
//   - One syscall per megabyte instead of one per byte; the kernel transfers
//     a whole page-cache page in one shot.
//   - The scan loop processes 8 bytes per iteration, so branch predictor and
//     instruction cache work in our favor.
package v3chunked

import (
	"errors"
	"io"
	"os"
	"unsafe"

	"github.com/segflow/blog-search-in-file/internal/search"
)

type S struct{}

func (S) Name() string { return "v3-chunked-uint64" }

const chunkSize = 1 << 20 // 1 MiB

func (S) Search(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return -1, err
	}
	defer f.Close()

	buf := make([]byte, chunkSize)
	var off int64
	for {
		n, err := io.ReadFull(f, buf)
		if n > 0 {
			// Reinterpret the byte chunk as []uint64. n is a multiple of 8
			// because the file size is and chunkSize is.
			words := unsafe.Slice((*uint64)(unsafe.Pointer(&buf[0])), n/8)
			for i, w := range words {
				if w != 0 {
					return off + int64(i)*8, nil
				}
			}
			off += int64(n)
		}
		if err == io.EOF || errors.Is(err, io.ErrUnexpectedEOF) {
			return -1, nil
		}
		if err != nil {
			return -1, err
		}
	}
}

func init() { search.Register(S{}) }
