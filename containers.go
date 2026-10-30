// Copyright (c) 2018, Mahmoud Hashemi
// SPDX-License-Identifier: BSD-3-Clause
//
// Go port of glom. Hashing, dicts and sets with CPython 3.12's semantics:
// insertion-ordered dicts whose keys are equal under Python's ==, and sets
// that iterate in the order of CPython's set hash table (setobject.c).

package glom

import (
	"hash/fnv"
	"math"
	"math/big"
	"math/bits"
)

var hashModulus = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 61), big.NewInt(1))

const (
	hashBits = 61
	hashInf  = 314159
)

var objIDs = map[Value]uint64{}

// objID is a stable identity number for an object (an id() stand-in).
func objID(v Value) uint64 {
	if id, ok := objIDs[v]; ok {
		return id
	}
	id := uint64(len(objIDs)+1) << 4
	objIDs[v] = id
	return id
}

func unhashableErr(v Value) error {
	return newExc(excTypeError, mkStr("unhashable type: '"+typeName(v)+"'"))
}

// pyHash is hash(): the numeric hash for numbers, CPython's tuple and
// frozenset hashes, a stable hash for str and bytes (CPython's depends on
// PYTHONHASHSEED), and an identity hash for everything else.
func pyHash(v Value) (int64, error) {
	switch x := v.(type) {
	case *noneObj:
		return 0xFCA86420 >> 4, nil
	case *boolObj:
		if x.v {
			return 1, nil
		}
		return 0, nil
	case *intObj:
		return intHash(x.v), nil
	case *floatObj:
		return floatHash(x.v, v), nil
	case *strObj:
		return bytesHash("s" + x.s), nil
	case *bytesObj:
		return bytesHash("b" + string(x.b)), nil
	case *tupleObj:
		return tupleHash(x.items)
	case *setObj:
		if !x.frozen {
			return 0, unhashableErr(v)
		}
		return frozensetHash(x), nil
	case *sliceObj:
		return tupleHash([]Value{x.start, x.stop, x.step})
	}
	if v.class().unhashable {
		return 0, unhashableErr(v)
	}
	return int64(objID(v) >> 4), nil
}

func bytesHash(s string) int64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	r := int64(h.Sum64())
	if r == -1 {
		r = -2
	}
	return r
}

func intHash(n *big.Int) int64 {
	var r int64
	if n.IsInt64() {
		a := n.Int64()
		neg := a < 0
		u := uint64(a)
		if neg {
			u = uint64(-a)
		}
		r = int64(u % (1<<61 - 1))
		if neg {
			r = -r
		}
	} else {
		m := new(big.Int).Rem(new(big.Int).Abs(n), hashModulus)
		r = m.Int64()
		if n.Sign() < 0 {
			r = -r
		}
	}
	if r == -1 {
		r = -2
	}
	return r
}

func floatHash(f float64, obj Value) int64 {
	if math.IsInf(f, 0) {
		if f > 0 {
			return hashInf
		}
		return -hashInf
	}
	if math.IsNaN(f) {
		return int64(objID(obj) >> 4)
	}
	m, e := math.Frexp(f)
	sign := int64(1)
	if m < 0 {
		sign = -1
		m = -m
	}
	var x uint64
	for m != 0 {
		x = ((x << 28) & (1<<61 - 1)) | x>>(hashBits-28)
		m *= 268435456.0
		e -= 28
		y := uint64(m)
		m -= float64(y)
		x += y
		if x >= 1<<61-1 {
			x -= 1<<61 - 1
		}
	}
	if e >= 0 {
		e = e % hashBits
	} else {
		e = hashBits - 1 - ((-1 - e) % hashBits)
	}
	x = ((x << uint(e)) & (1<<61 - 1)) | x>>uint(hashBits-e)
	r := int64(x) * sign
	if r == -1 {
		r = -2
	}
	return r
}

func tupleHash(items []Value) (int64, error) {
	const (
		prime1 uint64 = 11400714785074694791
		prime2 uint64 = 14029467366897019727
		prime5 uint64 = 2870177450012600261
	)
	acc := prime5
	for _, it := range items {
		lane, err := pyHash(it)
		if err != nil {
			return 0, err
		}
		acc += uint64(lane) * prime2
		acc = bits.RotateLeft64(acc, 31)
		acc *= prime1
	}
	acc += uint64(len(items)) ^ (prime5 ^ 3527539)
	if int64(acc) == -1 {
		return 1546275796, nil
	}
	return int64(acc), nil
}

func frozensetHash(s *setObj) int64 {
	var hash uint64
	shuffle := func(h uint64) uint64 { return ((h ^ 89869747) ^ (h << 16)) * 3644798167 }
	for _, e := range s.table {
		if e.live() {
			hash ^= shuffle(uint64(e.hash))
		}
	}
	if s.used&1 != 0 {
		hash ^= shuffle(^uint64(0))
	}
	hash ^= (uint64(s.used) + 1) * 1927868237
	hash ^= (hash >> 11) ^ (hash >> 25)
	hash = hash*69069 + 907133923
	if int64(hash) == -1 {
		hash = 590923713
	}
	return int64(hash)
}

