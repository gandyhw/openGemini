# 工程质量与 RCA 能力评审报告

## 1. 工程质量评分卡 (1-5 分)

| 维度 | 评分 | 证据 |
|------|------|------|
| 测试覆盖 | 3/5 | 518 个测试文件 vs 1073 个非测试 Go 文件，核心模块测试较充分但稀疏索引/拓扑模块测试不足 |
| 文档完整 | 2/5 | 核心类型缺乏 Go 文档注释，EngineImpl 及其大多数方法无 doc comment |
| 接口设计 | 3/5 | 整体架构清晰(engine/coordinator/services)，但错误处理模式不统一 |
| 错误处理 | 2/5 | 596 个 panic(非测试)，95 个 recover，大量业务逻辑用 panic，含 Raft 关键路径 |
| 静态质量 | 3/5 | RC执 行器中有 RCA 代码但缺测试，存在可观察性埋点但覆盖面有限 |

### 测试覆盖详细分析

**测试文件核心分布:**

- `engine/executor/` — 68 个测试文件 (最多)
- `engine/immutable/` — 37 个 (存储引擎核心)
- `engine/` — 30 个
- `lib/statisticsPusher/statistics/` — 27 个
- `app/ts-meta/meta/` — 20 个
- `lib/config/` — 12 个
- `coordinator/` — 10 个
- `lib/metaclient/` — 9 个
- `engine/index/sparseindex/` — 9 个

**关键模块测试情况 (Test 函数 vs 源文件函数):**

- `coordinator/points_writer.go` — 32 个函数, 37 个测试函数
- `engine/engine.go` — 96 个函数, 80 个测试函数 (`engine/engine_test.go`)
- `engine/executor/graph.go` — 18 个函数, 3 个测试函数(`graph_test.go`)
- `engine/executor/graph_transform.go` — 12 个函数, 2 个测试函数(`graph_transform_test.go`)
- `engine/executor/rca.go` — 7 个关键函数, 1 个测试函数 `TestRCA`
- `services/retention/service.go` — 15 个函数, 7 个测试函数

**集成测试:**
- `tests/server_test.go` — 177 个集成测试用例 (HTTP API 级别)
- `tests/server_write_test.go` — 写入集成测试
- `tests/server_colstore_test.go` — 列存储集成测试
- `tests/server_continuous_query_test.go` — 连续查询集成测试

**混沌/故障注入测试:**
- 存在 `failpoint` 使用(如 `coordinator/points_writer.go`, `engine/shard.go`, `engine/wal.go` 等)
- 但仅约 15 个文件使用 failpoint, 未发现独立 chaos 测试套件
- 未搜索到 "chaos", "fault injection" 独立测试文件

### godoc 完整度抽样

核心类型概述:
- `lib/util/lifted/influx/influxql/ast.go` — 大多数 Statement 类型有 `// XxxStatement represents a command for ...` 注释, 但有些缺失(如 `CreateMeasurementStatement`, `DeleteSeriesStatement`, `DropSeriesStatement`)
- `engine/engine.go` — `EngineImpl` 只有原始定义无 godoc, 其 60+ 个方法中仅 `getShard`, `checkAndGetDBPTInfo` 等少数有单行注释
- `lib/metaclient/meta_client_impl.go` — `Client` 有 godoc 注释
- `lib/config/config.go` — `Validator`, `Config` 接口无 godoc, `Common` 有
- `engine/executor/graph.go` — 所有类型无 godoc, 仅 `IGraph` 接口无注释

结论: exported API 文档覆盖不足 40%, 多数函数签名自描述能力弱。

---

## 2. RCA 能力对齐表

