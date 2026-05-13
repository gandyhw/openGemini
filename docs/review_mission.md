# Claude Code 任务指令 · openGemini RCA 重构评审

> **使用方式**：将本文件全文粘贴到 Claude Code 会话首条消息，或保存为仓库根目录的 `REVIEW_MISSION.md` 后让 Claude Code 读取。Claude Code 会按下面的阶段自动推进。

---

## 1 · 任务定义

你是 openGemini 重构评审任务的执行者。你的目标:对 `https://github.com/openGemini/openGemini` 进行系统化评审,产出一份可用于驱动重构的工程报告,使其能够支撑下述生产规模的 **Ops RCA(根因分析)** 场景:

| 维度 | 目标量级 |
|---|---|
| 指标写入 | 1 亿 metrics / 秒 |
| 日志写入 | 10 亿 logs / 秒 |
| 实体数(host/svc/pod) | 10 万 |
| 拓扑关系(边) | 100 万 |
| 查询场景 | 跨 metric / log / trace / topology 的关联下钻,秒级响应 |

评审维度:**架构 / 性能 / 高质量完备性 / 韧性** 四个,分别由四个 subagent 在阶段 2 并行执行。

---

## 2 · 工作目录与产出物约定

在仓库目录之外建立独立工作区,避免污染源码:

```
./review_workspace/
  ├── 00_recon.md              # 阶段 1 全局侦察笔记
  ├── 10_architecture.md       # subagent A 报告
  ├── 11_performance.md        # subagent B 报告
  ├── 12_quality.md            # subagent C 报告
  ├── 13_resilience.md         # subagent D 报告
  ├── 20_cross_review.md       # 阶段 3 交叉评审纪要
  ├── 30_executive_summary.md  # 最终执行摘要 (≤ 2 页)
  ├── 30_risk_register.csv     # 风险登记册
  ├── 30_roadmap.md            # 0/3/6/12 月路线图
  ├── 30_poc_list.md           # 必须实测验证的假设
  └── evidence/                # 摘录的代码片段、命令输出
```

**所有结论必须满足**:
- 附 `相对路径:行号` 或 `commit hash` 作为证据(可用 `evidence/` 下的引用文件)
- 用数字说话(QPS、p99、覆盖率%、行数、内存 MB);无法直接测得就给出推算公式与假设
- 对照目标规模而不是"通用够用"
- 每条建议带成本档位:`S ≤1w / M 1-4w / L 1-3mo / XL >3mo`
- 区分 **现状(is) / 缺陷(defect) / 风险(potential at target scale)**

---

## 3 · 阶段 0 · 准备

```bash
mkdir -p review_workspace/evidence
cd review_workspace && touch 00_recon.md
# 克隆仓库到工作区相邻目录
git clone --depth=200 https://github.com/openGemini/openGemini ../opengemini
cd ../opengemini
git log -1 --format='%H %ai %s' > ../review_workspace/evidence/HEAD.txt
```

检查 toolchain(没有就向用户报告并退化):
- `go version` (项目应为 Go 1.21+)
- `rg --version`
- `tokei --version` 或 `cloc --version`
- `golangci-lint --version`
- `gocyclo --help`
- `staticcheck -version`

**先用 TodoWrite 工具把阶段 1–4 拆成可勾选的 TODO 列表,再开始执行。**

---

## 4 · 阶段 1 · 全局侦察(单线程,你自己执行)

目标:建立全局心智模型,让阶段 2 的 subagent 不会再重复做同样的探索。产出 `00_recon.md`。

**必做动作**:

1. **代码盘点**
   ```bash
   tokei --sort=lines | head -40
   # 各组件代码量
   for d in app/ts-meta app/ts-sql app/ts-store app/ts-monitor app/ts-server engine coordinator; do
     echo "=== $d ==="; tokei "$d" 2>/dev/null | tail -3
   done
   ```
2. **包依赖**
   ```bash
   go list -deps ./... 2>/dev/null | head -50
   go mod graph | head -50
   ```
3. **组件入口与启动链路**
   - 用 `rg "func main"` 找到 ts-meta / ts-sql / ts-store 等入口
   - 用 `rg "RegisterService|NewServer|StartServer"` 找服务装配点
