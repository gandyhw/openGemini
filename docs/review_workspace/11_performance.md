# openGemini 性能瓶颈分析报告

**目标**: 1 亿 metrics/s 写入、10 亿 logs/s 写入
**分析方式**: 全代码静态推断（Go 1.24 toolchain 不可用，无法运行 benchmark）
**Git commit**: b3fc3714

---

## 写入路径热点函数（按预估 CPU/mem 占比排序）

| 排名 | 位置 | 函数 | 瓶颈机理 | 优化路径 | 预估提升 |
|------|------|------|---------|---------|---------|
| 1 | `engine/index/tsi/mergeset_index.go:124-160` | `CreateIndexIfNotExistsByRow` | 4 重: (a) 行级加锁写入 mergeset Bloom Filter (b) 每行计算 TSID (c) goroutine 队列切换开销 2*CPU 个 channel (d) bloom/v3 库的 hash 计算。每个 row 的 tag KV 都要写入 inverted index。 | (1) 批处理: 累计 N 行后批量写入 inverted index (2) 改用 roaring bitmap 替代 mergeset (3) 避免每个 row 都计算 TSID | 3-5x |
| 2 | `engine/shard.go:685-707` | `writeRows` (memtable + WAL) | 串行执行: 先写 memtable（`MTable.WriteRows`），再写 WAL（`wal.Write`）。WAL 使用 snappy 压缩每条记录，IO 路径串行阻塞。 | (1) WAL 异步化: memtable 写完成后立即返回，WAL 异步写 (2) WAL 批量压缩: 积攒多条再 snappy encode (3) 预写日志改用 LZ4（更快压缩） | 2-3x |
| 3 | `engine/mutable/table.go:170-196` | `WriteRows` (tsMemTableImpl) | 每行 `s.MTable.WriteRows` 内需要: (a) 按 measurement 分组用 `dictpool.Dict` (b) clone row (c) 按 sid 定位 `WriteChunk` (d) 每个 chunk 有独立 mutex。高 cardinality 下 sid 膨胀导致大量小 chunk 竞争。 | (1) 改用 lock-free 的 per-shard write buffer (2) 按 measurement 批量 flush (3) 减少 row clone 次数 | 2x |
| 4 | `engine/shard.go:592-640` | `writeRowsToTable` | 串行执行: `mapRows`→`cloneRowToDict`→`allocResource`→`WriteIndex`→`writeRows`→`wait()(index callback)`。`WriteIndex` 和 `writeRows` 可以并行但当前是串行的。 | (1) index building 和 memtable write 流水线并行 (2) 预分配 mem size 避免 allocResource 瓶颈 | 1.5-2x |
| 5 | `coordinator/points_writer.go:227-310` | `routeAndMapOriginRows` | 每行执行: 时间范围检查、字段排序去重、类型检查、measurement 创建、schema 更新、shard key 路由。全是 row-by-row 不可批量的操作。 | (1) 使用预编译的 schema cache (2) bypass schema check 在已知 schema 后 | 1.3-1.5x |
| 6 | `coordinator/points_writer.go:343-360` | `writeShardMap` | 每个 shard 一个 goroutine，goroutine 之间共享 `mutex` 收集错误。大量写入时产生大量 goroutine 调度开销。 | (1) 使用 worker pool 限制并发 (2) 批量聚合 shard 内数据 | 1.2x |

**静态推断假设**: 以上排序基于单次 invocation 的指令数估计，未考虑 cache miss、分支预测、CPU pipeline 等微架构因素。实际 profile 可能因数据特征（cardinality，batch size）而不同。

### 10 亿 logs/s 是否需要前置消息队列解耦？

**结论: 必须。** 当前架构完全不支持。

