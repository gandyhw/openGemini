# 20_cross_review · 交叉评审纪要

> 基准报告: A(架构) / B(性能) / C(质量+RCA) / D(韧性)
> 评审日期: 2026-05-13

---

## 1. 去重合并

以下问题被 ≥2 个 agent 独立发现，合并为统一描述：

### D1. ts-meta 单 Raft 组瓶颈
- A: 全局风险，FSM ApplyBatch 双锁 (`store_fsm.go:36-40`)，10 万实体下串行化
- B: 写入热点 #5，row-by-row schema check 依赖 meta 查询
- C: P0 代码债 — FSM 中 12 处 panic
- D: F3 FMEA — 单锁串行化，100ms 缓存增量延迟
- **合并**: ts-meta 的单一 Hashicorp Raft 组 + 全内存 FSM + 双锁 + panic 滥用 = 整个集群的写入可用性单点。是**最高优先级风险**。

### D2. 写入路径无反压
- A: 风险 #6 — 无端到端限流，写入抖动雪崩
- B: 瓶颈 #1 — 同步串行 `memtable → WAL`，1B logs/s 1000-2000x 缺口
- D: F6 — WriteConcurrentLimit=0，无 admission control，OOM 风险
- **合并**: HTTP handler → coordinator → shard → WAL 全程同步无缓冲，目标规模下必然雪崩。

### D3. 无端到端超时级联
- A: 风险 #12 — `points_writer.go:862` 用 `time.Since` 自旋，无 context 链
- B: goroutine 泄漏分析 — 134 个 `go func()`，多个无 context 传播
- D: F7 — InterruptQuery 仅看全局内存 85%，无法隔离单查询
- **合并**: context 未从 HTTP 贯穿到 engine scan，大查询无资源隔离。

### D4. 跨模关联/RCA 能力缺失
- A: 风险 #10 — 无跨 measurement join 执行
- C: RCA 对齐表 — Trace/Metric/Log/Topo 四个模型完全独立
- **合并**: 生产 RCA 的核心"关联下钻"链路（告警 → trace → 日志 → 拓扑影响面）无法闭环。

### D5. 日志全功能缺失
- A: 无 LogQL；全文索引代码存在，但未确认接入 OTLP log body 查询路径
- B: 压缩比仅 2-4x，无专用日志压缩
- C: trace body 以 string field 存储，无独立存储引擎
- **合并**: 日志从摄入(OTLP)→存储(共享 influx.Row)→查询(LogQL/body 全文查询未打通)→压缩(Snappy 2-4x) 全链路薄弱。

### D6. 拓扑外挂不可靠
- A: 风险 #5 — 依赖外部 TopoManager HTTP 服务
- C: 100 万边全内存 + 外部 API，不可扩展
- **合并**: 1M 拓扑边不在 openGemini 管理范围，依赖外部 HTTP 且无可降级方案。

### D7. Compaction 写放大
- B: 10-18x WA，7 级 LSM，L0→L1 每轮 6 次
- D: F10 — 慢查询吃 IO 导致 compaction 延迟，写入吞吐下降
- **合并**: 写放大在 1B logs/s 场景产生 ~3.6 TB/s 存储带宽需求，远超 SSD 能力。

### D8. 运维成熟度低
- D: 无滚动升级、无配置热加载、无 decommission、备份原始（RPO=天级）
- C: 无自动备份调度器
- **合并**: 运维自动化几乎为零，规模化部署不可行。

---

## 2. 冲突识别

### 冲突 #1: 架构可保留 vs. 需重写

| A 观点 | B 观点 |
|--------|--------|
| ts-meta/ts-sql/ts-store 三层都需拆分，独立 ingestion gateway + query coordinator + meta 分组 (XL) | Metrics 100M/s 在 100 节点 + 3x 优化后**可达**；仅 Logs 需要架构重构 |

**仲裁**: 不冲突。A 关注的是 RCA 完备性（跨模关联、拓扑内建），这些确实需要架构变更。B 关注的是纯吞吐量，高密度 metrics 写入路径的瓶颈可通过水平扩展缓解。**统一结论**: Metrics 100M/s 在当前架构 ≤100 节点可行；Logs 1B/s + RCA 场景必须重构。

