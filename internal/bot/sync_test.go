package bot

import (
	"sync"
	"testing"
)

func TestKeyedMutex(t *testing.T) {
	var k keyedMutex
	unlock := k.lock(1)
	if _, ok := k.tryLock(1); ok {
		t.Fatal("tryLock took a held key")
	}
	other, ok := k.tryLock(2)
	if !ok {
		t.Fatal("tryLock refused a free key")
	}
	other()
	unlock()
	if n := k.size(); n != 0 {
		t.Fatalf("%d keys left after every unlock", n)
	}

	var wg sync.WaitGroup
	counter := 0
	for range 50 {
		wg.Go(func() {
			defer k.lock(7)()
			counter++
		})
	}
	wg.Wait()
	if counter != 50 || k.size() != 0 {
		t.Fatalf("counter %d, %d keys left", counter, k.size())
	}
}

func TestTaskSet(t *testing.T) {
	var s taskSet
	s.add("a")
	if !s.has("a") || s.has("b") {
		t.Fatal("membership")
	}
	s.remove("a")
	if s.has("a") {
		t.Fatal("removed id still present")
	}
}