- **无内置消息队列**: 搜索 `kafka|pulsar|message.*queue|mq` 仅发现 Kafka protocol 兼容的消费服务 (`services/consume/kafka/server.go`)，用于 **数据消费（读）**，不是写缓冲。
- **串行写入瓶颈**: 当前 `WriteRows → memtable → WAL` 是同步串行调用路径。1B logs/s × 300 bytes/log = 300 GB/s 原始数据，单节点任何架构都无法处理。
- **推荐架构**: 前置 Kafka/Pulsar 作为 write-ahead buffer，openGemini 从 topic 消费批量写入。当前无预留接口, `PointsWriter` 直接接受 `[]influx.Row`，需要新增 consumer connector。
- **Parquet 导出**: `engine/immutable/task_parquet.go` 已有 parquet export 能力，可用于冷数据归档。

---

## 序列化/反序列化 CPU 占比推算

### Influx Line Protocol 解析
- **文件**: `lib/util/lifted/vm/protoparser/influx/parser.go`
- **来源**: VictoriaMetrics 的定制解析器，非正则表达式。逐字符扫描 + `fastfloat.Parse` 解析数值。
- **推算**: 每条 metrics 约 150 bytes line protocol → 解析耗时 ~500ns（基于 Go 文本解析基准），100M/s → 50 核 CPU 仅解析，不可接受。
- **优化**: 使用原生 protobuf 协议替代 line protocol 可降 60-70%（跳过了文本->二进制转换）。

### OTLP Protobuf 反序列化
- **文件**: `lib/opentelemetry/otlp_writer.go:73-107`
- **路径**: `ptrace.UnmarshalProto` / `pmetric.UnmarshalProto` / `plog.UnmarshalProto` → `otel2influx` 转换 → `influx.Row`
- **瓶颈**: 双重转换: OTLP proto → OTLP pdata struct → influx Row。每个行都要 `EnqueuePoint` 做 tag/field 转换。
- **推算**: OTLP trace/metrics 比 line protocol 解析更慢 ~1.5-2x，因为 struct 嵌套层次更深。

### JSON 使用
- **场景**: 仅元数据序列化（备份、meta client 通信、syscontrol），不经过写入热路径。非瓶颈。

### Protobuf 调用密度
- 搜索 `proto.Marshal`/`proto.Unmarshal` 集中在 `lib/metaclient/` 和 `lib/util/lifted/influx/meta/`
- **写入热路径中几乎无 protobuf**（除 OTLP 写入外），这是优点。
- `netstorage.MarshalRows` 用于节点间传输，`lib/netstorage/storage.go`。

### 总序列化占比估算

| 场景 | 当前 | 占比估算 |
|------|------|---------|
| Metrics (line protocol) | 单次解析 ~500ns | 写入总 CPU 的 15-20% |
| Logs (line protocol) | 单次解析 ~800ns (字符串更长) | 写入总 CPU 的 20-30% |
| OTLP metrics | ~1us (双重转换) | 写入总 CPU 的 30-40% |
| 节点间 protobuf 传输 | `MarshalRows` | 网络层 ~10% |

**静态推断假设**: 基于 Go 文本解析、protobuf 解析的典型性能数据估算，非本仓库实测值。

---

## LSM Compaction 写放大推算

### 配置参数
- **Levels**: 7 (`CompactLevels`, `engine/immutable/compact.go:30`)
- **LevelCompactRule**: `{0, 1, 0, 2, 0, 3, 0, 1, 2, 3, 0, 4, 0, 5, 0, 1, 2, 6}` (18 步)
- **Level Compact For CS**: `{0, 1, 0, 1, 0, 1}` (6 步)
- **MinGroupFiles**: `[8, 4, 4, 4, 4, 4, 2]`
- **File size limit**: 8GB (`DefaultFileSizeLimit`, `lib/util/util.go`)
- **maxCompactor**: CPU 核数 (上限 32)
- **maxFullCompactor**: CPU 核数 / 2

### 写放大推算表

