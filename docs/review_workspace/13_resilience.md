# 韧性 (Resilience) 评审报告

> 评审日期: 2026-05-13
> 目标规模: 1 亿 metrics/s、10 亿 logs/s、10 万实体、100 万拓扑边
> 基准 commit: `b3fc3714`

---

## FMEA 表

| ID | 故障模式 | 触发条件 | 影响面 | 当前缓解 | 不足 | 改进建议 | 严重度 |
|----|---------|---------|--------|---------|------|---------|--------|
| F1 | **脑裂 (Split-brain)** | 网络分区导致 ts-store 数据副本 Raft 组出现双主 | 数据不一致, 脏写覆盖 | engine_ha.go:201-203 调用 Fencer.Fence()；streamfs 构建下使用文件锁 + WAL seal (engine/fence_streamfs.go:39-63) | **非 streamfs 构建下 fencer 为 no-op** (engine/fence.go:23-33)，默认编译无脑裂防护 | 默认构建也启用至少文件级 fencing；增加 lease-based 失败检测 | **5** |
| F2 | **元数据 Raft leader 丢失** | ts-meta leader 节点宕机 | 全集群写入不可用, 元数据变更阻塞 | ElectionTimeout=1s, HeartbeatTimeout=1s (lib/config/meta.go:38-40)；集群管理器中 leader 变更事件处理 (app/ts-meta/meta/cluster_manager.go:183-291) | waitForLeader() 轮询间隔 100ms, 无指数退避 (app/ts-meta/meta/store.go:1521-1532) | 增加退避机制防止 leader 选举风暴；增加 pre-vote 协议防止分区节点扰动选举 | **4** |
| F3 | **元数据 FSM 串行瓶颈** | 高并发元数据操作 | 元数据更新延迟增加, shard 分配阻塞 | ApplyBatch 批量处理日志 (app/ts-meta/meta/store_fsm.go:33-75) | 单锁 s.mu.Lock() 串行化所有操作 (store_fsm.go:36)；100ms 缓存增量推送、10s 全量推送延迟高 | 考虑 FSM 内部读写分离；减少全量推送频率；对大集群使用分层元数据 | **3** |
| F4 | **WAL 崩溃丢数据** | ts-store 进程崩溃 | 最多 100ms 数据丢失 | RaftEntrySyncInterval=100ms 后异步 backSync (lib/raftlog/storage.go:332-368) | backSync 是异步 goroutine；syncTaskCount CAS 会静默跳过正在进行的 sync (storage.go:347-348)；数据窗口期内全丢 | 允许配置同步 fsync 模式；增加 sync 确认 barrier；考虑 group commit 替代定时 sync | **5** |
| F5 | **磁盘空间写满** | 日志写入超预期, 磁盘 100% | WAL 写入失败, TSSP 文件损坏, 节点不可用 | 无 | **全代码库无 ENOSPC/disk-full 检测** (rg 搜索无结果)；IsMemUsageExceeded() 只查内存 (engine/sysctrl.go:307-316) | 接入磁盘水印机制 (hard/soft limit)；disk-full 时主动拒绝写入而非 panic；预创建文件时检查剩余空间 | **5** |
| F6 | **写入 OOM** | 10B logs/s 突发写入 | ts-store 进程 OOMKilled, 级联故障扩散 | WriteConcurrentLimit=0 (lib/config/store.go:347, 默认无限制) | **写路径无反压机制**, 从 HTTP handler 到 Shard.WriteRows 为同步路径 (coordinator/points_writer.go)；无 request queuing 或 admission control | 前置 admission control (token bucket)；Distributed rate limiter；全局写入队列 | **5** |
| F7 | **大查询 OOM** | 跨大量 shard 的聚合/排序查询 | 内存耗尽导致查询节点 OOM | InterruptQuery + InterruptSqlMemPct=85% 在内存超 85% 时中断查询 (lib/config/store.go:281-282, engine/executor/processor.go:263-270) | 触发条件是全局内存百分比, 无法隔离"大查询 vs 小查询"；无 per-query memory limit | 引入 per-query memory 预算；H2O (high-to-low) 优先级驱逐；查询队列 + backpressure | **4** |
| F8 | **Gossip/Serf 成员变更风暴** | 大规模节点同时重启 | 集群成员状态震荡, 迁移事件重复触发 | cluster_manager 有 leader 变更重发机制 (app/ts-meta/meta/cluster_manager.go:183-291) | Serf 事件处理与 Raft 状态耦合；无成员变更速率限制 | 引入 member change rate limiter；增加 gossip 协议隔离 | **3** |
| F9 | **备份一致性缺失** | 跨节点备份 | 备份数据集不一致, 恢复后数据损坏 | FileCopy + FolderCopy 文件级拷贝 (lib/backup/backup.go:47-107) | **无跨节点一致性快照**；增量备份仅追踪文件列表 (lib/backup/backuplog.go:19-37)；无备份调度器 | 实现全局一致性快照；使用 Raft snapshot 对齐备份点；增加 backup scheduler | **4** |
| F10 | **慢日志导致级联** | 大范围扫描查询耗用大量 IO | compaction 延迟, 写入吞吐下降 | compact.go:46-47 限制 compaction concurrency；OpenShardLimit 限制并发打开 shard | 无 read/write IOPS 隔离；无查询优先级/租约 | 实施 IOPS scheduler；read/write 线程池分离；存储层 QoS | **3** |