// eqKey is PyObject_RichCompareBool(a, b, Py_EQ): identity first.
func eqKey(a, b Value) (bool, error) {
	if a == b {
		return true, nil
	}
	return pyEq(a, b)
}

// ---------------------------------------------------------------- dict

type dictEntry struct {
	key, val Value
	hash     int64
	live     bool
}

// dictObj is a dict: entries in insertion order, found by hash and ==.
type dictObj struct {
	entries []dictEntry
	index   map[int64][]int
	n       int
}

func (*dictObj) class() *classObj { return dictClass }

func newDict() *dictObj { return &dictObj{index: map[int64][]int{}} }

func (d *dictObj) find(key Value, h int64) (int, error) {
	for _, i := range d.index[h] {
		e := d.entries[i]
		if !e.live {
			continue
		}
		eq, err := eqKey(e.key, key)
		if err != nil {
			return -1, err
		}
		if eq {
			return i, nil
		}
	}
	return -1, nil
}

// get is d[key]: the value and whether the key is present.
func (d *dictObj) get(key Value) (Value, bool, error) {
	h, err := pyHash(key)
	if err != nil {
		return nil, false, err
	}
	i, err := d.find(key, h)
	if err != nil || i < 0 {
		return nil, false, err
	}
	return d.entries[i].val, true, nil
}

// set is d[key] = val; an equal key keeps its object and position.
func (d *dictObj) set(key, val Value) error {
	h, err := pyHash(key)
	if err != nil {
		return err
	}
	i, err := d.find(key, h)
	if err != nil {
		return err
	}
	if i >= 0 {
		d.entries[i].val = val
		return nil
	}
	d.entries = append(d.entries, dictEntry{key: key, val: val, hash: h, live: true})
	d.index[h] = append(d.index[h], len(d.entries)-1)
	d.n++
	return nil
}

// del is del d[key]; it reports whether the key was present.
func (d *dictObj) del(key Value) (bool, error) {
	h, err := pyHash(key)
	if err != nil {
		return false, err
	}
	i, err := d.find(key, h)
	if err != nil || i < 0 {
		return false, err
	}
	d.entries[i] = dictEntry{}
	d.n--
	if d.n*2 < len(d.entries) && len(d.entries) > 16 {
		d.compact()
	}
	return true, nil
}

func (d *dictObj) compact() {
	old := d.entries
	d.entries = nil
	d.index = map[int64][]int{}
	for _, e := range old {
		if e.live {
			d.entries = append(d.entries, e)
			d.index[e.hash] = append(d.index[e.hash], len(d.entries)-1)
		}
	}
}

func (d *dictObj) length() int { return d.n }

func (d *dictObj) keys() []Value {
	out := make([]Value, 0, d.n)
	for _, e := range d.entries {
		if e.live {
			out = append(out, e.key)
		}
	}
	return out
}

func (d *dictObj) items() []dictEntry {
	out := make([]dictEntry, 0, d.n)
	for _, e := range d.entries {
		if e.live {
			out = append(out, e)
		}
	}
	return out
}

func (d *dictObj) copy() *dictObj {
	n := newDict()
	for _, e := range d.items() {
		n.entries = append(n.entries, e)
		n.index[e.hash] = append(n.index[e.hash], len(n.entries)-1)
		n.n++
	}
	return n
}

// setStr sets a str key (attribute dicts).
func (d *dictObj) setStr(name string, val Value) { _ = d.set(mkStr(name), val) }

func (d *dictObj) getStr(name string) (Value, bool) {
	v, ok, _ := d.get(mkStr(name))
	return v, ok
}

// ---------------------------------------------------------------- set

const (
	setMinSize      = 8
	setLinearProbes = 9
	setPerturbShift = 5
)

type setEntry struct {
	key   Value
	hash  int64
	dummy bool
}

func (e setEntry) live() bool { return e.key != nil && !e.dummy }

// setObj is a set, or a frozenset when frozen is true.
type setObj struct {
	table  []setEntry
	fill   int
	used   int
	frozen bool
}

func (s *setObj) class() *classObj {
	if s.frozen {
		return frozensetClass
	}
	return setClass
}

func newSet(frozen bool) *setObj {
	return &setObj{table: make([]setEntry, setMinSize), frozen: frozen}
}

func (s *setObj) mask() int { return len(s.table) - 1 }

func (s *setObj) items() []Value {
	out := make([]Value, 0, s.used)
	for _, e := range s.table {
		if e.live() {
			out = append(out, e.key)
		}
	}
	return out
}