| Level | 触发文件数 | 合并策略 | 单次 WA | 出现频次/轮次 | 摊销 WA |
|-------|-----------|---------|---------|--------------|--------|
| 0→1 | ≥8 个 L0 文件 | 归并多个 L0 碎片到 L1 有序文件 | ~8x | 6次/完整轮次 | ~6 |
| 1 (solo) | ≥4 个 L1 文件 | L1 内部合并 | ~4x | 3次/完整轮次 | ~1.5 |
| 2 | ≥4 个 L2 文件 | L2 内部合并 | ~4x | 1次/完整轮次 | ~0.5 |
| 3 | ≥4 个 L3 文件 | L3 内部合并 | ~4x | 1次/完整轮次 | ~0.5 |
| 4 | ≥4 个 L4 文件 | L4 内部合并 | ~4x | 1次/完整轮次 | ~0.5 |
| 5 | ≥4 个 L5 文件 | L5 内部合并 | ~4x | 1次/完整轮次 | ~0.5 |
| 6 | ≥2 个文件 | major compaction 到最终 level | ~2x | 1次/完整轮次 | ~0.3 |
| **L0→L1+L1→L2→L6** | chain merge | 跨 level 链式合并 | 6-14x | 2次/完整轮次 | ~2 |

**TSStore 写放大下限**: ~10x (每次 compact 至少写 10 次)
**TSStore 写放大上限**: ~18x (频繁 L0→L1 compaction + chain merge)
**ColumnStore 写放大**: ~2-3x (仅 L0→L1，无层次合并)

**对比 RocksDB (leveled, 7 levels)**: 典型 WA 6-12x。openGemini 的 7 级 + 复杂调度规则导致上限更高。主要原因是 L0→L1 在每轮中出现 6 次，说明 L0 多次生成碎片再合并。

**对 1 亿 metrics/s 的影响**: 若写入 5 GB/s 原始数据，压缩前 storage bandwidth 需求: 5 GB/s × 12 WA = 60 GB/s，需要 12+ NVMe SSD 才能维持。写入寿命消耗严重。

**静态推断假设**: 基于 `LeveLMinGroupFiles` 和 `LevelCompactRule` 的组合逻辑推算，未动态运行 compaction 调度。实际 WA 取决于数据分布和写入速率。

---

## 列存压缩比场景评估

### 使用算法
| 算法 | 位置 | 适用类型 | 压缩比 (典型) |
|------|------|---------|-------------|
| Gorilla (XOR) | `lib/compress/compress.go:64-80` | float64 时间序列 | 12-15x |
| Snappy | `lib/compress/compress.go:40-57` | 通用 (WAL, string) | 2-4x |
| RLE | `lib/compress/compress.go:22-38` | 重复整数 | 10-100x(*) |
| Padding_zero | `lib/compress/compress.go:104-108` | 稀疏数据 | 可变 |

(*) RLE 压缩比取决于重复值比率

### 列存布局
- maxRowsPerSegment: TSStore 1000, ColStore 8192 (`lib/util/util.go`)
- maxSegmentLimit: 256K (`DefaultMaxSegmentLimit4ColStore`)
- fileSizeLimit: 8GB
- 列存使用 chunk-based 列式存储 (`engine/immutable/colstore/`)
- PK 文件自定义格式 (magic "COLX") (`engine/immutable/colstore/reader.go`)

### 日志场景压缩比评估

| 日志字段 | 类型 | 高基数? | 压缩算法 | 预估压缩比 | 理由 |
|---------|------|---------|---------|-----------|------|
| Timestamp | int64 | 低 | 自研浮点/delta | >10x | 相邻时间戳差异小 |
| Severity/Level | string | 低 (≤10) | RLE | >20x | 值域极小 |
| Service name | string | 中 (100-1000) | Snappy | 3-5x | 重复率高 |
| Message body | string | 极高 | Snappy | 2-3x | 每个 log 都不同 |
| Resource tags | kv pairs | 极高 | Snappy | 2-3x | key 可压缩, value 各异 |
| Trace/span ID | string | 极高 | Snappy | ~1x | 随机字符串不可压 |

**整体日志压缩比估计: 2-4x**（主要受 message body 和随机 ID 限制）

**与列存 TSBS benchmark 对比**: TSBS 时序基准对 metrics 数据压缩比 8-15x，日志数据固有可压缩性更差，列存优势不如 metrics 明显。长字符串是列存的弱点（随机的长字符串列存 vs 行存几乎无差别）。

