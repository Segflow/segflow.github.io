// Package v1naive: the dumbest possible implementation. Read the entire file
// into a single []byte with os.ReadFile, then walk every byte looking for the
// first non-zero one. We treat the haystack as bytes (not int64s) on purpose:
// that is what a beginner would write.
package v1naive

import (
	"os"

	"github.com/segflow/blog-search-in-file/internal/search"
)

type S struct{}

func (S) Name() string { return "v1-naive-readall" }

func (S) Search(path string) (int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return -1, err
	}
	for i, b := range data {
		if b != 0 {
			// Round down to the 8-byte word start.
			return int64(i) &^ 7, nil
		}
	}
	return -1, nil
}

func init() { search.Register(S{}) }