---

## HA 一致性评估

### ts-meta Raft (Hashicorp Raft)

```
ElectionTimeout:    1s   (lib/config/meta.go:39)
HeartbeatTimeout:   1s   (lib/config/meta.go:40)
LeaderLeaseTimeout: 500ms (lib/config/meta.go:38)
```

- 元数据层使用 Hashicorp Raft, 3 节点集群配置下可容忍 1 节点故障。
- Leader 切换时间: 大部分场景 < 3s (ElectionTimeout + 少量波动)。存在 `notifyCh` 用于 leader 变更通知 (raft_wrapper.go:93)。
- **不足**: 未启用 PreVote 协议 (Hashicorp Raft 默认关闭), 网络分区恢复后可能造成 leader 选举扰动 (证据: app/ts-meta/meta/raft_wrapper.go, 无 PreVote 相关配置)。

### ts-store 数据复制 Raft (etcd/raft)

```
ElectionTick:    10  * 400ms = 4s   (lib/config/store_raft.go:21, lib/raftconn/node.go:43)
HeartbeatTick:   1   * 400ms = 400ms (lib/config/store_raft.go:22)
MaxInflightMsgs: 256 (lib/raftconn/node.go:42)
MaxSizePerMsg:   4096 (lib/raftconn/node.go:41)
WaitCommitTimeout: 20s (lib/config/store_raft.go:25)
```

- 数据副本 Raft 基于 etcd/raft v3, 每个 DB+PT 一个 Raft 组。
- Leader 选举超时约 4s, 对于 1B logs/s 的写入延迟容忍需求，这个时间偏长。
- **Split-brain 防护**: `engine_ha.go:201-203` `Fence` = 在 streamfs 构建下使用文件锁 + WAL seal (`engine/fence_streamfs.go:39-63`)。**然而默认构建 (`!streamfs`) fencer 是 no-op** (`engine/fence.go:23-33`)，这意味着默认编译的 binary **完全没有脑裂防护**。
- **Quorum**: 未找到显式 quorum/minimum write concern 配置。etcd/raft 默认使用 majority quorum。

### 脏读保护

- ts-meta: Raft FSM 的 `Apply()` 和 `ApplyBatch()` 使用 `s.mu.Lock()` 串行化所有状态变更 (store_fsm.go:36)。
- metaclient: 100ms interval 增量缓存同步 + 10s 全量推送，客户端可能读到过期元数据 (`store.go:70-72`)。

---

## 数据持久化评估 (含 fsync 策略 + 丢失窗口)

### 架构层次

```
写入路径:
  Shard.WriteRows()
    -> WAL (engine/wal.go, 默认 sync interval 100ms)
    -> MemTable (mutable)
  
  Raft 数据复制:
    Propose()
    -> RaftDiskStorage.SaveEntries()  (写入 entry file)
    -> RaftDiskStorage.TrySync()      (异步 sync, 默认 interval 100ms)
```

### fsync 策略分析

| 组件 | fsync 时机 | 默认间隔 | 确认模式 | 文件 |
|------|-----------|---------|---------|------|
| WAL | 定时 sync goroutine | 100ms | `WalSyncInterval` | `lib/config/wal.go:24` |
| Raft Entry | 异步 backSync | 100ms | `RaftEntrySyncInterval` | `lib/config/raft_storage.go:24`, `lib/raftlog/storage.go:332-368` |
| TSSP File | 文件关闭时 | 无定时 sync | 由 compaction/刷盘触发 | — |