### 列存对日志的优势仍然存在:
1. **只读需要的列**: 查询 "only count error logs by service" 只需读 severity + service 两列，跳过大 body
2. **同列同类型**: 更好的 CPU cache 利用
3. **Batch processing**: 列式 layout 对 batch 操作友好

**静态推断假设**: 压缩比基于公开的时序/日志压缩数据估算，非本仓库实测值。

---

## 向量化算子覆盖率清单

### 执行引擎架构
- 核心数据结构: `Chunk` (列式, `engine/executor/chunk.go`)
- 列类型: `Column`（代码从模板生成 `column.gen.go.tmpl`）
- 处理模式: Chunk 通过 channel 在算子间传递，算子内部按行遍历

### 算子清单

| 算子 | 文件 | 处理模式 | 向量化? | 证据 |
|------|------|---------|---------|------|
| FilterTransform | `engine/executor/filter_transform.go:154-189` | 外层 chunk 批处理, 内层 `for i := 0; i < c.NumberOfRows(); i++` 逐行 | **否** | 逐行调用 `ValuerEval.EvalBool(cond)` |
| HashAggTransform | `engine/executor/hash_agg_transform.go` | chunk 批处理 + per-chunk agg iteration | **部分** | 聚合函数按列计算, 但 group by 逐行 hash |
| GroupByTransform | `engine/executor/groupby_transform.go` | chunk 批处理 | **部分** | 列式但无 SIMD |
| SortTransform | `engine/executor/sort_transform.go` | 多路归并排序, 列式 | **否** | 排序比较逐行 |
| MergeTransform | `engine/executor/merge_transform.go` | 多路归并 | **否** | 逐行比较 |
| TopNTransform | `engine/executor/topn_transform.go` | batch-based group compute | **部分** | batch 分组的聚合计算 |
| MaterializeTransform | `engine/executor/materialize_transform.go:1445` | `AppendRowValue(dst, value)` 逐行处理 | **否** | 显式逐行 AppendRowValue |
| LimitTransform | `engine/executor/limit_transform.go` | chunk 截断 | **N/A** | 几乎无计算 |
| FillTransform | `engine/executor/fill_transform.go` | 逐行填充 null | **否** | |
| IncAggTransform | `engine/executor/inc_agg_transform.go` | chunk-level 增量聚合 | **部分** | 聚合列操作 |
| AggIterator | `engine/executor/agg_iterator.go` | 遍历 iteration | **否** | 逐行 callback |
| ChunkArrowTransform | `engine/executor/chunk_arrow_transform.go` | Chunk ↔ Arrow | **N/A** | 序列化 |
| IndexScanTransform | `engine/executor/index_scan_transform.go` | 索引扫描 | **否** | |
| FullJoinTransform | `engine/executor/full_join_transform.go` | hash join / merge join | **否** | 逐行匹配 |
| DDCM (Deep Cardinality) | `engine/executor/ddcm.go` | Count-min sketch | **部分** | hash vector 批量计算 |
| PromRangeVector | `engine/executor/prom_range_vector_transform.go` | PromQL range vector | **否** | 逐窗口 |
| CTETransform | `engine/executor/cte_transform.go` | 通用 | **否** | |

### 向量化结论
- **SIMD/向量化指令: 零使用**。全仓库搜索 `SIMD|simd|avx|sse` 无结果。
- **Column/chunk 批处理**: Chunk 作为批处理单元在算子间传递，但算子内部仍是逐行循环（`for i := 0; i < c.NumberOfRows(); i++`）。
- **仅"批量": 有**（batch/chunk-level 操作）。**"向量化": 无**（无 SIMD 指令、无数据级并行）。
- **代码生成**: `column.gen.go.tmpl` 通过模板生成类型特化代码，减少了 interface boxing，但生成的是标量代码。

**对 1 亿 metrics/s 的影响**: 全标量执行意味着 CPU IPC ~1-2，而 SIMD 向量化可以实现 IPC 8+。filter/aggregation 如果向量化预计提升 3-6x。

**静态推断假设**: 未使用 `go tool asm` 验证指令输出，基于源码中无 SIMD 函数/库导入和纯 Go 循环模式推断。

---

## Go Runtime 开销评估

