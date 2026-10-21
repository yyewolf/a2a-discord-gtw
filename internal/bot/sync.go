package bot

import (
	"sync"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/disgoorg/snowflake/v2"
)

// keyedMutex is a set of mutexes created on demand and dropped when no one
// holds or waits for them.
type keyedMutex struct {
	mu    sync.Mutex
	locks map[snowflake.ID]*refMutex
}

type refMutex struct {
	sync.Mutex
	refs int
}

func (k *keyedMutex) acquire(key snowflake.ID) *refMutex {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.locks == nil {
		k.locks = map[snowflake.ID]*refMutex{}
	}
	m, ok := k.locks[key]
	if !ok {
		m = &refMutex{}
		k.locks[key] = m
	}
	m.refs++
	return m
}

func (k *keyedMutex) release(key snowflake.ID, m *refMutex) {
	k.mu.Lock()
	defer k.mu.Unlock()
	m.refs--
	if m.refs == 0 {
		delete(k.locks, key)
	}
}

// lock locks key and returns its unlock.
func (k *keyedMutex) lock(key snowflake.ID) func() {
	m := k.acquire(key)
	m.Lock()
	return func() {
		m.Unlock()
		k.release(key, m)
	}
}

// tryLock locks key if it is free.
func (k *keyedMutex) tryLock(key snowflake.ID) (func(), bool) {
	m := k.acquire(key)
	if !m.TryLock() {
		k.release(key, m)
		return nil, false
	}
	return func() {
		m.Unlock()
		k.release(key, m)
	}, true
}

// size is the number of keys tracked, for tests.
func (k *keyedMutex) size() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.locks)
}

// taskSet is a concurrent set of task IDs.
type taskSet struct {
	mu sync.Mutex
	m  map[a2a.TaskID]struct{}
}

func (s *taskSet) add(id a2a.TaskID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[a2a.TaskID]struct{}{}
	}
	s.m[id] = struct{}{}
}

func (s *taskSet) remove(id a2a.TaskID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
}

func (s *taskSet) has(id a2a.TaskID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.m[id]
	return ok
}