### 丢失窗口估算

对于 1B logs/s 写入速率:
- **最优情况**(首次 sync 后): 丢失窗口 ≈ 100ms (backSync sleep 时间)
- **最差情况**(sync 刚完成即 crash): 丢失窗口 ≈ 200ms (backSync 进行中 + 下一轮 sleep)
- **额外风险**: `syncTaskCount.CompareAndSwap` 在第 347-348 行静默跳过正在进行的 sync，连续两次写入之间若 crash 会丢失更多数据。
- **数据量**: 100ms 窗口 ≈ 1 亿 log lines 丢失。

### 关键代码路径

```go
// lib/raftlog/storage.go:347-348
// If a sync is already in progress, silently skip this one!
if !rds.syncTaskCount.CompareAndSwap(0, 1) {
    return nil   // <-- 写入已确认但未落盘
}
```

---

## RPO/RTO 评估

### 备份现状

备份功能位于 `lib/backup/`，仅提供文件/目录拷贝接口 (`backup.go:47-107`):

- `FileCopy(src, dst)` — 单文件拷贝
- `FolderCopy(src, dst)` — 递归目录拷贝
- `ReadBackupLogFile` / `WriteBackupLogFile` — JSON 元数据日志
- `FullBackupLog`, `IncBackupLog` — 全量/增量备份文件清单

恢复工具位于 `app/ts-recover/recover/recover.go:51-72`:
- 支持全量恢复 (`FullRecoverMode = "2"`)
- 支持全量+增量恢复 (`FullAndIncRecoverMode = "1"`)
- 通过网络从备份路径拷贝数据

### RPO 估计

| 场景 | RPO | 说明 |
|------|-----|------|
| 无备份 | ∞ | 未配置备份策略则无法恢复 |
| 手工全量备份 | 天级 | 依赖运维手动触发 |
| 手工增量备份 | 小时级 | 依赖运维手动触发 |
| 自动备份 | 不存在 | 无内置备份调度器 |

### RTO 估计

| 数据量 | RTO (预估) | 说明 |
|--------|-----------|------|
| 100 TB | 5-10 小时 | 千兆网络拷贝时间 |
| 1 PB | 50-100 小时 | 受限于文件拷贝 + TSSP 文件数量 |

### 核心不足

1. **无可恢复性验证**: 无恢复演练功能或恢复后一致性校验。
2. **无跨节点一致性**: 多节点备份非事务性，无法保证全局一致性点。
3. **无增量差异机制**: 增量备份仅追踪文件新增/删除列表 (lib/backup/backuplog.go:23-26)，无 block-level delta。
4. **无备份调度器**: `lib/backup/` 仅为底层库函数，无独立备份服务或 crontab 集成。
5. **RPO=RTO**: 恢复必须重放全量+所有增量，无法实现 point-in-time recovery。

---

## 过载保护矩阵

| 资源维度 | 限制机制 | 阈值/默认值 | 是否可配置 | 文件:行号 |
|---------|---------|-------------|-----------|----------|
| **内存(查询)** | InterruptQuery + InterruptSqlMemPct | 85% | 是 | lib/config/store.go:281-282 |
| **内存(PT分配)** | IsMemUsageExceeded() 阻止新 PT 分配 | 动态百分比 | 是 | engine/engine_ha.go:99-100, engine/sysctrl.go:307-316 |
| **OpenShard 并发** | openShardsLimit = limiter.NewFixed(options.OpenShardLimit) | cpuNum | 是 | engine/engine.go:132 |
| **写入并发** | WriteConcurrentLimit | 0 (无限制) | 是 | lib/config/store.go:347 |
| **Compaction 并发** | maxFullCompactor = cpuNum/2, maxCompactor = cpuNum | CPU 相关 | 否 | engine/immutable/compact.go:46-47 |
| **Shard 查询并发** | MaxConcurrencyInOnePt | 8 | 否(硬编码) | coordinator/shard_mapper.go:48 |
| **写入速率限制** | 无 | — | — | — |
| **磁盘空间** | 无 | — | — | — |
| **租户隔离** | 无 | — | — | lib/config/limits.go:25-40 (仅 Prom 字段级别限制) |
| **Raft 消息** | maxInflightMsgs=256, maxSizePerMsg=4096 | 256 / 4KB | 否(硬编码) | lib/raftconn/node.go:41-42 |

### 关键缺口