### GC Pressure 分析

| 压力来源 | 位置 | 严重程度 | 说明 |
|---------|------|---------|------|
| 每个 row 的 tag/field 切片分配 | `influx.Row` struct，写入时大量分配 | **高** | 每行都有 `Tags []Tag`, `Fields []Field` |
| `dictpool.Dict` 行分组 | `engine/shard.go:mapRows`→`cloneRowToDict` | **中** | Pool 复用减轻但仍有分配 |
| Chunk 对象 | `engine/executor/chunk.go`, `ColumnImpl` | **中** | 使用 `CircularChunkPool` 池化 |
| Index 条目 | `mergeset` inverted index 写入 | **高** | 每 tag KV 创建新的 index entry |
| WAL 缓冲区 | `engine/wal.go` 的 `walCompBufPool` | **低** | 良好池化 |

### sync.Pool 使用密度
- **总数**: ~47 个在 `engine/` + `coordinator/` 热路径，全仓库 >71 个文件引用
- **关键池**:
  - `injestionCtxPool` (`coordinator/points_writer.go:51`) — 写入上下文
  - `walRowsObjectsPool` (`engine/wal.go:47`) — WAL 行对象
  - `mstWriteCtxPool` (`engine/shard.go:961`) — measurement 写上下文
  - `indexRowsPool` (`engine/index/tsi/index_builder.go:93`) — 索引行
  - `columnSortHelperPool` (`lib/record/column_sort.go:28`) — 列排序辅助
  - `fileReaderPool` (`engine/immutable/colstore/reader.go:30`) — 文件读取器
- **评估**: Pool 使用密集且合理，但部分 Pool 使用 `interface{}` 装箱，有 GC 压力。少部分 Pool 无 `New` 函数，冷启动分配频繁。

### Goroutine 泄漏风险

| 风险点 | 位置 | 问题 | 风险 |
|-------|------|------|------|
| Executor workers | `engine/executor/` 多个文件 | `go func()` 使用 `ctx.Done()` 但部分算子没有（如 `limit_transform.go:229`） | **中** |
| Index 构建 goroutines | `engine/index/tsi/mergeset_index.go:124-160` | 从 channel 消费，channel 关闭时退出，正确 | **低** |
| WAL 后台 goroutines | `engine/wal.go` 多处 | 使用 `struct{}{}` channel 而非 context，关闭逻辑分散 | **中** |
| Compaction goroutines | `engine/immutable/merge_out_of_order.go:47` | 使用 `compLimiter` 限制 + `m.closed` channel，基本安全 | **低** |
| Kafka 服务 | `services/consume/kafka/server.go` | 每个连接一个 goroutine，无上限限制 | **高** (大规模下) |
| Snapshot goroutines | `engine/shard.go:1213` | ticker + closed channel | **低** |

**总 goroutine 数**: 全仓库非 test 代码中 134 个 `go func()` 启动点。

### 内存管理策略
- `lib/memory/sysmemory.go`: 通过 `gopsutil/mem` 读取系统内存信息，`ReadSysMemory()` 每 100ms 缓存一次
- `lib/memory/sysmemory_monitor.go`: 监控接口 `MemUsedPct()`/`SysMem()`
- 节点级 `nodeMutableLimit` 实现写入限流（`engine/shard.go:writeRowsToTable` 中的 `allocResource`）
- **除 write token 限流外，没有进程级 OOM 保护机制**

### Go Runtime 总评
- **GC 是潜在瓶颈**: 日志场景下大量 string 字段 → heap 分配大幅增加 → GC STW 时间增长。1B logs/s 每行 ~300 bytes heap → 300 GB/s 分配速率 → GC 完全无法处理。需要 object pooling 或 off-heap 内存。
- **建议**: (1) 引入 off-heap / mmap 管理 string 数据 (2) 使用 `runtime.GC` 调优（`GOGC` 调整）(3) 关键路径零分配（零分配解析器）

**静态推断假设**: GC 开销估算基于 Go runtime 的一般特性（约每 2MB 分配触发一次 GC），未对仓库做 `pprof` 分析。

---

## 规模缺口表

