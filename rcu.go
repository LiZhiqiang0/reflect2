package reflect2

import (
	"sync"
	"sync/atomic"
	"unsafe"
)

// LinerRCU 依据 Read Copy Update 原理实现
type LinerRCU struct {
	lock sync.Mutex
	m    unsafe.Pointer
}

func NewLinerRCU() (c *LinerRCU) {
	return &LinerRCU{
		lock: sync.Mutex{},
		m: unsafe.Pointer(&linerMap{
			n: 0,
			m: _InitCapacity - 1,
			b: make([]mapEntry, _InitCapacity),
		}),
	}
}

func (c *LinerRCU) Load(key uintptr) (v any, ok bool) {
	m := (*linerMap)(atomic.LoadPointer(&c.m))
	res := m.get(key)
	return res, res != nil
}

func (c *LinerRCU) Store(key uintptr, v any) {
	c.lock.Lock()
	defer c.lock.Unlock()

	m := (*linerMap)(atomic.LoadPointer(&c.m))
	atomic.StorePointer(&c.m, unsafe.Pointer(m.add(key, v)))
}

func (c *LinerRCU) LoadOrStore(key uintptr, newV any) (v any, loaded bool) {
	got, ok := c.Load(key)
	if ok {
		return got, true
	}

	c.lock.Lock()
	defer c.lock.Unlock()
	// double check
	m := (*linerMap)(atomic.LoadPointer(&c.m))
	res := m.get(key)
	if res != nil {
		return res, true
	}

	m2 := m.add(key, newV)
	atomic.StorePointer(&c.m, unsafe.Pointer(m2))

	return newV, false
}

/** 线性探测的开放寻址 Map **/

const (
	_LoadFactor   = 0.5
	_InitCapacity = 4096 // must be a power of 2
)

type linerMap struct {
	n uint64 // 实际元素个数
	m uint64 // capacity
	b []mapEntry
}

type mapEntry struct {
	vt uintptr
	fn any
}

func (self *linerMap) copy() *linerMap {
	fork := &linerMap{
		n: self.n,
		m: self.m,
		b: make([]mapEntry, len(self.b)),
	}
	for i, f := range self.b {
		fork.b[i] = f
	}
	return fork
}

func (self *linerMap) get(vt uintptr) any {
	i := self.m + 1
	h := uint64(vt)
	p := h & self.m

	/* linear probing */
	for ; i > 0; i-- {
		if b := self.b[p]; b.vt == vt {
			return b.fn
		} else if b.vt == 0 {
			break
		} else {
			p = (p + 1) & self.m
		}
	}

	/* not found */
	return nil
}

func (self *linerMap) add(vt uintptr, fn any) *linerMap {
	p := self.copy()
	f := float64(atomic.LoadUint64(&p.n)+1) / float64(p.m+1)

	/* check for load factor */
	if f > _LoadFactor {
		p = p.rehash()
	}

	/* insert the value */
	p.insert(vt, fn)
	return p
}

func (self *linerMap) rehash() *linerMap {
	c := (self.m + 1) << 1
	r := &linerMap{m: c - 1, b: make([]mapEntry, int(c))}

	/* rehash every entry */
	for i := uint64(0); i <= self.m; i++ {
		if b := self.b[i]; b.vt > 0 {
			r.insert(b.vt, b.fn)
		}
	}

	/* rebuild successful */
	return r
}

func (self *linerMap) insert(vt uintptr, fn any) {
	h := uint64(vt)
	p := h & self.m

	/* linear probing */
	for i := uint64(0); i <= self.m; i++ {
		if b := &self.b[p]; b.vt > 0 {
			p += 1
			p &= self.m
		} else {
			b.vt = vt
			b.fn = fn
			atomic.AddUint64(&self.n, 1)
			return
		}
	}

	/* should never happens */
	panic("no available slots")
}
