/*
 * Copyright 2020 Dgraph Labs, Inc. and Contributors
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
	"encoding/binary"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/dgraph-io/badger/v4/y"
	"github.com/dgraph-io/ristretto/z"
)

// discardStats keeps track of the amount of data that could be discarded for
// a given logfile.
// 实现sort.Interface接口
type discardStats struct {
	sync.Mutex

	*z.MmapFile
	opt           Options
	nextEmptySlot int // 下一个slot的偏移,每个slot占16字节
}

const discardFname string = "DISCARD"

// 文件格式(FID升序)
// +----------------+---------+----------------+
// |   entry(16B)   |   ...   |   entry(16B)   |
// +----------------+---------+----------------+
// 项格式
// +-------------+--------------+
// |   fid(8B)   |   size(8B)   |
// +-------------+--------------+

func InitDiscardStats(opt Options) (*discardStats, error) {
	fname := filepath.Join(opt.ValueDir, discardFname)

	// 1MB file can store 65.536 discard entries. Each entry is 16 bytes.
	mf, err := z.OpenMmapFile(fname, os.O_CREATE|os.O_RDWR, 1<<20) // 硬编码1M ???
	lf := &discardStats{
		MmapFile: mf,
		opt:      opt,
	}
	if err == z.NewFile {
		// We don't need to zero out the entire 1MB.
		lf.zeroOut()

	} else if err != nil {
		return nil, y.Wrapf(err, "while opening file: %s\n", discardFname)
	}

	for slot := 0; slot < lf.maxSlot(); slot++ { // 初始化slot偏移
		if lf.get(16*slot) == 0 { // 取槽的8个字节
			lf.nextEmptySlot = slot
			break
		}
	}
	sort.Sort(lf) // 文件id升序排序
	opt.Infof("Discard stats nextEmptySlot: %d\n", lf.nextEmptySlot)
	return lf, nil
}

// sort.Interface
func (lf *discardStats) Len() int {
	return lf.nextEmptySlot
}

// sort.Interface
func (lf *discardStats) Less(i, j int) bool {
	return lf.get(16*i) < lf.get(16*j) // fid升序
}

// sort.Interface
func (lf *discardStats) Swap(i, j int) {
	left := lf.Data[16*i : 16*i+16]
	right := lf.Data[16*j : 16*j+16]
	var tmp [16]byte
	copy(tmp[:], left)
	copy(left, right)
	copy(right, tmp[:])
}

// offset is not slot.
func (lf *discardStats) get(offset int) uint64 {
	return binary.BigEndian.Uint64(lf.Data[offset : offset+8])
}
func (lf *discardStats) set(offset int, val uint64) {
	binary.BigEndian.PutUint64(lf.Data[offset:offset+8], val)
}

// zeroOut would zero out the next slot.
func (lf *discardStats) zeroOut() { // 清空一个完整的槽位
	lf.set(lf.nextEmptySlot*16, 0)
	lf.set(lf.nextEmptySlot*16+8, 0)
}

func (lf *discardStats) maxSlot() int {
	return len(lf.Data) / 16
}

// Update would update the discard stats for the given file id. If discard is
// 0, it would return the current value of discard for the file. If discard is
// < 0, it would set the current value of discard to zero for the file.
// discard = 0 查询当前值
// discard < 0 重新值为0
// discard > 0 且存在对应slot则增加值,无对应slot则增加slot并设置值
func (lf *discardStats) Update(fidu uint32, discard int64) int64 {
	fid := uint64(fidu)
	lf.Lock()         // +锁
	defer lf.Unlock() // -锁

	idx := sort.Search(lf.nextEmptySlot, func(slot int) bool {
		return lf.get(slot*16) >= fid
	})
	if idx < lf.nextEmptySlot && lf.get(idx*16) == fid { // 找到对应的slot
		off := idx*16 + 8 // +8获取discard偏移
		curDisc := lf.get(off)
		if discard == 0 { // 代表查询当前值
			return int64(curDisc)
		}
		if discard < 0 { // 重置值为0
			lf.set(off, 0)
			return 0
		}
		lf.set(off, curDisc+uint64(discard)) // 在原值增加
		return int64(curDisc + uint64(discard))
	}
	if discard <= 0 { // 没有找到对应的slot且discard>0,此时不需要创建新的slot
		// No need to add a new entry.
		return 0
	}

	// Could not find the fid. Add the entry. 没有找到对应的slot且discard>0所以需要增加新的slot
	idx = lf.nextEmptySlot
	lf.set(idx*16, fid)               // 文件ID
	lf.set(idx*16+8, uint64(discard)) // discard数据量

	// Move to next slot.
	lf.nextEmptySlot++
	for lf.nextEmptySlot >= lf.maxSlot() { // 超过阈值
		y.Check(lf.Truncate(2 * int64(len(lf.Data)))) // 以2倍自动扩容,截断操作会重新映射Data大小
	}
	lf.zeroOut()

	sort.Sort(lf) // 文件id升序排序,实际不需要此操作,排序顺序不会因discard数的更新而变化 ???
	return discard
}

func (lf *discardStats) Iterate(f func(fid, stats uint64)) {
	for slot := 0; slot < lf.nextEmptySlot; slot++ {
		idx := 16 * slot
		f(lf.get(idx), lf.get(idx+8))
	}
}

// MaxDiscard returns the file id with maximum discard bytes.
func (lf *discardStats) MaxDiscard() (uint32, int64) {
	lf.Lock()         // +锁
	defer lf.Unlock() // -锁

	var maxFid, maxVal uint64
	lf.Iterate(func(fid, val uint64) {
		if maxVal < val {
			maxVal = val
			maxFid = fid
		}
	})
	return uint32(maxFid), int64(maxVal)
}
