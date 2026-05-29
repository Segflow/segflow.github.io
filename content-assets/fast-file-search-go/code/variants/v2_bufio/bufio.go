// Package v2bufio streams the file through a bufio.Reader and reads one byte
// at a time. Slower than V1 in throughput but uses ~64 KiB instead of 2 GiB
// of RAM. It is the textbook "stream a file" answer.
package v2bufio

import (
	"bufio"
	"io"
	"os"

	"github.com/segflow/blog-search-in-file/internal/search"
)

type S struct{}

func (S) Name() string { return "v2-bufio-byte" }

func (S) Search(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return -1, err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<16) // 64 KiB
	var off int64
	for {
		b, err := r.ReadByte()
		if err == io.EOF {
			return -1, nil
		}
		if err != nil {
			return -1, err
		}
		if b != 0 {
			return off &^ 7, nil
		}
		off++
	}
}

func init() { search.Register(S{}) }
