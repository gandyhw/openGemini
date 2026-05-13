# 00_recon · 全局侦察笔记

> 基准 commit: `b3fc3714` (2025-11-11) — "chore: add DeepWiki badge to README (#914)"
> 侦察日期: 2026-05-13

---

## 仓库地图

| 组件 | Go LOC（非测试） | 测试文件数 | 职责 |
|------|------------------|-----------|------|
| `engine/` | ~61,870 | 182 | LSM-tree 存储引擎,含 row/column store、compaction、index |
| `lib/` | ~25,812 | 246 | 公共库: config、raftlog、metaclient、record、codec、OTLP 等 |
| `app/ts-meta/` | ~13,165 | 22 | 元数据管理, Raft 一致性, store FSM (`app/ts-meta/meta/store_fsm.go:31`) |
| `app/ts-store/` | ~7,122 | 19 | 数据存储节点, shard 管理, transport server |
| `coordinator/` | ~5,165 | 10 | 写入路由(PointsWriter `coordinator/points_writer.go:93`)、shard 映射、流处理 |
| `app/ts-monitor/` | ~2,130 | 4 | 监控采集 daemon |
| `app/ts-sql/` | ~684 | 1 | SQL 查询入口, server 组装 |
| `app/ts-recover/` | ~483 | 1 | 恢复工具 |
| `app/ts-server/` | ~90 | 1 | 统一 server 入口 |
| `app/ts-data/` | ~45 | 0 | 数据节点入口（thin wrapper） |
| `services/` | 未独立统计 | — | 后台服务: downsample, retention, continuousquery, stream, hierarchical, arrowflight 等 |

**总计**: ~398,121 Go LOC (非测试), 518 个 `_test.go` 文件, 283 个 `func Benchmark`

**历史热点** (12 个月内改动最多):
1. `lib/util/lifted/influx/httpd/handler.go` (4 次)
2. `tests/server_test.go` (3 次)
3. `README.md`, `README_CN.md` (各 3 次)
4. `lib/crypto/passkey_decipher.go` (3 次)
5. `lib/config/store.go`, `lib/config/config.go` (各 3 次)
6. `engine/immutable/hot.go` (3 次)

**结论**: HTTP 层、配置、加密、热数据管理是持续改动热点,可能存在技术债累积。

---

## 关键数据流路径

### 写入路径
```
HTTP /write (InfluxDB Line Protocol)
  → handler.serveWrite()  [lib/util/lifted/influx/httpd/handler.go]
  → PointsWriter.WritePoints()  [coordinator/points_writer.go:93]
     → 按 shard 分组 (shard_mapper.go)
     → WriteRows() → Engine.WriteRows()  [engine/engine_interface.go:86]
        → Shard.WriteRows()  [engine/shard.go]
           → WAL (raftlog) → mutable table (memtable) → compaction → immutable TSSP files
```

### 查询路径
```
HTTP /query (InfluxQL / PromQL)
  → handler.serveQuery()
  → influxql.Parser → AST
  → coordinator.StatementExecutor.ExecuteStatement()  [coordinator/statement_executor.go]
  → optimizer → LogicalPlan
  → Engine.CreateLogicalPlan()  [engine/engine_interface.go:114]
  → executor 算子链: IndexScan → Filter → Aggregate → Sort → Fill → ...
  → 列存 reader (colstore) 或 行存 reader (immutable.MmsTables)
```

### 元数据更新路径
```
HTTP API → ts-meta (Raft leader)
  → store_fsm.ApplyBatch()  [app/ts-meta/meta/store_fsm.go:33]
  → Raft log replication → peers
  → metaclient cache sync (100ms interval, store.go:70)
```

### OTLP 路径 (2025 新增)
```
HTTP /api/v2/otlp
  → handler.serveOTLP()  [lib/util/lifted/influx/httpd/handler_otlp.go:31]
  → OtelContext → plog/ptrace/pmetric protobuf deserialize  [lib/opentelemetry/otlp_writer.go:79]
  → otel2influx 转换 → influx.Row
  → InfluxRowsWriter.WritePointRows() → 复用 influx 写入路径
```

---

## 多模能力初判

