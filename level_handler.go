/*
 * Copyright 2017 Dgraph Labs, Inc. and Contributors
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

package badger

import (
	"fmt"
	"sort"
	"sync"

	"github.com/dgraph-io/badger/v4/table"
	"github.com/dgraph-io/badger/v4/y"
)

type levelHandler struct {
	// Guards tables, totalSize.
	sync.RWMutex

	// For level >= 1, tables are sorted by key ranges, which do not overlap.
	// For level 0, tables are sorted by time.
	// For level 0, newest table are at the back. Compact the oldest one first, which is at the front.
	tables         []*table.Table
	totalSize      int64
	totalStaleSize int64

	// The following are initialized once and const.
	level    int
	strLevel string
	db       *DB
}

func (s *levelHandler) isLastLevel() bool {
	return s.level == s.db.opt.MaxLevels-1
}

func (s *levelHandler) getTotalStaleSize() int64 {
	s.RLock()
	defer s.RUnlock()
	return s.totalStaleSize
}

func (s *levelHandler) getTotalSize() int64 {
	s.RLock()
	defer s.RUnlock()
	return s.totalSize
}

// initTables replaces s.tables with given tables. This is done during loading.
func (s *levelHandler) initTables(tables []*table.Table) {
	s.Lock()
	defer s.Unlock()

	s.tables = tables
	s.totalSize = 0
	s.totalStaleSize = 0
	for _, t := range tables {
		s.addSize(t)
	}

	if s.level == 0 {
		// Key range will overlap. Just sort by fileID in ascending order
		// because newer tables are at the end of level 0.
		sort.Slice(s.tables, func(i, j int) bool {
			return s.tables[i].ID() < s.tables[j].ID()
		})
	} else {
		// Sort tables by keys.
		sort.Slice(s.tables, func(i, j int) bool {
			return y.CompareKeys(s.tables[i].Smallest(), s.tables[j].Smallest()) < 0
		})
	}
}

// deleteTables remove tables idx0, ..., idx1-1.
func (s *levelHandler) deleteTables(toDel []*table.Table) error {
	s.Lock() // s.Unlock() below

	toDelMap := make(map[uint64]struct{}) // 表id映射
	for _, t := range toDel {
		toDelMap[t.ID()] = struct{}{}
	}

	// Make a copy as iterators might be keeping a slice of tables.
	var newTables []*table.Table
	for _, t := range s.tables {
		_, found := toDelMap[t.ID()]
		if !found {
			newTables = append(newTables, t) // 要保留的表
			continue
		}
		s.subtractSize(t) // 减去要删除的表大小
	}
	s.tables = newTables

	s.Unlock() // Unlock s _before_ we DecrRef our tables, which can be slow.

	return decrRefs(toDel) // 通过减少引用计数删除表
}

// replaceTables will replace tables[left:right] with newTables. Note this EXCLUDES tables[right].
// You must call decr() to delete the old tables _after_ writing the update to the manifest.
func (s *levelHandler) replaceTables(toDel, toAdd []*table.Table) error {
	// Need to re-search the range of tables in this level to be replaced as other goroutines might
	// be changing it as well.  (They can't touch our tables, but if they add/remove other tables,
	// the indices get shifted around.)
	s.Lock() // We s.Unlock() below. +锁

	toDelMap := make(map[uint64]struct{}) // 表id映射
	for _, t := range toDel {
		toDelMap[t.ID()] = struct{}{}
	}
	var newTables []*table.Table
	for _, t := range s.tables {
		_, found := toDelMap[t.ID()]
		if !found {
			newTables = append(newTables, t) // 要保留的表
			continue
		}
		s.subtractSize(t) // 减去要删除的表大小
	}

	// Increase totalSize first.
	for _, t := range toAdd {
		s.addSize(t)                     // 增加新表的大小
		t.IncrRef()                      // 增加引用计数
		newTables = append(newTables, t) // 增加新表
	}

	// Assign tables.
	s.tables = newTables
	sort.Slice(s.tables, func(i, j int) bool { // 按键(升序)排序
		return y.CompareKeys(s.tables[i].Smallest(), s.tables[j].Smallest()) < 0
	})
	s.Unlock()             // s.Unlock before we DecrRef tables -- that can be slow. -锁
	return decrRefs(toDel) // 通过减少引用计数删除表
}

// addTable adds toAdd table to levelHandler. Normally when we add tables to levelHandler, we sort
// tables based on table.Smallest. This is required for correctness of the system. But in case of
// stream writer this can be avoided. We can just add tables to levelHandler's table list
// and after all addTable calls, we can sort table list(check sortTable method).
// NOTE: levelHandler.sortTables() should be called after call addTable calls are done.
func (s *levelHandler) addTable(t *table.Table) {
	s.Lock()
	defer s.Unlock()

	s.addSize(t) // Increase totalSize first.
	t.IncrRef()
	s.tables = append(s.tables, t)
}

// sortTables sorts tables of levelHandler based on table.Smallest.
// Normally it should be called after all addTable calls.
func (s *levelHandler) sortTables() {
	s.RLock()
	defer s.RUnlock()

	sort.Slice(s.tables, func(i, j int) bool {
		return y.CompareKeys(s.tables[i].Smallest(), s.tables[j].Smallest()) < 0
	})
}

func decrRefs(tables []*table.Table) error {
	for _, table := range tables {
		if err := table.DecrRef(); err != nil {
			return err
		}
	}
	return nil
}

func newLevelHandler(db *DB, level int) *levelHandler {
	return &levelHandler{
		level:    level,
		strLevel: fmt.Sprintf("l%d", level),
		db:       db,
	}
}

// tryAddLevel0Table returns true if ok and no stalling.
func (s *levelHandler) tryAddLevel0Table(t *table.Table) bool {
	y.AssertTrue(s.level == 0)
	// Need lock as we may be deleting the first table during a level 0 compaction.
	s.Lock()         // +锁
	defer s.Unlock() // -锁
	// Stall (by returning false) if we are above the specified stall setting for L0.
	if len(s.tables) >= s.db.opt.NumLevelZeroTablesStall {
		return false
	}

	s.tables = append(s.tables, t)
	t.IncrRef() // +1
	s.addSize(t)

	return true
}

// This should be called while holding the lock on the level.
func (s *levelHandler) addSize(t *table.Table) {
	s.totalSize += t.Size()
	s.totalStaleSize += int64(t.StaleDataSize())
}

// This should be called while holding the lock on the level.
func (s *levelHandler) subtractSize(t *table.Table) {
	s.totalSize -= t.Size()
	s.totalStaleSize -= int64(t.StaleDataSize())
}
func (s *levelHandler) numTables() int {
	s.RLock()
	defer s.RUnlock()
	return len(s.tables)
}

func (s *levelHandler) close() error {
	s.RLock()
	defer s.RUnlock()
	var err error
	for _, t := range s.tables {
		if closeErr := t.Close(-1); closeErr != nil && err == nil {
			err = closeErr
		}
	}
	return y.Wrap(err, "levelHandler.close")
}

// getTableForKey acquires a read-lock to access s.tables. It returns a list of tableHandlers.
func (s *levelHandler) getTableForKey(key []byte) ([]*table.Table, func() error) {
	s.RLock()
	defer s.RUnlock()

	if s.level == 0 {
		// For level 0, we need to check every table. Remember to make a copy as s.tables may change
		// once we exit this function, and we don't want to lock s.tables while seeking in tables.
		// CAUTION: Reverse the tables.
		out := make([]*table.Table, 0, len(s.tables))
		for i := len(s.tables) - 1; i >= 0; i-- {
			out = append(out, s.tables[i])
			s.tables[i].IncrRef()
		}
		return out, func() error {
			for _, t := range out {
				if err := t.DecrRef(); err != nil {
					return err
				}
			}
			return nil
		}
	}
	// For level >= 1, we can do a binary search as key range does not overlap.
	idx := sort.Search(len(s.tables), func(i int) bool {
		return y.CompareKeys(s.tables[i].Biggest(), key) >= 0
	})
	if idx >= len(s.tables) {
		// Given key is strictly > than every element we have.
		return nil, func() error { return nil }
	}
	tbl := s.tables[idx]
	tbl.IncrRef()
	return []*table.Table{tbl}, tbl.DecrRef
}

// get returns value for a given key or the key after that. If not found, return nil.
func (s *levelHandler) get(key []byte) (y.ValueStruct, error) {
	tables, decr := s.getTableForKey(key)
	keyNoTs := y.ParseKey(key)

	hash := y.Hash(keyNoTs)
	var maxVs y.ValueStruct
	for _, th := range tables {
		if th.DoesNotHave(hash) {
			y.NumLSMBloomHitsAdd(s.db.opt.MetricsEnabled, s.strLevel, 1)
			continue
		}

		it := th.NewIterator(0)
		defer it.Close()

		y.NumLSMGetsAdd(s.db.opt.MetricsEnabled, s.strLevel, 1)
		it.Seek(key)
		if !it.Valid() {
			continue
		}
		if y.SameKey(key, it.Key()) {
			// 此处不断判断最大版本是因为GC会重写vlog文件数据
			// 导致更旧的版本出现在更新版本的上层
			// 所以需要一直查询到sst的最后一层才能确定真正的最新版本
			if version := y.ParseTs(it.Key()); maxVs.Version < version {
				maxVs = it.ValueCopy()
				maxVs.Version = version
			}
		}
	}
	return maxVs, decr()
}

// appendIterators appends iterators to an array of iterators, for merging.
// Note: This obtains references for the table handlers. Remember to close these iterators.
func (s *levelHandler) appendIterators(iters []y.Iterator, opt *IteratorOptions) []y.Iterator {
	s.RLock()         // +锁
	defer s.RUnlock() // -锁

	var topt int
	if opt.Reverse {
		topt = table.REVERSED
	}
	if s.level == 0 {
		// Remember to add in reverse order!
		// The newer table at the end of s.tables should be added first as it takes precedence.
		// Level 0 tables are not in key sorted order, so we need to consider them one by one.
		var out []*table.Table
		for _, t := range s.tables {
			if opt.pickTable(t) {
				out = append(out, t)
			}
		}
		return appendIteratorsReversed(iters, out, topt)
	}

	tables := opt.pickTables(s.tables)
	if len(tables) == 0 {
		return iters
	}
	return append(iters, table.NewConcatIterator(tables, topt))
}

type levelHandlerRLocked struct{}

// overlappingTables returns the tables that intersect with key range. Returns a half-interval.
// This function should already have acquired a read lock, and this is so important the caller must
// pass an empty parameter declaring such.
func (s *levelHandler) overlappingTables(_ levelHandlerRLocked, kr keyRange) (int, int) {
	if len(kr.left) == 0 || len(kr.right) == 0 {
		return 0, 0
	}
	left := sort.Search(len(s.tables), func(i int) bool {
		return y.CompareKeys(kr.left, s.tables[i].Biggest()) <= 0
	})
	right := sort.Search(len(s.tables), func(i int) bool {
		return y.CompareKeys(kr.right, s.tables[i].Smallest()) < 0
	})
	return left, right
}
