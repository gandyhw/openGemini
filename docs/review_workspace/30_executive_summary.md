# 30_executive_summary · openGemini 重构评审执行摘要

> 评审范围: 架构 / 性能 / 代码质量与 RCA 完备性 / 韧性 四维
> 目标规模: 1 亿 metrics/s 写入, 10 亿 logs/s 写入, 10 万实体, 100 万拓扑边, 跨模关联下钻秒级响应
> 基准 commit: b3fc3714 (2025-11-11)

---

## Top 10 风险（按 RICE 排序）

| # | 风险 | 严重度 | 影响面 | RICE |
|---|------|--------|--------|------|
| 1 | **默认构建无脑裂防护** — `engine/fence.go:23-33` fencer 为 no-op | 致命 | 数据不一致 | 125 |
| 2 | **WAL fsync 静默跳过** — `lib/raftlog/storage.go:347-348` 每 100ms 丢 1 亿条日志 | 致命 | 数据丢失 | 125 |
| 3 | **无端到端超时级联** — context 未从 HTTP 贯穿到 engine scan | 严重 | 查询雪崩 | 80 |
| 4 | **无磁盘空间保护** — 全代码库无 ENOSPC 检测 | 致命 | 节点不可恢复崩溃 | 62.5 |
| 5 | **写入路径无反压/缓冲** — HTTP→coordinator→shard→WAL 全程同步 | 致命 | 写入 OOM/雪崩 | 41.7 |
| 6 | **FSM panic 滥用 (596 处)** — Raft 状态机中 12 处 proto 失败 panic | 严重 | ts-meta 进程崩溃 | 33.3 |
| 7 | **备份一致性缺失** — 仅有文件级拷贝，无跨节点快照，RPO=天级 | 严重 | 恢复后数据不一致 | 25 |
| 8 | **ts-meta 单 Raft 组瓶颈** — 100K 实体下串行化不可扩展 | 严重 | 写入全集群不可用 | 20 |
| 9 | **Compaction 写放大 10-18x** — 1B logs/s 需 3.6 TB/s IO 带宽 | 严重 | 存储层击穿 | 20 |
| 10 | **零 SIMD/向量化** — 全标量执行，查询 CPU 低效 3-6x | 严重 | 查询延迟无法达标 | 20 |

---

## Top 5 重构建议

### 1. 先止血：紧急修复非侵入性缺陷（成本 S/M，1-2 月）

- 启用默认脑裂防护 (`engine/fence.go:23-33`)
- WAL sync 策略可配（同步/异步模式）
- 磁盘水印保护 — 写入前检查剩余空间
- 端到端 context 超时传播链
- **收益**: 消除 4 个致命风险（F1/F4/F5/F7），成本极低

### 2. 写入解耦：引入 Ingestion Gateway（成本 L，3-6 月）

- 将 ts-sql/ts-store 的写入路径拆分为独立的 ingestion gateway
- 支持前置 Kafka/Pulsar 消息队列缓冲
- gateway 负责协议解析(Line Protocol/OTLP/PromQL) → 标准化内部格式
- **收益**: 写入吞吐弹性伸缩，1B logs/s 写入突发可缓冲

### 3. 跨模关联：统一查询层 + 本地拓扑引擎（成本 XL，6-12 月）

- 将 Topology 数据从外部 HTTP 服务迁移到 openGemini 本地图存储引擎
- 实现 metric/log/trace/topo 的跨模 join 算子（HashJoin/MergeJoin）
- 引入全文检索索引（替代当前无 LogQL 的窘境）
- **收益**: 生产 RCA 关联下钻链路闭环（告警→trace→日志→拓扑影响面）

### 4. 存储引擎分层：日志专用存储路径（成本 L，3-6 月）

- 为日志数据创建独立 shard 类型（与 metrics 物理隔离）
- 内置全文索引 + 专用日志压缩（字典压缩 / delta encoding for string）
- 复用列存架构但针对日志高基数/长字符串场景优化
- **收益**: 日志压缩比 2-4x → 5-8x，查询延迟显著降低

### 5. 元数据拆分：多 Raft 组 / 分层元数据（成本 XL，6-12 月）

- 将 ts-meta 单 Raft 组拆分为拓扑、数据字典、shard 分配等多组
- 元数据缓存从 100ms 轮询改为 push-based (etcd watch)
- FSM panic 全部改为 error return
- **收益**: 消除 100K 实体元数据瓶颈，支撑 10 万+ 实体规模

---

## 路线建议

**基于 openGemini 重构 vs. 自研 vs. 选择其他底座**

| 选项 | 适用场景 | 理由 |
|------|---------|------|
| **基于 openGemini 重构** ✅ | Metrics 100M/s + 逐步增加日志/trace/拓扑能力 | 存储引擎(LSM+列存)成熟度高，测试覆盖尚可(518 文件/283 benchmark)；重构范围可控：S/M 档修复 1-2 月见效，L 档 3-6 月补齐核心缺口 |
| **自研** ❌ | 需要 LogQL/TraceQL 等全部原生支持 | 成本 XL(>1年/10+ 工程师)，从零实现 LSM 存储引擎、查询优化器、Raft 一致性的工程风险极大 |
| **选择其他底座** ⚠️ | 如果主场景是纯日志或纯 Trace | ClickHouse (日志)、Elasticsearch (全文)、Jaeger/Tempo (Trace) 在各自领域更成熟，但 cross-model correlation 同样不足 |

**推荐**: 基于 openGemini 重构，分三阶段推进:
- **0-3 月**: 止血 + ingestion gateway POC
- **3-6 月**: 日志专用存储 + 备份自动化 + 跨模 join POC
- **6-12 月**: 拓扑内建 + 元数据拆分 + 向量化执行引擎

**不建议放弃 openGemini 另起炉灶**: 存储引擎核心（LSM compaction、列存、倒排索引、OTLP 摄入）已经过工程验证。缺口主要在查询层（跨模关联）和运维层（韧性/自动化），补全成本低于从零自研。

---

## 评审方法说明

- 四 agent 并行评审，各产出一份独立报告（10-13_*.md）
- 交叉评审去重 8 个共同问题，无实质冲突
- 所有 1034 行报告包含 80+ 个 `文件:行号` 证据引用
- Go 1.24 toolchain 不可用，性能数据为静态推断；建议在可编译环境中运行 benchmark 验证
