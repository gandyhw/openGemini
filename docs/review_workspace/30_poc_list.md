# 30_poc_list · 必须实测验证的假设

> 以下假设全部基于代码静态分析推断，必须在可编译环境 (Go 1.24+) 中通过 POC 实测验证。
> 每个 POC 包含: 目标假设 / 验证方法 / 通过条件 / 预估耗时

---

## POC-1: 单节点写入吞吐上限

**目标假设**: 单节点 (32 核 / 256GB RAM / NVMe) 写入 ~3-5M metrics/s (静态推算)

**验证方法**:
1. 部署单节点 ts-server + ts-meta
2. 使用 influx line protocol 写入 (batch=5000, 3 fields + 3 tags, float64)
3. 逐步增加并发度，记录 p99 延迟和吞吐量
4. 分别测试不同 cardinality (10K / 100K / 1M / 10M series)

**通过条件**: 实测数据 ±30% 以内吻合静态推算；识别实际瓶颈点（CPU/IO/锁竞争）

**预估耗时**: 3-5 天

**产出**: 单节点吞吐量曲线 (QPS vs p99 latency) + flamegraph 热点函数

---

## POC-2: Logs 写入压测

**目标假设**: 单节点日志写入 ~0.5-1M logs/s；100ms crash 丢失 1 亿条（静态推算）

**验证方法**:
1. 使用 OTLP logs 协议写入 (200 bytes body + 5 tags)
2. 模拟 crash 场景: 写入 10s 后 `kill -9`，重启后统计丢失日志数
3. 分别在 async sync (100ms) 和 sync fsync 模式测试
4. 测试不同写入速率下的延迟和丢失率

**通过条件**: 
- async 模式: 丢失窗口 < 200ms (与推算一致)
- sync 模式: 丢失窗口 < 1ms
- 获得实际单节点日志写入上限

**预估耗时**: 5-7 天

**产出**: 日志写入性能数据 + WAL sync 策略对比报告

---

## POC-3: ts-meta 元数据扩展性

**目标假设**: 单 ts-meta 在 10K+ 实体下出现延迟上升（静态推算）；100K 实体下可能不可用

**验证方法**:
1. 部署 3 节点 ts-meta Raft 集群
2. 逐步创建 1K → 10K → 50K → 100K 个 database + measurement
3. 在每级下测量: 写入路由延迟 (DBPtView)、shard 创建延迟、schema 更新延迟
4. 监控 ts-meta 内存占用变化

**通过条件**: 获得实际拐点数据；确认 100K 实体是否需要元数据拆分

**预估耗时**: 5-7 天

**产出**: ts-meta 扩展性曲线 + 内存使用增长图

---

## POC-4: 高 Cardinality Index 压力

**目标假设**: >100M timeseries 下 TSI 倒排索引内存爆炸、写入延迟恶化

**验证方法**:
1. 写入 100M unique series (高基数: 10K tag keys × 10K tag values)
2. 测量: index 内存使用、写入延迟 p50/p99、index build 吞吐量
3. 测量: 按 tag 过滤查询延迟 (1 tag / 3 tags / 5 tags 组合)
4. 对比不同 index 类型 (TSI / ski / sparse)

**通过条件**: 获得不同 cardinality 下的内存/延迟曲线；确定是否需要 index 分片

**预估耗时**: 7-10 天

**产出**: 高基数 index 性能报告 + index 类型选择建议

---

## POC-5: Compaction 写放大实测

**目标假设**: TSStore compaction 写放大 10-18x (基于 level 配置推算)

**验证方法**:
1. 持续写入 1TB 数据 (metrics + logs 混合)
2. 监控: 实际写入字节数 vs 磁盘写入字节数 (通过 `/proc/diskstats`)
3. 监控: compaction 触发的每次 level 合并统计
4. 分别测试纯 metrics / 纯 logs / 混合负载下的 WA

**通过条件**: 实测 WA ±30% 以内吻合推算；找到主要 WA 来源

**预估耗时**: 5-7 天

