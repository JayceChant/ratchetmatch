# Tasks

全部任务已完成（历史实现进度，供追溯；契约与算法见 spec.md）：

- [x] Task 1–2: module 与 API 骨架；rune 级 Trie 构建与词库校验（空词库/空串报错、去重）
- [x] Task 3–4: BFS 失败指针与输出链 outLens；BM 坏字符跳跃表（首字符集 + 256 位字节过滤器）
- [x] Task 5–6: 单遍扫描与结果收集（FindAll / FindNext）；单元测试、500 组随机对照、-race 并发
- [x] Task 7–8: 可运行示例与基准（混合文本跳跃 ~1.4x、FindNext ~10x）；gofmt/vet/lint/test 全链路通过
- [x] Task 9: 稀疏转移 + fail 回退优化（替代 DFA 全量表，决策见 spec「转移表内存布局」）：New 快 1.3–2.9x、allocs 减半，扫描不回退
- [x] Task 10: FindAllOverlapping 重叠全量返回（独立循环，默认路径零改动）
- [x] Task 11: 测试强化与 Go 原生 fuzz（白盒语义重推导、黑盒边界、fuzz 不变量 oracle；449 万 execs 零失败，两项 fuzz 发现转正为修复）
- [x] Task 12: 基准新增朴素多模式对照（`naiveMultiFindAll` / `naiveMultiFindNext`：逐关键词 strings.Index 枚举 + 最左最长归并 / 首命中即停一轮扫描；TestNaiveMultiEquiv 守卫参照与 FindAll/FindNext 在基准语料下等价）。结论：50 词 × 10 万 rune 文本，FindAll 快 ~5.2x（中文）/ ~5.2x（混合）、FindNext 快 ~7.6x；既有基准全部保留无回退
- [x] Task 13: 基准参照重构为三基线体系。naiveFindAll（同自动机无跳跃对照）改为 trieFindAll（纯 Trie 重启：复用 CSR 边与 rootNext、失配即回 root；词尾判定 outLens[0]==路径字节长——成品节点不存 termLen，fail 链继承的真后缀严格更短故等式精确）；新增 bmFindAll（教科书坏字符表逐词搜索）；naiveMulti* 更名 stringsIndex*（体现底层原理）；逐词归并抽为 collectLeftmostLongest 共用；等价守卫改为 TestBaselineEquiv（三参照 × 两语料 + 首命中）。实测（50 词 × 10 万 rune）：中文 FindAll 1.62ms vs Trie 1.52ms（~0.9x，持平属预期：该语料 fail 链收益小、跳跃无空间）/BM 9.13（~5.6x）/Index 9.25（~5.7x）；混合 0.94ms vs Trie 1.26（~1.3x）/BM 6.31（~6.7x）/Index 4.82（~5.1x）；首停 0.089ms vs 0.669ms（~7.6x）。同自动机无跳跃隔离对照随之移除（改以三基线呈现；历史跳跃收益 ~1.4–2x 见 2026-08-29/31 记录）

新增任务按 `Task N: 描述` 追加于上表。

# 实现期修正记录（防回归：勿以「简化」或「优化」为由重蹈以下覆辙）

- **匹配语义由「先命中优先」改为非重叠最左最长**：原贪心在真包含词库下输出碎片（{国,人,中国人}+"中国人" → 国、人），不符合设计意图——长词进行中优先延续，断词才 fail 结算短词。scan 由单值 pending 改为「待提交链」（规则与 root 时刻提交的安全性证明见 spec）。
- **maxOut 单值遮蔽缺陷**：每个结束位置只暴露最长关键词时，其被 pending 遮蔽会连带丢失同位置更短的兼容候选，导致 FindAll 与 FindNext 迭代不一致（随机测试 59/2000 组分歧）。修正：节点存全部输出长度 outLens（降序）。
- **必死候选弹链导致空档**（fuzz 发现）：词库 {0,000} 文本 "000000000001" 中，更左但被遮蔽的候选弹出链尾，为它让位的合法命中被丢弃，[9,10) 成空档，且无状态 FindNext 无法复现。修正：被遮蔽候选无权改变链（弹出可恢复）；flush 移至 root 循环头（消除单 rune 词的时序漏洞）；FindNext 改整链首条产出；naiveSearch 参照改写为独立的教科书式重启贪心，不与被测链规则同构。
- **New 词库校验加固**（fuzz 发现）：关键词非法 UTF-8 在 rune 层坍缩为同一 RuneError（身份歧义），规范 U+FFFD 的 3 字节编码与查询端逐字节 RuneError 前进不一致（长度歧义，极端情形切片 panic）。二者均拒绝入词；文本侧非法字节不受影响（仍逐字节处理）。
- 2026-08-31 文档精简：README 收敛为用户视角（选型依据与权衡），spec/tasks/checklist 压缩历史、保留设计决策与修正记录。
- 2026-08-31 README 双语化：英文版置 README.md，中文原版迁移至 README_CN.md，两文件开头互链；正文内容与迁移前一致，无行为变化。
- 2026-08-31 更名 ratchetsearch → ratchetmatch：原名「search」与本库模式匹配语义不符且与 API 家族（Find*/Match）不一致，"ratchet search" 又是系统发生学既有术语、Go 生态另有知名同名工具，检索混淆重。新名保留棘轮隐喻（文本指针单向只进不退），以 match 修正语义。仓库无下游引用，零迁移成本。
- 2026-08-31 确定许可证为 MIT：宽松许可（允许闭源商用，仅要求保留版权声明）+ 不含专利条款（本人决策，算法库专利风险自评可控）+ 企业采用友好（MIT 认知度最高、法务审查零成本）。署名 Jayce Chant（陈思杰）。
- 2026-09-01 README 性能段前移至「适用范围」之后、「设计与权衡」之前，朴素多模式对比由行文改为表格呈现（双语同步，无行为变化）。
- 2026-09-01 基准参照体系改版：NoSkip（同自动机去跳跃）对照被纯 Trie 重启基线取代，跳跃收益不再能从基准套件直接复现（历史值保留于本文件与 checklist）；三基线（Trie/BM/strings.Index）与正式 API 的等价由 TestBaselineEquiv 在基准语料上锁定。勿再以为基准中存在无跳跃对照。
- 2026-09-01 朴素基线形态定论（分析结论，未改基准代码）：strings.Index 已是标准库 SIMD（internal/bytealg，AVX2/SSE2）最快单串搜索；「文本下标外循环 × 逐词逐字节比较」同为 O(K·n) 但常数差一个向量宽度（~1500 万标量迭代 vs ~50 万向量迭代），只会持平或更慢。更强的「首字节位图+同首字符桶」本质是深度 1 trie（与被测 byteFilter/rootNext 同构），不算朴素基线，不入基准；理由固化于 bench_test.go naiveMultiFindAll 注释与 README。
