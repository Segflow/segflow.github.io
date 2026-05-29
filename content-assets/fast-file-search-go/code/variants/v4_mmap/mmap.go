// Package v4mmap maps the whole file into the process address space with
// mmap(2). The kernel then pulls in 4 KiB pages on demand straight from the
// page cache -- no read(2) syscalls, no userland copy. Our search loop just
// walks a giant []uint64.
package v4mmap

import (
	"os"
	"unsafe"

	"github.com/segflow/blog-search-in-file/internal/search"
	"golang.org/x/sys/unix"
)

type S struct{}

func (S) Name() string { return "v4-mmap" }

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

	words := unsafe.Slice((*uint64)(unsafe.Pointer(&data[0])), size/8)
	for i, w := range words {
		if w != 0 {
			return int64(i) * 8, nil
		}
	}
	return -1, nil
}

func init() { search.Register(S{}) }