| 能力 | 状态 | 代码证据 |
|------|------|----------|
| **Metrics** | ✅ 生产可用 | influx line protocol, PromQL via `promql2influxql` transpiler (`lib/util/lifted/promql2influxql/transpiler.go`) |
| **Logs** | ⚠️ 部分支持 | OTLP logs 摄入存在 (`lib/opentelemetry/otlp_writer.go:53`); 全文索引存在 (`engine/index/clv/search.go`, `engine/index/textindex/`); **缺失 LogQL 查询语言** (rg 搜索 LogQL/logql 返回空) |
| **Trace** | ⚠️ 部分支持 | OTLP traces 摄入存在 (`lib/opentelemetry/otlp_writer.go:42`), span 追踪在 executor 中 (`lib/tracing/`); **缺失 trace 专用查询语法和 span 关联分析** |
| **Topology/Graph** | ⚠️ Alpha | `GraphStatement` AST 存在 (`lib/util/lifted/influx/influxql/ast.go:12282`), 支持 HopNum + 节点/边条件过滤; `GraphTransform` executor 存在 (`engine/executor/graph_transform.go:34`); **缺失 k-hop 遍历、最短路径、影响面分析算法** (rg 搜索 k-hop/shortest-path/graph-traversal 返回空) |

### 关键确认

- **1M 拓扑边当前承载方式**: `GraphStatement` 通过 HopNum + NodeCondition/EdgeCondition 表达式进行类图遍历查询,底层存储仍走 influx row 模型。**不存在独立图存储引擎或图索引结构。**
- **跨模查询**: OTLP 通过 `otel2influx` 将所有 telemetry 统一转为 influx.Row 写入同一存储引擎。跨模关联查询无原生支持——需要应用层手工 join。
- **AI4DB**: rg 搜索 `anomaly|forecast|ai4db|predict` 返回空,不存在的功能。

---

## 已知缺口的初步假设（供阶段 2  subagent 验证）

- **假设 1: ts-meta 在 100K 实体 × 高基数 tag 下是瓶颈** — Raft FSM 使用 `ApplyBatch` 但单锁保护 (`fsm.mu.Lock()`, store_fsm.go:36), 100ms 增量更新 + 10s 全量推送, 百万级 time series 元数据可能触发 O(n) 遍历。
- **假设 2: 写入路径缺少解耦层,无法承载 1B logs/s 突发** — 从 HTTP handler 到 WAL 是同步链路, coordinator 无反压队列或前置缓冲(如 Kafka 接口预留)。
- **假设 3: 跨模 RCA 能力严重不足** — 无 LogQL, 无 trace 专用查询, 拓扑仅支持基础 hop 过滤。生产 RCA 需要的"从告警 → 关联 trace → 下钻日志 → 拓扑影响面"链路无法在当前代码中闭环。
- **假设 4: LSM-tree 7 级 compaction 在日志高基数场景下写放大严重** — `CompactLevels = 7` (compact.go:39), 日志场景 key 基数极高, level 合并可能频繁触发。
- **假设 5: 列存对半结构化日志压缩比 15:1 不现实** — 日志字段稀疏、长字符串,不同于 metrics 的数值密集型,列存收益有限。
- **假设 6: 无统一查询语言或跨模 join 能力** — PromQL 走 transpiler → InfluxQL 的单向转换,无 LogQL→InfluxQL,无跨模 join 算子。

---

## 风险热区

| 排名 | 文件 | 近期改动 | 风险判断 |
|------|------|---------|----------|
| 1 | `lib/util/lifted/influx/httpd/handler.go` | 4 次 | HTTP 层集中度过高,所有协议复用同一 handler |
| 2 | `lib/config/config.go` / `store.go` | 各 3 次 | 配置结构变更频繁,可能设计稳定性不足 |
| 3 | `engine/immutable/hot.go` | 3 次 | 冷热分层逻辑持续迭代 |
| 4 | `lib/crypto/passkey_decipher.go` | 3 次 | 安全模块改动频繁—关注正确性 |
| 5 | `app/command.go` | 3 次 | 命令行框架持续调整 |

---

## 工具链状况（降级说明）

| 工具 | 状态 | 降级方案 |
|------|------|----------|
| `go` | ❌ 项目需要 go1.24,本机不可用 | 无法 `go test`、`go vet`、`go list`; 覆盖率数据需从代码静态推断 |
| `rg` | ✅ v14.1.1 | — |
| `tokei` / `cloc` | ❌ | 用 `find + wc -l` 替代 |
| `golangci-lint` | ❌ | `rg` 搜索 anti-pattern |
| `gocyclo` | ❌ | 目视评估 |
| `staticcheck` | ❌ | `rg` 搜索常见问题 |
| `govulncheck` | ❌ | 手动检查 go.mod 依赖 |