| 业务需求 | 现状 | 缺口 | 补齐方案 | 成本档 |
|---------|------|------|---------|-------|
| **Trace 存储与查询** | OTLP 写入通过 `otel2influx` 转为 InfluxDB line protocol, 以 measurement 方式存储, 无独立 trace 存储引擎 | trace_id/span_id 作为普通 field 存储, 无法做 span 关联; 无 TraceQL 或分布式 tracing 查询; 无 span event 保留保证 | 引入独立 trace 存储格式, 实现 Span 索引 | XL |
| **Trace-Metric-Log 关联查询** | 不存在任何关联查询能力 | 三数据模型相互隔离, 无 correlation ID 传递机制, 无统一查询接口 | 设计 correlation 框架, 统一查询层支持跨模型 join | XL |
| **拓扑存储与查询** | 图数据从外部 HTTP API 获取, 内存中构建 Graph 结构, 支持 k-hop BFS 遍历, mock 默认回退 | 100 万拓扑边无法在内存高效处理; 无持久化拓扑存储; 无分层布图/图分区 | 实现持久化图存储引擎, 图数据分片 | XXL |
| **图算法完备性** | 仅 BFS MultiHopFilter (k-hop) | 缺失: (1) 最短路径 (2) 影响面/故障传播 (3) 子图匹配 (4) 环路检测 (5) 拓扑排序 | 按优先级依次实现: 最短路径 > 影响面 > 子图匹配 | L |
| **事件/Annotations** | RCA 引擎中有 AnomalyEvent 模型 (`engine/executor/rca.go:36-52`) 支持 alarm/event/anomaly 三种类型 | 事件数据需作为 measurement 预写入, 无独立事件存储; 无事件与 metric 时序自动对齐机制 | 扩展 event store, 实现基于时间范围的自动关联查询 | M |
| **AI4DB/异常检测** | 不存在独立的异常检测或预测模块 | 无 anomaly detection/forecast/outlier detection 内置能力 | 引入 castor 服务(已有 `services/castor/`) 或外部 ML 集成 | L |
| **根因推断(RCA)** | `engine/executor/rca.go` 实现了 `FaultDemarcation` — 基于拓扑 BFS + 异常时间戳的故障划定, 处理 anomaly/alarm/event 三种事件类型 | 仅 FaultDemarcation 一种算法; 无概率排序; 无多根因分析; 无因果推断 | 扩展 RCA 算法集合, 引入概率模型 | L |
| **跨模统一查询** | 不存在 unified query API | metric/trace/log/topo 各自独立查询, 用户需分别发请求并手工关联 | 设计 UnifiedQueryStatement + 跨模型查询规划器 | XL |
| **GDPR 数据删除** | 支持 `DeleteSeriesStatement` / `DropMeasurementStatement` / `DropDatabaseStatement`; 通过 shard TTL 自动过期 | 无按条件删除系列(delete from where); 无 `DeleteStatement`(ast.go 已有定义但`/U602E`实现路径不完整) | 完善 DeleteStatement 按条件删除路径 | M |

---

## 3. 代码债优先级清单

### P0 (高影响, 高修复代价)

| 位置 | 问题 | 修复建议 |
|------|------|---------|
| `engine/engine.go` | EngineImpl 全部 60+ 方法无 godoc | 补充 exported 方法文档注释 |
| `engine/executor/rca.go:160-166` | FaultDemarcation 使用 defer recover 捕获 panic, 仅 1 个弱测试 | 重构为返回 error, 增加单元测试覆盖边界 |
| `app/ts-meta/meta/store_fsm.go` | Raft FSM Apply 中多处 panic (proto 解析失败也 panic) | 改为返回 error, Raft 状态机不可 panic |
| `app/ts-meta/meta/store.go` | 20 处 panic 用于 proto 操作失败 | 建立检查型 panic 恢复机制或统一错误处理 |
| `coordinator/write_helper.go` | schema 创建逻辑复杂, `updateCleanSchemaCheck` / `updateSchemaCheck` / `updateSchemaIfNeeded` 三层嵌套, 缺乏单个函数的错误原子性 | 简化 schema 验证流程, 拆分为幂等操作 |

### P1 (中影响)

