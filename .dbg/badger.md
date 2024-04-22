# ISSUES

## vlog

- 写mmap不需要同步 --- ok
- vlogThreshold大小[通过metric动态计算]
- vlogThreshold变更时机

## memtable

- request结构定义
- skiplist
  - skiplistIterator
  - arena
  - randomHeight
  - skiplist表中key的格式
  - memTable.maxVersion
  - 写入skiplist并发流程

## sstable

- builder
  - sstable entry format
  - sstable index format
  - flushMemTable (SSTable)
  - table
    - OpenTable逻辑
    - 分析blockIterator
    - 分析表Iterator
    - 分析ConcatIterator --- ok
    - 分析MergeIterator --- ok
    - table.block函数
    - table,block引用计数

## compaction

- levelsController
  - newLevelsController
  - startCompact
  - doCompact
    - fillTablesL0
      - fillTablesL0ToLbase
      - fillTablesL0ToL0
      - 分析为什么增加L0->L0层压实功能逻辑
      - 分析L0->LBase的触发情况以及L0->L0的触发情况
    - fillTables
  - runCompactDef
    - split键范围并行压实
      - split键范围操作结果中丢失的右边界在压实中如何处理
    - 压实操作中创建新表,删除旧表并没有同步修改manifest
    - compactBuildTables函数逻辑
      - 分析subcompact函数逻辑
        - 分析updateStats
        - 分析exceedsAllowedOverlap
        - 分析addKeys
          - firstKeyHasDiscardSet
          - isExpired
  - pickCompactLevels
  - levelTargets
  - add sst
  - levelHandler
  - compactStatus
    - levelCompactStatus
      - keyRange
- levelHandler --- ok
- draw compact flow --- ok

## manifest

- init
- format
- compressionType[snappy/ZSTD]
- draw format

## GC

- gc触发(RunValueLogGC)
- gc流程
  - pickLog
  - doRunGC
- discardStats
- discardStats结构与使用
- discard比例

## tnx

- 事务处理逻辑

## encrypt

- keyregistry
