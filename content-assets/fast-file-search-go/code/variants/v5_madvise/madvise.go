// Package v5madvise = v4 + two madvise hints:
//
//   - MADV_SEQUENTIAL tells the kernel "I'll read these pages in order; feel
//     free to read ahead aggressively and drop pages behind me".
//   - MADV_WILLNEED says "go fetch these pages now". On a warm cache this is
//     basically free; on a cold cache it lets the kernel kick off async I/O
//     before the userland loop ever touches the page.
//
// Together they keep the CPU from stalling on minor page faults.
package v5madvise

import (
	"os"
	"unsafe"

	"github.com/segflow/blog-search-in-file/internal/search"
	"golang.org/x/sys/unix"
)

type S struct{}

func (S) Name() string { return "v5-mmap-madvise" }

func (S) Search(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return -1, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return -1, err
	}
	size := int(st.Size())

	data, err := unix.Mmap(int(f.Fd()), 0, size, unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		return -1, err
	}
	defer unix.Munmap(data)

	_ = unix.Madvise(data, unix.MADV_SEQUENTIAL)
	_ = unix.Madvise(data, unix.MADV_WILLNEED)

	words := unsafe.Slice((*uint64)(unsafe.Pointer(&data[0])), size/8)
	for i, w := range words {
		if w != 0 {
			return int64(i) * 8, nil
		}
	}
	return -1, nil
}

func init() { search.Register(S{}) }