| 位置 | 问题 | 修复建议 |
|------|------|---------|
| `engine/executor/graph.go:426-765` | `mockGetTimeGraph` 返回 300+ 行硬编码 JSON, 生产环境通过外部 HTTP 获取拓扑 | 移除 mock 回退, 只在开发测试中启用 |
| `engine/executor/graph.go:137-140` | `BatchInsertEdges` 在边数据缺失源/目标节点时立即返回 hard error, 不利于部分数据可用 | 改为 warn + 跳过 + 错误聚合 |
| `engine/executor/graph_transform.go:48` | todo 注释 "more abundant args of graphTransform" 未完成 | 完成 schema 参数传递实现 |
| `lib/opentelemetry/otlp_writer.go:210-248` | trace 属性解析: `convertAttributesToKeyValuesPairs` 做了 JSON marshal/unmarshal 来回转换, 低效 | 直接处理 pcommon.Map 避免序列化开销 |
| `services/retention/service.go` | retention 基于 shard 粒度的 TTL, 无法按 measurement 或 tag 设置 | 支持多级 TTL (database/measurement/series) |

### P2 (低影响)

| 位置 | 问题 | 修复建议 |
|------|------|---------|
| `engine/executor/rca.go` | RCA 代码硬编码时间窗口 (30min, 2h) | 提取为配置参数 |
| `lib/opentelemetry/otel_context.go:58-65` | OTLP span/log dimensions 硬编码为 service.name + span.name | 改为可配置维度列表 |
| `lib/statisticsPusher/statistics/executor_statistics.go` | 执行器统计项丰富但未与 Prometheus 指标对接 | 添加 Prometheus 指标暴露 |
| `engine/executor/graph.go:306-358` | 过滤条件仅支持 EQ/NEQ 运算符, 无 LIKE/REGEX/IN | 扩展运算符支持 |

---

## 4. Panic/Recover 使用分析

### 统计

| 度量 | 数值 |
|------|------|
| 总 `panic()` 使用数 (非测试) | 596 处 |
| 总 `recover()` 使用数 (非测试) | 95 处 |
| panic 文件数 | 约 170+ 个文件 |

### 分类分析

**A. 合理的 panic 使用 (init 失败/不可能状态):**
- `lib/config/openGemini_dir.go:1` — C 库初始化失败
- `lib/binarysearch/binary_search.go:4` — 不应该发生的逻辑错误
- `lib/record/record_check.go:11` — 数据校验内部错误

**B. 不合理的业务逻辑 panic:**
- `engine/executor/call_processor.go:34` — "input and output schemas are not aligned" → 应该返回 error
- `engine/executor/schema.go:16` — "derive type from %v failed" → 应返回 error
- `engine/executor/logic_plan.go:17` — "no child in logical series" → 应返回 error
- `engine/immutable/reader.go:21` — "column(%v) not find in %v" → 应返回 error
- `engine/series_call_processor.go:29` — 迭代器类型断言 panic

**C. 高危的 Raft/状态机 panic:**
- `app/ts-meta/meta/store_fsm.go:12` — Raft Apply 中全部 12 处 panic, 含 `proto.Unmarshal` 失败
- `app/ts-meta/meta/store.go:20` — 20 处 panic 用于 proto 操作
- `app/ts-meta/meta/member_event_handler.go:2` — 成员变更事件处理

**D. 有保护的使用:**
- `engine/executor/rca.go:162` — FaultDemarcation 的 defer recover

---

## 5. 自观测埋点完备性

### 统计系统架构

- `/home/baobao/geminidb/openGemini/lib/statisticsPusher/` — 统计推送框架
  - `statistics_pusher.go` — 主控, 支持 HTTP/文件推送
  - `statistics/` — 各模块统计积累器, 覆盖以下领域:
    - `executor_statistics.go` — 查询执行: run time, wait time, dag edge/vertex, timeout/abort/failed, rows processed
    - `immutable_statistics.go` — TSSP 文件操作统计
    - `compact_statistics.go` — 合并压缩统计
    - `handler_statistics.go` — 请求处理统计
    - `store_query_statistics.go` — 存储查询统计
    - `query_statistics.go` — 查询统计
    - `slowquery_statistics.go` — 慢查询
    - `merge_statistics.go` — 合并统计
    - `downsample_statistics.go` — 降采样统计
    - `io_statistics.go`, `file_statistics.go` — IO/文件操作
    - `runtime_statistics.go` — 运行时统计
    - `db_statistics.go` — 数据库级别统计
    - `dbpt_ha_statistics.go` — 高可用统计
    - `cold_migration_statistics.go` — 冷迁移统计
    - `window_statistics.go` — 窗口函数统计
  - `statistics/opsStat/ops_statistics.go` — Ops 级统计(仅含 name/tags/values 通用结构)