1. **写路径零反压**: 从 HTTP handler 到 WAL 完全同步，无缓冲/限流/队列。在 1B logs/s 场景下单个 ts-store 节点会被瞬间压垮。
2. **无磁盘保护**: 磁盘写满是不可恢复的崩溃场景 —— WAL 写入失败、compaction 写 TSSP 失败、文件操作 panic，都无降级路径。
3. **租户隔离缺失**: `Limits` 结构体 (`lib/config/limits.go:25-40`) 仅限 Prometheus 字段级别校验，不支持多租户 QPS/capacity 隔离。
4. **无 distributed rate limiter**: 所有限制都是单节点级别，全局写入速率无协调。
5. **HardWrite 策略**: `ha_policy.go:64-72` 定义了 `IsHardWrite()` 但缺乏对应的写入降级路径。

---

## 运维成熟度

| 能力 | 状态 | 证据 | 不足 |
|------|------|------|------|
| **Graceful Shutdown** | 部分支持 | `interruptsignal/signal.go:17-46` 提供信号处理通道；`lib/raftconn/node.go:534-546` 停止 Raft node 时关闭 channel | 无 preStop hook；无 draining connection 机制；shutdown 时无 inflight write 完成确认 |
| **PT 迁移/Move** | 支持 | `engine/engine_ha.go:33-64` PreOffload + `engine_ha.go:137-158` Offload + `engine_ha.go:160-256` Assign | 15s 超时硬编码 (`engine_ha.go:277`)；迁移中 IO 冲突检测 `ErrConflictWithIo`；无 data verification after move |
| **Leadership Transfer** | 支持 | `handler.go:442-454` `/leadershiptransfer` API；`lib/raftconn/node.go:281-314` TransferLeadership 带 10s 超时 | 手动触发，无自动 rebalance |
| **滚动升级** | 不支持 | rg 搜索 `rolling.*upgrade` 无结果 | 无版本兼容性检查；无 staged shutdown；无蓝绿部署支持 |
| **Decommission** | 不支持 | rg 搜索 `drain|decommission` 无结果 | 节点下线需要手动 PT 迁移再停进程 |
| **配置热加载** | 不支持 | rg 搜索 `SIGHUP` 无结果 | 配置变更需全量重启 |
| **健康检查** | 部分 | `/debug/varz` 等 HTTP 接口；leader 检测 `store.go:1547-1554` | 无 readiness probe；无 dependency health cascade |
| **可观测性 (自监控)** | 部分 | `lib/statisticsPusher/` 推送监控指标至 Kafka/HTTP | 监控推送失败不影响告警；无内置告警规则 |

### 关键运维风险

1. **滚动升级需要全集群停服**: 无版本兼容性保证，必须停机升级。
2. **无配置校验与回滚**: 配置错误会导致进程启动失败，无自动回滚能力。
3. **PT 迁移无数据校验**: `offload/assign` 过程完成后，源端数据直接删除，**无校验** 目标端是否完全一致 (`engine_ha.go:151` `offloadDbPT` 无 post-move verification)。
4. **无运维窗口度量**: 所有操作 (升级、迁移、备份) 无预估时间，无法制定 SLA。

---

## 安全评估

| 维度 | 状态 | 证据 | 不足 |
|------|------|------|------|
| **传输加密 (TLS)** | 支持 | SPDY 层完整 TLS 配置 (`lib/config/spdy.go:40-49`); 双向 mTLS 支持 (`spdy.go:108-113`) | **默认关闭** (`spdy.go:81` TLSEnable=false); inter-node 通信 TLS 需用户自行开启 |
| **认证授权** | 部分支持 | `AuthEnabled` 配置项 (`lib/config/meta.go:166`, `lib/config/store.go:487`); `lib/config/recordwrite.go:21` AuthEnabled | 仅控制读写 API 鉴权开关, 未发现 RBAC/ACL 实现; 认证方式不可插拔 (rg 搜索 jwt/oauth/oidc 无结果) |
| **密码管理** | 部分 | `lib/crypto/passkey_decipher.go` — 加解密工具 | 前置解密仅保护配置文件中的密码静态安全；无密钥轮换机制 |
| **监控凭据** | 明文存储风险 | `Password` 字段明文存在于多处 config (`lib/config/monitor.go:133,151,178`) | 监控目标密码存于配置文件；建议集成 secret store |
| **审计日志** | 不支持 | rg 搜索 `audit|Audit` 无结果 | 无元数据变更审计日志，无法追踪"谁在何时改了什么" |

---

