/*
 * Copyright 2019 Dgraph Labs, Inc. and Contributors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package table

import (
	"bytes"

	"github.com/dgraph-io/badger/v4/y"
)

type node struct {
	// MergeIterator.small使用
	valid bool
	key   []byte // 当前key

	// MergeIterator.small(left,right)使用
	iter y.Iterator // Iterator,ConcatIterator,MergeIterator

	// The two iterators are type asserted from `y.Iterator`, used to inline more function calls.
	// Calling functions on concrete types is much faster (about 25-30%) than calling the
	// interface's function.
	// 此类性能优化第一次遇到,是个新思路 !!!
	// MergeIterator.left(right)使用
	merge  *MergeIterator
	concat *ConcatIterator
}

// iter可能三种类型
// - Iterator
// - ConcatIterator
// - MergeIterator
func (n *node) setIterator(iter y.Iterator) {
	n.iter = iter
	// It's okay if the type assertion below fails and n.merge/n.concat are set to nil.
	// We handle the nil values of merge and concat in all the methods.
	// 类型可能不匹配, 后续使用需要注意判nil
	n.merge, _ = iter.(*MergeIterator)
	n.concat, _ = iter.(*ConcatIterator)
}

// 设置valid,key字段
func (n *node) setKey() {
	switch { // 等价于n.iter.Key()
	case n.merge != nil:
		n.valid = n.merge.small.valid
		if n.valid {
			n.key = n.merge.small.key
		}
	case n.concat != nil:
		n.valid = n.concat.Valid()
		if n.valid {
			n.key = n.concat.Key()
		}
	default:
		n.valid = n.iter.Valid()
		if n.valid {
			n.key = n.iter.Key()
		}
	}
}

func (n *node) next() {
	switch { // 等价于n.iter.Next()
	case n.merge != nil:
		n.merge.Next()
	case n.concat != nil:
		n.concat.Next()
	default:
		n.iter.Next()
	}
	n.setKey()
}

func (n *node) rewind() {
	n.iter.Rewind()
	n.setKey()
}

func (n *node) seek(key []byte) {
	n.iter.Seek(key)
	n.setKey()
}

// MergeIterator merges multiple iterators.
// NOTE: MergeIterator owns the array of iterators and is responsible for closing them.
// 实现y.Iterator接口
type MergeIterator struct {
	left  node
	right node

	small  *node
	curKey []byte

	reverse bool
}

// NewMergeIterator creates a merge iterator.
func NewMergeIterator(iters []y.Iterator, reverse bool) y.Iterator {
	switch len(iters) {
	case 0:
		return nil
	case 1:
		return iters[0]
	case 2:
		mi := &MergeIterator{
			reverse: reverse,
		}
		mi.left.setIterator(iters[0])
		mi.right.setIterator(iters[1])
		// Assign left iterator randomly. This will be fixed when user calls rewind/seek.
		mi.small = &mi.left
		return mi
	}
	mid := len(iters) / 2
	return NewMergeIterator(
		[]y.Iterator{
			NewMergeIterator(iters[:mid], reverse),
			NewMergeIterator(iters[mid:], reverse),
		}, reverse)
}

// Close implements y.Iterator.
func (mi *MergeIterator) Close() error {
	err1 := mi.left.iter.Close()
	err2 := mi.right.iter.Close()
	if err1 != nil {
		return y.Wrap(err1, "MergeIterator")
	}
	return y.Wrap(err2, "MergeIterator")
}

// 函数作用就是不断调整small指向当前较小key的iter
func (mi *MergeIterator) fix() {
	if !mi.bigger().valid { // 已经超出表对应的范围
		return
	}
	if !mi.small.valid {
		mi.swapSmall()
		return
	}
	cmp := y.CompareKeys(mi.small.key, mi.bigger().key) // 首先比较key,相同再比较version
	switch {
	case cmp == 0: // Both the keys are equal.
		// In case of same keys, move the right iterator ahead.
		mi.right.next()
		if &mi.right == mi.small {
			mi.swapSmall()
		}
		return
	case cmp < 0: // Small is less than bigger().
		if mi.reverse {
			mi.swapSmall()
		} else { //nolint:staticcheck
			// we don't need to do anything. Small already points to the smallest.
		}
		return
	default: // bigger() is less than small.
		if mi.reverse {
			// Do nothing since we're iterating in reverse. Small currently points to
			// the bigger key and that's okay in reverse iteration.
		} else {
			mi.swapSmall()
		}
		return
	}
}

func (mi *MergeIterator) bigger() *node {
	if mi.small == &mi.left {
		return &mi.right
	}
	return &mi.left
}

func (mi *MergeIterator) swapSmall() {
	if mi.small == &mi.left {
		mi.small = &mi.right
		return
	}
	if mi.small == &mi.right {
		mi.small = &mi.left
		return
	}
}

// Next returns the next element. If it is the same as the current key, ignore it. 键完全相同跳过 ???
func (mi *MergeIterator) Next() {
	for mi.Valid() {
		if !bytes.Equal(mi.small.key, mi.curKey) { // 包含version比较
			break // key不同相当于找到next
		}
		mi.small.next()
		mi.fix()
	}
	mi.setCurrent()
}

func (mi *MergeIterator) setCurrent() {
	mi.curKey = append(mi.curKey[:0], mi.small.key...)
}

// Rewind seeks to first element (or last element for reverse iterator).
func (mi *MergeIterator) Rewind() {
	mi.left.rewind()
	mi.right.rewind()
	mi.fix()
	mi.setCurrent()
}

// Seek brings us to element with key >= given key.
func (mi *MergeIterator) Seek(key []byte) {
	mi.left.seek(key)
	mi.right.seek(key)
	mi.fix()
	mi.setCurrent()
}

// Valid returns whether the MergeIterator is at a valid element.
func (mi *MergeIterator) Valid() bool {
	return mi.small.valid
}

// Key returns the key associated with the current iterator.
func (mi *MergeIterator) Key() []byte {
	return mi.small.key
}

// Value returns the value associated with the iterator.
func (mi *MergeIterator) Value() y.ValueStruct {
	return mi.small.iter.Value()
}
