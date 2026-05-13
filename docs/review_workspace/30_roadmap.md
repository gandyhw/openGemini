# 30_roadmap · 重构路线图 (0/3/6/12 月)

> 分三阶段推进: 止血 → 加固 → 重构
> 每阶段可独立验收，逐步提升目标规模承载力

---

## Phase I · 紧急止血 (0–3 月)

**目标**: 消除致命风险，可运维，可观测

| # | 条目 | 涉及模块 | 依赖 | 验收标准 | 预估人力 |
|---|------|---------|------|---------|---------|
| I-1 | **启用默认脑裂防护** | `engine/fence.go` | 无 | non-streamfs 构建下 fencer 提供基础文件锁保护；压测验证无脑裂 | 1-2 人周 |
| I-2 | **WAL 同步 fsync 模式** | `lib/raftlog/storage.go` | 无 | 提供 "sync"/"async" 两种配置；sync 模式下每次写入后 `TrySync`；压测验证 crash 丢数据 ≤1ms 窗口 | 1-2 人周 |
| I-3 | **磁盘水印保护** | `engine/shard.go`, `engine/wal.go` | 无 | 写入前检查剩余空间；磁盘满时优雅拒绝返回 `DiskFull`；压测磁盘满场景不 panic | 2-3 人周 |
| I-4 | **端到端 context 超时传播** | `coordinator/`→`engine/executor/` | 无 | HTTP timeout → coordinator → engine scan 全程 context 传播；慢查询可被 kill；集成测试 | 2-3 人周 |
| I-5 | **FSM panic 转 error** | `app/ts-meta/meta/store_fsm.go` | 无 | Raft FSM 中所有 12 处 panic 改为 error return + 结构化日志；故障注入测试 | 2-3 人周 |
| I-6 | **Prometheus metrics 端点** | `lib/statisticsPusher/`, `lib/metrics/` | 无 | 暴露 `/metrics` 端点；引擎/core/查询统计对接 Prometheus；Grafana dashboard 模板 | 2-3 人周 |
| I-7 | **新增核心 benchmark** | `engine/mutable/`, `engine/wal.go`, `engine/index/tsi/` | Go 1.24 toolchain | memtable/WAL/index build 吞吐量 benchmark 就绪；基线数据可查 | 2-3 人周 |
| I-8 | **Per-query memory budget** | `engine/executor/processor.go` | I-4 | 替换全局 85% 内存阈值；支持 per-query 内存上限配置；大查询被杀时不误杀小查询 | 2-3 人周 |

**阶段验收**: 5 个致命风险 (F1/F2/F3/F4/F7) 全部关闭；单节点 benchmark 数据就绪；可对外暴露 Prometheus 指标。

---

## Phase II · 核心加固 (3–6 月)

**目标**: 日志场景可用、备份自动化、查询性能 2-3x 提升

| # | 条目 | 涉及模块 | 依赖 | 验收标准 | 预估人力 |
|---|------|---------|------|---------|---------|
| II-1 | **Ingestion Gateway POC** | 新建 `app/ts-ingest/` | I-4 | 独立网关进程接受 Line Protocol/OTLP；Kafka consumer connector；5 节点集群压测 10M metrics/s | 4-6 人月 |
| II-2 | **日志专用存储路径** | `engine/` 新增 log shard type | I-7 | 日志数据物理隔离 metrics；内置 CLV 全文索引；日志查询支持 keyword/正则/全文 3 种模式 | 4-6 人月 |
| II-3 | **日志压缩优化** | `lib/compress/`, `engine/immutable/` | II-2 | 字典压缩 + delta encoding for string；日志压缩比 2-4x → 5-8x；混合日志压测验证 | 2-3 人月 |
| II-4 | **自动备份调度** | `services/backup/` (新建) | 无 | cron 表达式调度；全量/增量自动切换；定期恢复演练；恢复后一致性校验 | 2-3 人月 |
| II-5 | **写入 admission control** | `coordinator/points_writer.go` | I-4 | Token bucket rate limiter；有界写入缓冲队列；backpressure signal 向 HTTP handler 传播 | 2-3 人月 |
| II-6 | **Compaction 策略优化** | `engine/immutable/compact.go` | I-7 | 日志 shard 使用专用精简 compaction rule (≤3 level)；实测 WA ≤5x；metrics 保持当前策略 | 2-3 人月 |
| II-7 | **运维基础能力** | `app/ts-server/` | 无 | Graceful shutdown (drain in-flight writes)；版本兼容性检查；SIGHUP 配置热加载 | 2-3 人月 |
| II-8 | **审计日志** | `app/ts-meta/meta/` | 无 | 元数据变更记录 (谁/何时/改了什么)；输出到结构化日志或 Kafka topic | 1-2 人月 |