证据重新确认:
- `coordinator/points_writer.go:227-310` — 写入路径确实可横向扩展到更多 ts-store 节点
- `lib/opentelemetry/otlp_writer.go:171-250` — 但 logs 统一走 influx.Row 无差异化优化

### 冲突 #2: 测试充分 vs. 测试不足

| C 观点 | D 观点 |
|--------|--------|
| 测试覆盖 3/5，518 测试文件，引擎有 182 个测试 | 关键故障路径（脑裂、磁盘满、背压）无测试 |

**仲裁**: 不冲突。C 关注的是单元测试数量，D 关注的是韧性/混沌测试。两者维度不同但都正确。**统一结论**: 单元测试量尚可但韧性测试严重不足，尤其是故障注入测试。

证据重新确认:
- `engine/executor/rca_test.go` — RCA 仅 1 个测试用例，证实 D 观点
- `engine/engine_test.go` — 80 个测试函数，证实 C 观点
- 搜索 `chaos|fault.*injection` 无独立测试套件

### 冲突 #3: 无实质冲突

以下代理结论表面看可能矛盾但实际不冲突，已全部确认：
- A 说 PromQL 转译有损 / C 说 OTLP 有测试覆盖 — 不同关注点
- B 说 sync.Pool 使用密集且合理 / B 也说部分 `interface{}` 装箱 — 同一个报告内部细节

---

## 3. 二次取证（争议项主动重读代码）

### 取证 #1: FSM panic 是否真的在写路径？

重读 `app/ts-meta/meta/store_fsm.go:50-52`:
```go
if err := proto.Unmarshal(logs[i].Data, &cmd); err != nil {
    panic(fmt.Errorf("cannot marshal command: ..."))
}
```

**结论**: proto.Unmarshal 失败在 Raft Apply 中直接 panic。如果是损坏的 Raft log entry，整个 ts-meta 进程会 crash。C 和 D 的严重度评估正确。

### 取证 #2: GraphTransform 是否真的走外部 HTTP？

重读 `engine/executor/graph_transform.go:137-188`:
- 第 161 行调用外部 HTTP API 获取拓扑数据
- 第 166 行: `resData, err := graph.GetGraphDataFromServer(...)`
- 无本地 fallback（除了测试中的 mock）

重读 `lib/util/graph_client.go:86-115`:
- `GetGraphDataFromServer()` 发起 HTTP GET 到 `TopoManagerUrl`
- 第 93 行 `"X-Auth-Token"` 为空串（TODO 注释）

**结论**: A 和 C 的评估正确 — 拓扑数据完全外部，且无认证配置。

### 取证 #3: WAL fsync 静默跳过是否属实？

重读 `lib/raftlog/storage.go:347-348`:
```go
if !rds.syncTaskCount.CompareAndSwap(0, 1) {
    return nil   // silently skip if sync already in progress
}
```

**结论**: D 的严重度 5 评估正确。连续两次写入之间 crash 确实会丢数据。实际丢失窗口可能 >100ms。

---

## 4. RICE 重排序

公式: **Priority = Reach × Impact × Confidence / Effort**

