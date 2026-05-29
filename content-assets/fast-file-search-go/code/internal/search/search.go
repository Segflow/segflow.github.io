// Package search defines the common Searcher interface and registry.
package search

// Searcher finds the first non-zero int64 in the file at path and returns the
// byte offset of that value. Implementations may assume the file is exactly
// testfile.Size bytes and 8-byte aligned needles.
type Searcher interface {
	Name() string
	Search(path string) (int64, error)
}

var registry = map[string]Searcher{}

// Register adds s to the registry under s.Name().
func Register(s Searcher) {
	registry[s.Name()] = s
}

// All returns every registered searcher.
func All() map[string]Searcher { return registry }

// Get returns the searcher with the given name, or nil.
func Get(name string) Searcher { return registry[name] }