**阶段验收**: 日志 10M/s 写入不丢数据 + 全文检索可用；备份 RPO ≤ 1h, RTO ≤ 2h；查询延迟降低 2-3x。

---

## Phase III · 架构重构 (6–12 月)

**目标**: 元数据扩展至 10 万实体、跨模 RCA 闭环、1 亿 metrics/s + 1B logs/s 写入

| # | 条目 | 涉及模块 | 依赖 | 验收标准 | 预估人力 |
|---|------|---------|------|---------|---------|
| III-1 | **跨模 join 算子** | `engine/executor/` | II-1, II-2 | HashJoin/MergeJoin 支持跨 measurement 关联；metric+log 关联查询 p99 < 5s | 4-6 人月 |
| III-2 | **本地拓扑图引擎** | `engine/graph/` (新建) | III-1 | 1M 拓扑边持久化存储 + 分片；k-hop / 最短路径 / 影响面算法；图查询需在 5s 内返回 | 6-8 人月 |
| III-3 | **元数据分组/分层** | `app/ts-meta/`, `lib/metaclient/` | III-2 | 拓扑元数据 vs 数据字典分开 Raft 组；元数据缓存 push-based (etcd watch)；100K 实体压测稳态延迟 <10ms | 6-8 人月 |
| III-4 | **向量化执行引擎** | `engine/executor/` | I-7 | Filter/Aggregate/TopN 算子 SIMD 向量化；基准: filter 3x, aggregate 4x, topn 2x 提升 | 4-6 人月 |
| III-5 | **Off-heap 内存管理** | `lib/memory/` | II-1, II-2 | mmap 管理日志 string 数据；Go heap 分配降低 70%+；GC STW < 10ms 在日志高负载下 | 4-6 人月 |
| III-6 | **滚动升级框架** | 全组件 | II-7 | staged shutdown → 新版本启动 → 流切换；蓝绿/金丝雀部署支持；升级期间写入不中断 | 3-4 人月 |
| III-7 | **多租户隔离** | `lib/config/limits.go` | II-5 | Per-tenant QPS 限流 + 存储配额 + 查询优先级；租户间完全隔离 (不同 shard group) | 3-4 人月 |
| III-8 | **原生 Trace 查询 (TraceQL)** | `lib/opentelemetry/` | III-1 | Span 索引独立；支持按 trace_id 检索完整 trace；span event 完整保留；与 metrics/logs 关联查询 | 4-6 人月 |
| III-9 | **RCA 算法扩展** | `engine/executor/rca.go` | III-2, III-4 | 新增 FaultPropagation(传播链) + 概率排序 + 多根因分析；RCA Top-1 准确率 >80% (合成数据验证) | 3-4 人月 |
| III-10 | **Decommission 自动化** | `app/ts-meta/`, `coordinator/` | III-3, III-6 | 自动 PT 迁移 → 数据一致性校验 → 节点下线；节点下线时间 < 30s | 2-3 人月 |

**阶段验收**: 全链路压测:
- 1 亿 metrics/s 写入 (100 节点集群)
- 10 亿 logs/s 写入 (1000 节点集群 + Kafka 前置)
- 跨模关联查询 (metric→trace→log→topology) p99 < 5s
- ts-meta 稳态 100K 实体延迟 <10ms
- 1M 拓扑边 k-hop 查询 < 5s

---

## 总人力估算

| 阶段 | 时长 | 核心工程师 | 主要产出 |
|------|------|-----------|---------|
| Phase I | 0–3 月 | 3-4 人 | 消除致命风险, benchmark 基线 |
| Phase II | 3–6 月 | 5-7 人 | 日志可用, 备份自动化, 查询优化 |
| Phase III | 6–12 月 | 8-12 人 | 元数据扩展, 跨模 RCA, 目标规模 |

**总人月估算**: ~60-80 人月 (Phase I: ~10, Phase II: ~20-30, Phase III: ~30-40)