### 关键评估

**优点:**
- 覆盖了引擎主要路径的执行统计
- 支持 HTTP 和文件推送两种方式
- 大部分统计有对应的 test 文件(27+ 测试统计文件)

**不足:**
- 缺少 Prometheus / OpenTelemetry metrics 原生输出
- `lib/metrics/base_collector.go` — 仅 42 行, 高度简化, 仅做了 prometheus.Desc 创建
- 无 tracing 埋点关联 (与 opentelemetry trace 数据无打通)
- 结构化日志: 关键路径使用了 `zap.Logger`, 如 `write_helper.go` 使用了 `zap.Int`, `zap.String`, `zap.Error`
  - 但 `engine/executor/graph.go` 无任何日志
  - `engine/executor/graph_transform.go` 仅初始化 logger 但未使用
  - RCA 模块使用 `log.Error` (全局 logger) 而不是传入结构化 logger

---

## 6. 证据索引

以下是最关键的「文件:行号」引用清单 (>=15 个):

1. `engine/executor/rca.go:36-52` — RCA 事件类型与约束定义 (ANOMALY/ALARM/EVENT)
2. `engine/executor/rca.go:160-288` — FaultDemarcation 根因划定算法
3. `engine/executor/graph.go:81-84` — Graph 结构定义 (Nodes/Edges map, 纯内存)
4. `engine/executor/graph.go:168-226` — MultiHopFilter BFS k-hop 算法
5. `engine/executor/graph_transform.go:137-188` — GraphTransform Work 流程 (外部 HTTP API 获取拓扑)
6. `lib/util/lifted/influx/influxql/ast.go:12282-12289` — GraphStatement 结构定义 (HopNum/StartNodeId/NodeCondition/EdgeCondition)
7. `lib/opentelemetry/otlp_writer.go:111-127` — WriteTraces OTLP span → line protocol 转换
8. `lib/opentelemetry/otel_context.go:58-68` — SpanDimensions 硬编码配置
9. `coordinator/write_helper.go:161-169` — createMeasurement schema 自动创建
10. `services/retention/service.go:32-38` — Retention Service: shard deletion types
11. `engine/engine.go` — EngineImpl 无 godoc 注释 (60+ methods)
12. `app/ts-meta/meta/store_fsm.go` — Raft FSM 中 panic 使用 (12 处)
13. `app/ts-meta/meta/store.go` — 20 处 panic 用于 proto 操作
14. `engine/executor/call_processor.go` — 34 处业务逻辑 panic
15. `tests/server_test.go` — 177 个集成测试用例
16. `lib/statisticsPusher/statistics/executor_statistics.go:36-68` — 执行器统计埋点定义
17. `lib/metrics/base_collector.go:24-28` — 自观测基础结构(仅 ModuleIndex)
18. `engine/executor/rca_test.go` — 仅 1 个 RCA 测试用例
19. `engine/executor/graph_test.go:29-63` — k-hop 拓扑查询测试(12 节点, 14 边 mock)
20. `lib/opentelemetry/otlp_writer_test.go` — 17 个 OTLP 测试用例

---

## 7. Summary of Critical Findings

1. **Trace 能力严重不足**: OTLP 写入后 trace_id/span_id 仅作为普通 field, 不支持 Span 关联查询, 无分布式 tracing 能力
2. **跨模关联查询为零**: Metrics-Logs-Traces-Topo 四者完全独立, 无统一关联查询接口
3. **拓扑存储方案不可扩展**: 100 万拓扑边全量在内存中处理, 外部 API 返回全量图数据, 无分层/分片支持
4. **panic 滥用**: 596 处 panic(含 Raft 状态机关键路径), 平均每个 panic 文件有 3.5 处 panic
5. **文档严重缺失**: `engine/engine.go` 60+ exported 方法无 godoc, 核心接口如 `Validator`, `Config` 无注释
6. **RCA 引擎初具雏形但深度不足**: 仅 FaultDemarcation 一种算法, 缺少概率排序/因果推断/多根因, 测试覆盖率仅 1 个用例