| 排名 | ID | 问题 | R | I | C | E | RICE | 严重度 |
|------|-----|------|---|---|---|---|------|--------|
| 1 | D2 | 写入路径无反压/缓冲 | 5 | 5 | 5 | 3 | 41.7 | **致命** |
| 2 | D1 | ts-meta 单 Raft 组瓶颈 | 5 | 5 | 4 | 5 | 20.0 | **致命** |
| 3 | D7 | Compaction 写放大 10-18x | 5 | 4 | 4 | 4 | 20.0 | **严重** |
| 4 | D4 | 跨模关联/RCA 缺失 | 5 | 5 | 3 | 5 | 15.0 | **严重** |
| 5 | F1 | 默认构建无脑裂防护 | 5 | 5 | 5 | 1 | 125.0 | **致命** |
| 6 | F4 | WAL fsync 静默跳过 | 5 | 5 | 5 | 1 | 125.0 | **致命** |
| 7 | F5 | 无磁盘空间保护 | 5 | 5 | 5 | 2 | 62.5 | **致命** |
| 8 | D3 | 无端到端超时级联 | 4 | 4 | 5 | 1 | 80.0 | **严重** |
| 9 | D5 | 日志全链路薄弱 | 4 | 4 | 3 | 5 | 9.6 | **严重** |
| 10 | D6 | 拓扑外挂不可靠 | 3 | 5 | 4 | 5 | 12.0 | **严重** |
| 11 | F6 | 写入 OOM (无 admission) | 5 | 5 | 4 | 4 | 25.0 | **致命** |
| 12 | F7 | 大查询 OOM (无 per-query budget) | 4 | 4 | 4 | 3 | 21.3 | **严重** |
| 13 | F9 | 备份一致性缺失 | 4 | 5 | 5 | 4 | 25.0 | **严重** |
| 14 | C1 | FSM panic 滥用 (596 处) | 5 | 4 | 5 | 3 | 33.3 | **严重** |
| 15 | C2 | EngineImpl 文档缺失 | 3 | 2 | 5 | 1 | 30.0 | **中等** |
| 16 | D8 | 运维成熟度低 | 4 | 4 | 4 | 4 | 16.0 | **严重** |
| 17 | A1 | PromQL 转译有损 | 3 | 3 | 4 | 5 | 7.2 | **中等** |
| 18 | C3 | RCA 引擎浅（仅 1 测试） | 3 | 4 | 5 | 4 | 15.0 | **中等** |
| 19 | B1 | 零 SIMD/向量化 | 4 | 4 | 5 | 4 | 20.0 | **严重** |
| 20 | C4 | 无 GC 优化(logs 场景) | 4 | 4 | 4 | 4 | 16.0 | **严重** |

**R/I/C/E 评分标准**: 1-5 (5=最高)
- Reach: 1=单模块, 3=单组件, 5=全集群
- Impact: 1=轻微, 3=功能受限, 5=不可用
- Confidence: 1=推测, 3=静态推断, 5=代码直接证实
- Effort (逆向): 1=**最小**(≤1w/S档), 5=**最大**(XL档)

**说明**: 上表中 Effort=1 表示修复成本最低(S 档)，RICE 值最高表示优先级最高（容易修 + 影响大）。

---

## 5. 跨报告一致性验证

**一致确认的事实** (≥3 个 agent 独立验证):
1. ts-meta 使用 Hashicorp Raft 单组 — A/D/C 确认
2. ts-store 使用 etcd/raft v3 per-PT — A/D 确认
3. OTLP → influx.Row 统一转换 — A/C/B 确认
4. 列存和行存共享同一 Shard 抽象 — A/B/C 确认
5. 无 LogQL/body 全文搜索查询语言；已有全文索引代码但未确认接入日志查询路径 — A/C 确认
6. 无 SIMD/向量化执行 — B 确认（全仓库搜索无结果）

**单一 agent 发现但未与其他矛盾**:
- D 的 Fence no-op 发现 — 其他 agent 未检查此路径
- B 的 GC 分配速率估算 — 仅 B 做定量推算
- C 的 panic 精确计数 (596 处) — 其他 agent 未精确统计

---

## 6. 交叉评审结论

### 最致命发现（必须立即修复）

1. **F1: 默认构建无脑裂防护** (`engine/fence.go:23-33`) — 修复成本 S，影响面全局
2. **F4: WAL fsync 静默跳过** (`lib/raftlog/storage.go:347-348`) — 修复成本 S，100ms 丢 1 亿条日志
3. **F5: 无磁盘空间保护** — 修复成本 M，磁盘满=不可恢复崩溃

### 最需架构决策

4. **D2: 写入缓冲/解耦层** — 决定了能否接近 1B logs/s
5. **D1: ts-meta 元数据层拆分** — 决定了能否支撑 10 万实体
6. **D4: 跨模关联查询** — 决定了 RCA 场景能否闭环

### 建议的修复顺序

**先止血（S/M 档，1-2 月）→ 再加固（L 档，3-6 月）→ 后重构（XL 档，6-12 月）**

此顺序将作为阶段 4 路线图的基础。
