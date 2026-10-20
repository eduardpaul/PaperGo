package script

import (
	"container/list"
	"sync"

	"github.com/dop251/goja"
)

// programCache keeps the most recently used compiled artifacts. A compiled
// program can run in many runtimes at once.
type programCache struct {
	mu       sync.Mutex
	capacity int
	order    *list.List
	entries  map[string]*list.Element
}

type cached struct {
	hash    string
	program *goja.Program
}

func newProgramCache(capacity int) *programCache {
	return &programCache{capacity: capacity, order: list.New(), entries: map[string]*list.Element{}}
}

func (c *programCache) get(a Artifact) (*goja.Program, error) {
	c.mu.Lock()
	if e, ok := c.entries[a.Hash]; ok {
		c.order.MoveToFront(e)
		c.mu.Unlock()
		return e.Value.(*cached).program, nil
	}
	c.mu.Unlock()
	program, err := goja.Compile("workflow.js", a.Code, true)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[a.Hash]; ok {
		return e.Value.(*cached).program, nil
	}
	c.entries[a.Hash] = c.order.PushFront(&cached{hash: a.Hash, program: program})
	for c.order.Len() > c.capacity {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.entries, oldest.Value.(*cached).hash)
	}
	return program, nil
}