4. **核心数据流**
   - 写入入口:`rg -l "WritePoints|WriteRows" --type=go`
   - 查询入口:`rg -l "ExecuteStatement|SelectStatement" --type=go`
   - 存储引擎:`engine/` 目录结构
   - 元数据:`meta/` 或 `coordinator/`
5. **多模能力定位**
   - Metrics:`measurement|series|tagset`
   - Logs:`logs?|fulltext`
   - Trace:`otlp|trace|span`
   - Topology:**重点确认是否存在原生支持**
6. **测试与 benchmark 现状**
   ```bash
   rg -l '_test\.go$' --files | wc -l
   rg -l 'Benchmark[A-Z]' --type=go | head -20
   ```
7. **历史热点(最常改动文件)**
   ```bash
   git log --pretty=format: --name-only --since='12 months ago' \
     | sort | uniq -c | sort -rn | head -30
   ```

`00_recon.md` 输出框架:
```
## 仓库地图
（关键目录与代码量、各组件职责一句话）

## 关键数据流路径
- 写入:文件 A:行 → 文件 B:行 → ...
- 查询:...
- 元数据更新:...

## 多模能力初判
- Metrics:✅ / Logs:? / Trace:? / Topology:?
  （每条带代码证据)

## 已知缺口的初步假设(供后续 subagent 验证)
- 假设 1: ...
- 假设 2: ...

## 风险热区
（历史改动 top 10 文件,可能是技术债集中地）
```

完成后向用户报告 recon 完成,等用户 ✅ 后再进入阶段 2(防止全自动跑飞)。

---

## 5 · 阶段 2 · 四视角并行评审(Task 工具派发 subagent)

**强制**:用 `Task` 工具派发四个 subagent **并行** 执行。每个 subagent 收到一份独立 prompt(下方 5.1–5.4),并被告知:
- 在开始前先读 `review_workspace/00_recon.md` 避免重复探索
- 产出独立报告到指定文件
- 不要修改源码,只读
- 把代码片段证据放进 `review_workspace/evidence/<subagent_name>/`

### 5.1 · Subagent A · 架构评审

> 你是 openGemini 重构评审团队的**首席架构师**。先读取 `review_workspace/00_recon.md`,然后在 `../opengemini` 仓库内完成下列评审,产出到 `review_workspace/10_architecture.md`。
>
> **目标规模**:1 亿 metrics/s、10 亿 logs/s、10 万实体、100 万拓扑边、生产 RCA 场景。
>
> **必查清单**:
> 1. ts-meta / ts-sql / ts-store 三层职责边界在目标规模下是否依然合理?是否需要拆出独立 ingestion gateway / query coordinator?用 `rg` 定位关键边界代码作为证据。
> 2. ts-meta 在 100K 实体 × 高基数 tag 下是否会成为瓶颈?元数据一致性协议(Raft 实现)的扩展边界。定位代码:`rg -i "raft|meta.*sync"`.
> 3. 多模数据模型:Metrics / Logs / Trace / Topology 当前如何各自组织?是否共用 storage engine?跨模查询是否在查询计划层原生支持?**特别确认 1M 拓扑边当前用什么承载**(measurement 模拟?独立结构?根本没有?)。
> 4. shard / partition / index 抽象在 100K 实体 × N 维 tag 下的 shard 爆炸风险评估。
> 5. 写入与查询关键路径上的串行点、反压机制端到端是否贯通。
> 6. 查询语义覆盖度:PromQL / InfluxQL 之外,LogQL 类全文检索、图遍历(k-hop)、跨模 join 缺什么?
>
> **产出格式**:
> - 用 Mermaid 画 C4 Level 1–2 现状图
> - 风险矩阵表:`风险项 / 影响面(单点/局部/全局) / 概率(低/中/高) / 证据`
> - 重构建议表:`现状 → 问题 → 方案 → 成本档(S/M/L/XL) → 收益`
>
> 全程严格"证据驱动":每条结论必须附 `文件:行号` 或 commit hash。

### 5.2 · Subagent B · 性能瓶颈分析