**产出**: Compaction WA 实测报告 + level 合并频率统计

---

## POC-6: 1M 拓扑边 k-hop 查询

**目标假设**: 当前拓扑查询走外部 HTTP API，全内存处理，1M 边缘不可扩展

**验证方法**:
1. 构建 1M 边 + 100K 节点的合成拓扑图
2. 测试外部 HTTP 模式下: 数据拉取延迟、内存峰值、k-hop BFS 延迟 (hops=1,2,3,5)
3. 对比: 为 POC 增加本地拓扑读取原型（如替换/扩展 `GraphTransform` 的数据源接口，从本地持久化 edge store 或 measurement reader 读取边），再用 `GraphStatement` 驱动同一 k-hop 过滤；仅把 edge 写成 measurement 不能覆盖当前 `GraphTransform.Work()` 路径
4. 测试 100 并发查询时的 QPS

**通过条件**: 
- 外部 HTTP 模式能否承载 1M 边 (可能直接 OOM)
- 本地拓扑读取原型 vs 外部 API 模式性能对比
- 确认 ≥85% 需要内建图引擎

**预估耗时**: 7-10 天

**产出**: 拓扑查询性能对比报告 + 图引擎方案建议

---

## POC-7: WAL sync 模式性能对比

**目标假设**: 同步 fsync 模式性能下降 30-50%，但丢失窗口降至 <1ms

**验证方法**:
1. 部署 ts-store，分别配置 `WalSyncInterval=0` (每次 fsync) vs `100ms` (异步)
2. 逐步加压写入 (1M → 10M → 100M metrics/s across 10 nodes)
3. 测量: p99 写入延迟、吞吐量、crash 实测数据丢失
4. 引入 group commit (收集 N 条后一次 fsync) 作为中间方案测试

**通过条件**: 找到最佳 fsync 策略 (平衡性能与持久性)；确认 sync 模式在目标延迟 10ms 内可达

**预估耗时**: 5-7 天

**产出**: WAL sync 策略对比数据 + 推荐配置

---

## POC-8: 跨模关联查询可行性

**目标假设**: 当前无法做 metric+log+trace+topology 关联查询

**验证方法**:
1. 在同一集群中写入 3 组数据:
   - Metrics: 1M points, 3 tags
   - Logs: 100K lines, 5 tags (含关联 ID)
   - Topology: 10K nodes + 50K edges
2. 尝试通过 correlation ID 在应用层关联查询 (多次查询 + 客户端 join)
3. 测量: 端到端延迟 (从发起关联到结果返回)
4. 评估: 如果实现 server-side HashJoin/MergeJoin，延迟可降低多少倍

**通过条件**: 明确跨模 join 的性能瓶颈位置 (网络往返 / 序列化 / 客户端合并)；补齐成本估算置信度提升

**预估耗时**: 5-7 天

**产出**: 跨模关联现状评估 + Join 算子性能预估

---

## POC 总结

| POC | 目标 | 通过条件 | 预估耗时 | 优先级 |
|-----|------|---------|---------|--------|
| POC-1 | 单节点吞吐上限 | 确认 3-5M/s 推算 | 3-5d | P0 |
| POC-2 | Logs 写入+丢失 | 确认丢失窗口 | 5-7d | P0 |
| POC-3 | ts-meta 扩展性 | 找拐点数据 | 5-7d | P0 |
| POC-4 | 高基数 Index | 内存/延迟曲线 | 7-10d | P1 |
| POC-5 | Compaction WA | 确认 10-18x 推算 | 5-7d | P1 |
| POC-6 | 1M 边拓扑 | 确认是否需要图引擎 | 7-10d | P1 |
| POC-7 | WAL sync 对比 | 找到最佳策略 | 5-7d | P2 |
| POC-8 | 跨模关联 | 确认 join 可行性 | 5-7d | P2 |

**总预估 POC 耗时**: 8-10 周 (可并行 2-3 人)

**POC 阻塞前提**: Go 1.24 toolchain 必须可用才能编译运行