func (s *setObj) find(key Value, hash int64) (int, error) {
	mask := s.mask()
	perturb := uint64(hash)
	i := int(uint64(hash) & uint64(mask))
	for {
		probes := 0
		if i+setLinearProbes <= mask {
			probes = setLinearProbes
		}
		j := i
		for {
			e := s.table[j]
			if e.key == nil && !e.dummy {
				return -1, nil
			}
			if e.live() && e.hash == hash {
				eq, err := eqKey(e.key, key)
				if err != nil {
					return -1, err
				}
				if eq {
					return j, nil
				}
			}
			if probes == 0 {
				break
			}
			probes--
			j++
		}
		perturb >>= setPerturbShift
		i = int((uint64(i)*5 + 1 + perturb) & uint64(mask))
	}
}

func (s *setObj) addHashed(key Value, hash int64) error {
	mask := s.mask()
	perturb := uint64(hash)
	i := int(uint64(hash) & uint64(mask))
	free := -1
	for {
		probes := 0
		if i+setLinearProbes <= mask {
			probes = setLinearProbes
		}
		j := i
		for {
			e := &s.table[j]
			if e.key == nil && !e.dummy {
				if free >= 0 {
					s.used++
					s.table[free] = setEntry{key: key, hash: hash}
					return nil
				}
				s.fill++
				s.used++
				e.key, e.hash = key, hash
				if s.fill*5 < mask*3 {
					return nil
				}
				if s.used > 50000 {
					s.resize(s.used * 2)
				} else {
					s.resize(s.used * 4)
				}
				return nil
			}
			if e.live() && e.hash == hash {
				eq, err := eqKey(e.key, key)
				if err != nil {
					return err
				}
				if eq {
					return nil
				}
			} else if e.dummy && free < 0 {
				free = j
			}
			if probes == 0 {
				break
			}
			probes--
			j++
		}
		perturb >>= setPerturbShift
		i = int((uint64(i)*5 + 1 + perturb) & uint64(mask))
	}
}

func (s *setObj) add(key Value) error {
	h, err := pyHash(key)
	if err != nil {
		return err
	}
	return s.addHashed(key, h)
}

func insertClean(table []setEntry, key Value, hash int64) {
	mask := len(table) - 1
	perturb := uint64(hash)
	i := int(uint64(hash) & uint64(mask))
	for {
		if table[i].key == nil {
			table[i] = setEntry{key: key, hash: hash}
			return
		}
		if i+setLinearProbes <= mask {
			for j := 1; j <= setLinearProbes; j++ {
				if table[i+j].key == nil {
					table[i+j] = setEntry{key: key, hash: hash}
					return
				}
			}
		}
		perturb >>= setPerturbShift
		i = int((uint64(i)*5 + 1 + perturb) & uint64(mask))
	}
}

func (s *setObj) resize(minused int) {
	size := setMinSize
	for size <= minused {
		size <<= 1
	}
	old := s.table
	s.table = make([]setEntry, size)
	for _, e := range old {
		if e.live() {
			insertClean(s.table, e.key, e.hash)
		}
	}
	s.fill = s.used
}

func (s *setObj) contains(key Value) (bool, error) {
	h, err := pyHash(key)
	if err != nil {
		if sk, ok := key.(*setObj); ok && !sk.frozen {
			fk := sk.copyAs(true)
			h, _ = pyHash(fk)
			j, err := s.find(fk, h)
			return j >= 0, err
		}
		return false, err
	}
	j, err := s.find(key, h)
	return j >= 0, err
}

// merge is set_merge: other's elements added to s.
func (s *setObj) merge(other *setObj) error {
	if other == s || other.used == 0 {
		return nil
	}
	if (s.fill+other.used)*5 >= s.mask()*3 {
		s.resize((s.used + other.used) * 2)
	}
	if s.fill == 0 && s.mask() == other.mask() && other.fill == other.used {
		copy(s.table, other.table)
		s.fill, s.used = other.fill, other.used
		return nil
	}
	if s.fill == 0 {
		for _, e := range other.table {
			if e.live() {
				insertClean(s.table, e.key, e.hash)
			}
		}
		s.fill, s.used = other.used, other.used
		return nil
	}
	for _, e := range other.table {
		if e.live() {
			if err := s.addHashed(e.key, e.hash); err != nil {
				return err
			}
		}
	}
	return nil
}

// update is set_update_internal: the elements of any iterable added to s.
func (s *setObj) update(v Value) error {
	switch o := v.(type) {
	case *setObj:
		return s.merge(o)
	case *dictObj:
		if (s.fill+o.n)*5 >= s.mask()*3 {
			s.resize((s.used + o.n) * 2)
		}
		for _, k := range o.keys() {
			if err := s.add(k); err != nil {
				return err
			}
		}
		return nil
	}
	it, err := pyIter(v)
	if err != nil {
		return err
	}
	for {
		item, ok, err := it.next()
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if err := s.add(item); err != nil {
			return err
		}
	}
}

func (s *setObj) copyAs(frozen bool) *setObj {
	n := newSet(frozen)
	_ = n.merge(s)
	return n
}