> 你是 openGemini 重构评审团队的**性能工程师**。先读 `review_workspace/00_recon.md`,然后在 `../opengemini` 仓库内完成下列分析,产出到 `review_workspace/11_performance.md`。
>
> **目标规模**:1 亿 metrics/s、10 亿 logs/s。
>
> **必查清单**:
> 1. 写入路径热点:WAL、memtable、index build。用 `rg "func.*Write|func.*Insert"` 定位。10 亿 logs/s 是否需要前置 Kafka/Pulsar 解耦?当前架构是否预留接口?
> 2. 序列化/反序列化(Line Protocol / Protobuf / JSON):CPU 占比估算。
> 3. LSM-tree compaction 写放大估算:扫 `engine/` 下 compaction 策略代码,基于代码可见的 level 配置算。
> 4. 列存压缩比 15:1 在**日志高基数 / 长字符串 / 半结构化**场景的现实性评估。
> 5. 向量化执行算子覆盖率:`rg -i "vector|vec\.|simd"` 看覆盖了哪些算子,哪些走 row-based fallback。
> 6. Go runtime:GC pressure 估算、`sync.Pool` 使用密度(`rg "sync\.Pool"`)、goroutine 泄漏风险点(`rg "go func\("` + context 传播)。
> 7. 仓库自带 benchmark 现状:`rg -l "func Benchmark"`,与目标规模的差距倍数。
>
> **产出格式**:
> - 热点函数清单(按预估 CPU/mem 占比排序,带证据)
> - 每条:`位置 / 瓶颈机理 / 优化路径 / 预估提升`
> - 规模缺口表:`当前 / 目标 / 缺口倍数 / 单点优化能否填平`
>
> 不能跑 profile 就说明"基于代码静态推断",并给出推算假设。不允许凭印象。

### 5.3 · Subagent C · 代码质量与 RCA 完备性

> 你是 openGemini 重构评审团队的**代码质量与业务完备性评审师**。先读 `review_workspace/00_recon.md`,然后在 `../opengemini` 仓库内完成下列分析,产出到 `review_workspace/12_quality.md`。
>
> **必查清单**:
>
> **A. 工程基线**:
> 1. 测试覆盖率(若可运行):`go test -short -cover ./... 2>&1 | tee evidence/coverage.txt`(超时则改 per-package)。
> 2. `go vet ./...` 与 `staticcheck ./...`(如可用)的告警数,分类汇总。
> 3. 单测/集成测/chaos 测试覆盖比例;关键路径(写、查、副本)是否覆盖。
> 4. godoc 完整度抽样:对 5 个核心 package 检查 exported symbol 是否有注释。
> 5. 错误处理 anti-pattern:`rg "_ = .*\.Close|recover\(\)|panic\("` 抽查滥用情况。
>
> **B. RCA 业务能力对齐**(**重点**):
> 6. **Trace**:OTLP 完整 span / span event / resource attribute 是否原生支持?`rg -i "otlp|opentelemetry"`.
> 7. **拓扑**:100 万拓扑边的存储与图查询语义(k-hop、最短路径、影响面)是否存在?如果没有,补齐成本估计。
> 8. **事件 / annotations** 与时序的对齐机制。
> 9. **AI4DB / 异常检测 / 根因推断**:成熟度、是否生产可用、与查询引擎的集成深度。`rg -li "anomaly|forecast|ai4db"`.
> 10. **跨模统一查询语言或 API** 是否存在。
>
> **C. 治理与自观测**:
> 11. schema 演进、TTL、retention 在多模场景下是否协调。
> 12. 数据回填、按行删除(GDPR 类)能力。
> 13. 数据库自身的 metrics / logs / traces 埋点完备性。
>
> **产出格式**:
> - 工程质量评分卡(测试/文档/接口/静态检查 × 1–5 分)
> - **RCA 能力对齐表**:`业务需求 ↔ 现状 ↔ 缺口 ↔ 补齐方案 ↔ 成本档`
> - 代码债优先级清单(带 `文件:行号`)

### 5.4 · Subagent D · 韧性与可靠性