## 韧性改造分阶段路线图

### Phase 1 (紧急修复, 1-2 个月)

1. **Fence 默认启用** (F1): 修复 `engine/fence.go:23-33`, 使 noop fencer 在非 streamfs 构建下也提供基础文件级保护。配置变更即可关闭。
2. **磁盘水印保护** (F5): 在 `shard.go` 和 `wal.go` 写入前检查磁盘剩余空间，`disk-full` 状态时优雅拒绝写入并返回 `DiskFull` 错误码。
3. **WAL sync 策略可选** (F4): 提供 `"sync"` 和 `"async"` 两种 fsync 模式，同步模式每次写入后 `TrySync`，允许用户在性能与持久性间权衡。

### Phase 2 (核心韧性, 3-6 个月)

4. **写入路径 Admission Control** (F6): HTTP handler 层增加 token bucket rate limiter，`coordinator/points_writer.go` 增加有界缓冲队列 + backpressure signal。
5. **Per-Query Memory Budget** (F7): 替换全局 `InterruptSqlMemPct` 为 per-query memory limit，引入 query queueing。
6. **自动备份调度器** (F9): 基于 `lib/backup/` 封装独立 backup service，支持 `cron` 表达式调度、自动全量/增量、定期恢复演练。
7. **跨节点一致性备份** (F9): 利用 Raft snapshot + WAL archive 实现集群级一致性备份点，RPO 降至秒级。

### Phase 3 (高级韧性, 6-12 个月)

8. **滚动升级框架**: 实现 staged shutdown、version compatibility check、graceful connection drain。
9. **多租户隔离**: 在 `Limits` 基础上增加 per-tenant QPS 限流、存储配额、查询优先级。
10. **配置热加载**: 支持 `SIGHUP` 信号触发配置重载，减少重启次数。
11. **Decommission 自动化**: 自动 PT 迁移 + 数据确认 + 节点下线。
12. **租约 + Lease-based failure detection**: 替代被动心跳检测，加速故障转移，减少脑裂窗口。

---

## 总引用清单 (15+ 文件:行号)

1. `app/ts-meta/meta/store_fsm.go:33-75` — FSM ApplyBatch 单锁串行化
2. `app/ts-meta/meta/store.go:1521-1532` — waitForLeader 固定间隔轮询
3. `app/ts-meta/meta/raft_wrapper.go:90-94` — Hashicorp Raft 配置构建
4. `lib/config/meta.go:38-40` — meta Raft 超时配置
5. `engine/engine_ha.go:200-209` — Fence 调用 (split-brain 防护)
6. `engine/fence.go:20-33` — 默认构建 fencer 为 no-op
7. `engine/fence_streamfs.go:39-113` — streamfs fencer 实现 (文件锁+WAL seal)
8. `lib/raftconn/node.go:40-46` — 数据 Raft 参数 (tick, msg size, inflight)
9. `lib/raftconn/node.go:108-121` — 数据 Raft 配置 (ElectionTick, HeartbeatTick)
10. `lib/config/store_raft.go:21-25` — 数据 Raft 默认参数
11. `lib/raftlog/storage.go:332-368` — TrySync + backSync 异步 sync 模式
12. `lib/raftlog/entrylog.go:344-345` — entry 文件 fsync
13. `lib/config/wal.go:24` — WALSyncInterval 默认 100ms
14. `lib/config/raft_storage.go:24` — RaftEntrySyncInterval 默认 100ms
15. `lib/config/ha_policy.go:22-72` — HA 策略枚举与配置
16. `coordinator/shard_mapper.go:48` — MaxConcurrencyInOnePt=8
17. `coordinator/points_writer.go:93-100` — PointsWriter 无反压结构
18. `lib/config/store.go:281-282,347,353-354` — 过载保护配置
19. `engine/sysctrl.go:307-316` — IsMemUsageExceeded() 内存保护
20. `engine/immutable/compact.go:46-47` — compaction 并发限制
21. `lib/backup/backup.go:47-107` — 文件级拷贝备份
22. `app/ts-recover/recover/recover.go:51-72` — 恢复工具
23. `lib/interruptsignal/signal.go:17-46` — 信号处理 (graceful shutdown)
24. `lib/config/spdy.go:40-49,102-116` — TLS/mTLS 配置与证书验证
25. `lib/config/limits.go:25-40` — Limits 结构 (仅字段级别校验)
26. `lib/config/store.go:306-308` — ClearEntryLogTolerate 配置
