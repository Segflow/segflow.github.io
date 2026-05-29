// Package v7preadparallel parallelizes the v3 strategy. Each worker holds its
// own file descriptor (so concurrent pread(2) calls don't contend on the
// per-fd position) and reads its slice of the file into a 1 MiB chunk that it
// scans as []uint64.
//
// Why this should beat v6 (parallel mmap):
//
//   - Page faults are out of the picture. We're back to one big copy_to_user
//     per chunk, batched at 1 MiB.
//   - Each goroutine touches its own kernel buffer cache pages without TLB
//     thrashing.
//   - We pin work to logical CPUs by having NumCPU goroutines on a 6c/12t
//     box; pread doesn't share state across workers.
package v7preadparallel

import (
	"os"
	"github.com/segflow/blog-search-in-file/internal/search"
	"sync"
	"sync/atomic"
	"unsafe"

)

type S struct{}

func (S) Name() string { return "v7-pread-parallel" }

const chunkSize = 1 << 20 // 1 MiB

func (S) Search(path string) (int64, error) {
	st, err := os.Stat(path)
	if err != nil {
		return -1, err
	}
	size := st.Size()

	nWorkers := search.Workers()
	// Shard size aligned up to chunkSize.
	per := (size + int64(nWorkers) - 1) / int64(nWorkers)
	if per%chunkSize != 0 {
		per += chunkSize - per%chunkSize
	}

	var found atomic.Int64
	found.Store(-1)
	var wg sync.WaitGroup
	errs := make(chan error, nWorkers)

	for w := range nWorkers {
		start := int64(w) * per
		end := min(start+per, size)
		if start >= end {
			continue
		}
		wg.Add(1)
		go func(start, end int64) {
			defer wg.Done()
			f, err := os.Open(path)
			if err != nil {
				errs <- err
				return
			}
			defer f.Close()

			buf := make([]byte, chunkSize)
			off := start
			for off < end {
				want := chunkSize
				if int64(want) > end-off {
					want = int(end - off)
				}
				n, err := f.ReadAt(buf[:want], off)
				if err != nil && n == 0 {
					errs <- err
					return
				}
				words := unsafe.Slice((*uint64)(unsafe.Pointer(&buf[0])), n/8)
				for i, x := range words {
					if x != 0 {
						hit := off + int64(i)*8
						for {
							cur := found.Load()
							if cur != -1 && cur <= hit {
								return
							}
							if found.CompareAndSwap(cur, hit) {
								return
							}
						}
					}
				}
				off += int64(n)
				// If a smaller offset was already found, give up early.
				if cur := found.Load(); cur != -1 && cur < off {
					return
				}
			}
		}(start, end)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			return -1, e
		}
	}
	return found.Load(), nil
}

func init() { search.Register(S{}) }