> 你是 openGemini 重构评审团队的 **SRE / 韧性评审师**。先读 `review_workspace/00_recon.md`,然后在 `../opengemini` 仓库内完成下列分析,产出到 `review_workspace/13_resilience.md`。
>
> **必查清单**:
> 1. **HA**:ts-meta 一致性协议实现,leader 选举、脑裂、网络分区行为。`rg -i "election|leader|split.brain|partition"`.
> 2. **副本**:ts-store 副本数、quorum、一致性窗口。`rg -i "replic|quorum"`.
> 3. **故障域**:单 shard / 单节点 / 单 AZ 故障的爆炸半径定量估计。
> 4. **慢/大查询隔离**:timeout、cancel context 传播链。`rg "context\.WithTimeout|ctx\.Done"`.
> 5. **背压**:从 store → sql → client 的反压代码是否端到端贯通。
> 6. **WAL 持久化**:fsync 策略,各级别数据丢失窗口。`rg -i "fsync|sync\.Write|WAL"`.
> 7. **备份恢复**:RPO/RTO 估算,目标规模下是否实际可行。
> 8. **过载**:多租户隔离、限流、OOM / disk full / fd 耗尽下的行为(grep panic & exit)。
> 9. **运维**:滚动升级、在线扩缩容、配置热更新。
> 10. **安全**:认证授权、TLS、cardinality bomb 防护、依赖 CVE(`govulncheck` 若可用)。
>
> **产出格式**:
> - **FMEA 表**:故障模式 / 触发 / 影响 / 当前缓解 / 改进 / 严重度 1–5
> - RPO / RTO 评估
> - 韧性改造分阶段路线图

---

## 6 · 阶段 3 · 交叉评审

四个 subagent 完成后,**你自己**(主 agent)读取四份报告,执行:

1. **去重**:不同 subagent 对同一问题的描述合并。
2. **冲突识别**:列出三类冲突
   - A 建议拆模块、B 认为该模块是性能不可动的核心
   - C 报告测试覆盖率良好、D 报告关键失败路径无测试
   - 任何"事实层面互斥"的结论
3. **二次取证**:对冲突项主动重读代码,在 `20_cross_review.md` 给出仲裁与证据。
4. **重打分**:用类 RICE 公式 `Reach × Impact × Confidence / Effort` 给所有问题统一排序。

完成后再次向用户报告,等用户 ✅ 后进入阶段 4。

---

## 7 · 阶段 4 · 综合与路线图

产出四份最终交付物:

1. **`30_executive_summary.md`** (≤ 2 页)
   - Top 10 风险(按 RICE 排序)
   - Top 5 重构建议
   - 是否建议基于此仓库重构 vs. 自研 vs. 选择其他底座 的明确意见与理由

2. **`30_risk_register.csv`**
   列:`ID, 维度, 标题, 现状, 缺陷或风险, 影响面, 概率, 严重度, 证据(文件:行号), 建议, 成本档, 优先级`

3. **`30_roadmap.md`**
   分 0–3 / 3–6 / 6–12 月三档,每条目:
   - 目标(可验收的)
   - 涉及模块
   - 依赖项
   - 验收标准(必须用目标规模子集压测验证)
   - 预估人力

4. **`30_poc_list.md`**
   必须先做 POC 验证的假设,推荐包含:
   - 1B logs/s 写入压测:单节点上限与横向扩展线性度
   - 1M 拓扑边的 k-hop 查询能否秒级返回
   - 100M metrics/s 下 ts-meta 元数据更新稳态延迟
   - 高基数(>100M timeseries)下 index 内存与查询延迟
   - 每条 POC:目标假设 / 验证方法 / 通过条件 / 预估耗时

---

## 8 · 强制规则(违反直接重做该步骤)

1. **不修改源码**。本次任务全程只读。
2. **每条结论必须有证据**:`文件:行号` 或 commit hash;长片段放 `evidence/` 引用。
3. **不输出空话**:"建议加强测试"这种没有指向的结论一律禁止。
4. **量化**:任何"慢"、"多"、"高"必须给数字或推算式。
5. **对标目标规模**:不写"一般场景够用"。
6. **每个阶段结束**:用 TodoWrite 勾选完成、向用户报告并等待确认。
7. **subagent 必须并行派发**,不能串行。
8. **遇到工具不可用**:报告"工具缺失 + 降级方案",不能静默跳过。

---

## 9 · 启动

读完本指令后,先回复:
1. 你已理解的任务摘要(≤ 5 行)
2. 你检测到的本机 toolchain 状态(列出 8 个工具的可用性)
3. 阶段 1 即将读取的 ≤ 20 个关键文件清单
4. 等待用户回复 `go` 后再开始动手。