### 单节点（32 核 / 256GB RAM / NVMe RAID）推算

| 指标 | 当前(推算) | 目标 | 缺口倍数 | 单点优化能否填平? |
|------|-----------|------|---------|-----------------|
| Metrics 写入吞吐 | ~3-5M metrics/s | 100M/s | **20-33x** | **不能** (需要 20-30 节点) |
| Logs 写入吞吐 | ~0.5-1M logs/s | 1B/s | **1000-2000x** | **绝对不能** (需要 100+ 节点 + 架构重构) |
| 写入延迟 (p99) | ~10-50ms | <10ms | 1-5x | 部分可以 (WAL 异步化 + 批处理) |
| 查询吞吐 (简单过滤) | ~100K qps | 1M qps | **10x** | 部分可以 (向量化 3-5x + 索引加速) |
| 压缩比 (metrics) | 8-12x | >10x | ~1x | 已达标 |
| 压缩比 (logs) | 2-4x | >5x | ~2x | 需要专用日志压缩 |

### 推算依据

**Metrics (100M/s)**: 每行 ~3 fields + 3 tags + timestamp ≈ 150 bytes, 处理链: parse(500ns) + index build(1us) + memtable(200ns) + WAL encode(300ns) ≈ 2us/row, 单核约 500K row/s, 32核约 16M row/s。reachability = 16% 目标。

**Logs (1B/s)**: 每行 ~200 bytes string + 5 tags ≈ 300 bytes, 处理链更长(字符串处理 2-3x 成本): parse(800ns) + index build(2us) + memtable(500ns) + WAL(1us) ≈ 4.3us/row, 单核约 230K row/s, 32核约 7M row/s。reachability = 0.7% 目标。

### 目标缺口填补策略

| 策略 | Metrics (100M/s) | Logs (1B/s) |
|------|-----------------|-------------|
| 水平扩展 (100 节点) | 3-5M/s × 20 = 100M/s | 1M/s × 200 = 200M/s **依然不够** |
| 优化 3x | 16M × 3 = 48M/s **还不够** | **架构层面不足** |
| 优化 3x + 100 节点 | 48M × 100 = 4.8B/s ✓ | 但日志还需要 5x 更优极限 |

**结论**: 
- **Metrics 目标**在 100 节点集群 + 3x 单点优化后可行
- **Logs 目标**需要: (a) 1000+ 节点集群 (b) 专用日志压缩 (c) 前置消息队列解耦 (d) 架构从 push-based 改为 pull-based ingestion。当前架构 **无法胜任**。

---

## 自带 Benchmark 覆盖度分析

### Top Benchmark 函数

| 排名 | 位置 | 函数名 | 测试目标 |
|------|------|--------|---------|
| 1 | `coordinator/points_writer_test.go` | `Benchmark_WritePointRows` | 写入端到端行处理 |
| 2 | `coordinator/points_writer_test.go` | `Benchmark_UnmarshalShardKey` | Shard key 反序列化 |
| 3 | `coordinator/points_writer_test.go` | `Benchmark_UnmarshalShardKeyByTagOp` | Tag 操作 Shard key |
| 4 | `coordinator/points_writer_test.go` | `Benchmark_selectIndexList` | 索引选择 |
| 5 | `coordinator/stream_test.go` | `Benchmark_Map_Write` | map 写入性能 |
| 6 | `coordinator/stream_test.go` | `Benchmark_Dict_Write` | dict 写入性能 |
| 7 | `coordinator/stream_test.go` | `Benchmark_Map_Read` | map 读取性能 |
| 8 | `coordinator/stream_test.go` | `Benchmark_Dict_Read` | dict 读取性能 |
| 9 | `lib/compress/compress_test.go` | `BenchmarkRLEData` | RLE 压缩编码 |
| 10 | `lib/compress/compress_test.go` | `BenchmarkSameData` | 相同值压缩 |
| 11 | `lib/compress/compress_test.go` | `BenchmarkRandData` | 随机 float 压缩 |
| 12 | `lib/compress/compress_test.go` | `BenchmarkRandIntData` | 随机 int 压缩 |
| 13 | `services/fence/fence_test.go` | `BenchmarkParseFloats` | 浮点数解析 |
| 14 | `services/fence/fence_test.go` | `BenchmarkCoverCells` | fence cell 覆盖 |
| 15 | `services/fence/fence_test.go` | `BenchmarkFenceCheck` | fence 检查 |

