# ISSUES

## DB

- stream
- subscribe

## vlog

- 写mmap不需要同步 --- ok
- vlogThreshold大小[通过metric动态计算] --- ok
- vlogThreshold变更时机 --- ok
- valueLog.write --- ???

## memtable

- request结构定义 --- ok
- skiplist --- ok
  - skiplistIterator --- ok
  - arena --- ok
  - randomHeight --- ok
  - skiplist表中key的格式 --- ok
  - memTable.maxVersion --- ok
  - 写入skiplist并发流程

## sstable

- builder --- ok
  - sstable entry format --- ok
  - sstable index format --- ok
  - flushMemTable (SSTable) --- ok
  - diffKey使用 --- ???
  - table --- ok
    - OpenTable逻辑 --- ok
    - 分析blockIterator --- ok
    - 分析表Iterator --- ok
    - 分析ConcatIterator --- ok
    - 分析MergeIterator --- ok
    - table.block函数 --- ok
    - table,block引用计数 --- ok

## query

- item类型方法实现 --- ???
- badger/v4.Iterator迭代器实现 --- ???

## compaction

- levelsController --- ok
  - newLevelsController --- ok
  - startCompact --- ok
  - doCompact --- ok
    - fillTablesL0 --- ok
      - fillTablesL0ToLbase --- ok
      - fillTablesL0ToL0 --- ok
      - 分析为什么增加L0->L0层压实功能逻辑 --- ok
      - 分析L0->LBase的触发情况以及L0->L0的触发情况 --- ok
    - fillTables --- ok
  - runCompactDef --- ok
    - split键范围并行压实 --- ok
      - split键范围操作结果中丢失的右边界在压实中如何处理 --- ok
    - 压实操作中创建新表,删除旧表并没有同步修改manifest --- ok
    - compactBuildTables函数逻辑 --- ok
      - 分析subcompact函数逻辑 --- ok
        - 分析updateStats --- ok
        - 分析exceedsAllowedOverlap --- ok
        - 分析addKeys --- ok
          - firstKeyHasDiscardSet --- ok
          - isExpired --- ok
   - dropPrefixes从哪里给出 --- ???
  - pickCompactLevels --- ok
  - levelTargets --- ok
  - add sst --- ok
  - levelHandler --- ok
  - compactStatus --- ok
    - levelCompactStatus --- ok
      - keyRange --- ok
- levelHandler --- ok
- draw compact flow --- ok
- graph
  - split compact --- ???
  - parallel compact --- ???
  - merge compact --- ???

## manifest

- init --- ???
- format --- ok
- compressionType[snappy/ZSTD] --- ok
- draw format --- ok

## GC

- gc触发(RunValueLogGC) --- ok
- gc流程 --- ok
  - valueLog.pickLog --- ok
  - valueLog.doRunGC --- ok
  - valueLog.rewrite --- ok
- discardStats --- ok
  - discardStats结构 --- ok
  - discard比例 --- ok

## tnx

- y.WaterMark --- ok
- pendingWritesIterator --- ok
- oracle --- ok
- Txn --- ok
- z.MemHash --- ok
- 事务处理逻辑 --- ok
- 迭代器Iterator,item --- ???
- prefetch --- ok
- bannedKey --- ok
- 用户管理时间戳 --- ???
- 批量写入 --- ???
- keepTogether --- ???

## encrypt

- keyregistry --- ???

## compress