### 覆盖度分析

| 覆盖维度 | 状态 | 缺什么 |
|---------|------|-------|
| **写入吞吐** | 只有 `Benchmark_WritePointRows` | 无 memtable 单测、无 WAL 吞吐、无 index build 性能 |
| **解析性能** | **缺失** | 无 line protocol parser benchmark，无 OTLP protobuf 解析 benchmark |
| **压缩性能** | 部分覆盖 (RLE/Gorilla/Snappy) | 无列存压缩比 benchmark，无混合数据压缩 |
| **Compaction** | **缺失** | 无 compaction 吞吐、写放大 benchmark |
| **查询性能** | **缺失** | 无 filter、aggregation、join benchmark |
| **向量化** | **缺失** | 无算子性能对比 |
| **内存分配** | **缺失** | 无 allocation profiling benchmark |
| **高基数** | **缺失** | 无高 cardinality 场景 benchmark |
| **日志场景** | **缺失** | 全部是数值/metrics 数据 |
| **端到端** | **缺失** | 无 rows→memtable→WAL→flush→compact 全链路 |

### 总评
现有 318 个 Benchmark 函数主要集中在 coordinator layer 和 compression lib，**完全缺失存储引擎核心路径（memtable、WAL、compaction、index build）的 benchmark**。对于 1 亿 metrics/s 目标，当前的 benchmark 覆盖率不到 20%。

### 建议新增 benchmark
1. `engine/mutable/` - Memtable write throughput (不同 batch size, cardinality)
2. `engine/wal.go` - WAL compress+write throughput
3. `engine/index/tsi/` - IndexCreate throughput (不同 tag 数)
4. `lib/util/lifted/vm/protoparser/influx/` - Line protocol parser throughput
5. `engine/immutable/` - Compaction throughput、写放大实测
6. 全链路: 从 `coordinator/PointsWriter.RetryWritePointRows` 到 memtable flush

---

## 综合结论

### 架构层面的关键瓶颈（按严重程度）

1. **同步串行写入架构**: `memtable → WAL` 串行、`WriteIndex → memtable → wait(index)` 串行。10 亿 logs/s 完全不可行。
2. **无 SIMD/向量化**: 全标量执行，CPU IPC 低。filter/aggregation 至少 3-6x 提升空间。
3. **无写入缓冲解耦**: 无 Kafka/Pulsar 接口。写后端没有 burst absorption 能力。
4. **Compaction 写放大过高**: TSStore 7 级 → 10-18x WA，10 亿 logs/s 对应 300 GB/s × 12 = 3.6 TB/s IO，远超 NVMe 阵列能力。
5. **Go GC 压力**: 日志场景大量 string → heap 暴涨。单点 GC 无法处理 1B/s 的 string allocation。

### 快速取胜（6 个月内）
- [ ] Index + memtable 写入流水线并行（`WriteIndex` 和 `writeRows` 并发）
- [ ] WAL 异步写入（返回成功不需要等待 fsync）
- [ ] WAL 批量压缩替代逐条压缩
- [ ] Row-level 过滤向量化（filter transform SIMD-enable per列）
- [ ] 列存下减少 row clone（zero-copy path 写入）

### 需要架构重设计的领域（12 个月+）
- [ ] Pull-based ingestion 架构（Kafka consumer + 批量 flush）
- [ ] Off-heap string 管理（减少 GC 压力）
- [ ] 专用日志压缩（字典压缩 + delta encoding for string）
- [ ] 减少 LSM levels 到 3-4 层，降低写放大
- [ ] SIMD 向量化执行引擎

---

*报告基于 commit b3fc3714 的代码静态推断。所有估算标注了"静态推断假设"。运行环境 Go 1.24 toolchain 不可用，无法完成 `go test -bench` 验证。*
