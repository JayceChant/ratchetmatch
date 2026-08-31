# ratchetsearch：中文优化 ACBM 多模式匹配库 Spec

## Why
全新 Go 1.27 组件仓库需要实现一个针对中文优化的 ACBM（Aho-Corasick + Boyer-Moore）字符串匹配库：给定词库 V（多个关键词 W），对每条输入的长文本 T 单遍扫描，返回 T 中命中的关键词位置，采用非重叠最左最长语义（leftmost-longest：起点最小优先、同起点取最长，关键词真包含关系一律输出最长）。现有通用实现未针对中文 rune 特点优化，且缺少明确的命中优先级语义。

## What Changes
- 新建 Go module（`go.mod` 声明 `go 1.27`，module 名 `ratchetsearch`）
- 核心公共 API：
  - `New(keywords []string) (*Matcher, error)`：构建不可变 Matcher
  - `(*Matcher) FindAll(text string) []Match`：返回全部命中（按 Start 升序）
  - `(*Matcher) FindAllOverlapping(text string) []Match`：重叠全量返回（全部出现，含互相重叠者；按 End 升序、同 End 长度降序），服务统计类场景
  - `(*Matcher) FindNext(text string, offset int) (Match, bool)`：无状态按需查找，从 offset（字节偏移）开始查找第一个命中，找到即终止扫描；comma-ok 形式返回，无命中时返回零值 Match 与 false
- 构建期：rune 级 Trie（支持中文多字节字符）+ 失败指针（BFS 构建），节点持稀疏转移表与失败指针（查询期段内查找 + 沿失败链回退，摊还 O(1)，表不膨胀）
- BM 风格跳跃（坏字符规则）：自动机处于 root 态时，利用「词库首字符集 + 256 位字节过滤器」直接跳过不可能出现匹配起始的文本段
- 匹配语义（非重叠最左最长）：起点最小优先，同一起点取最长；命中后跳过被覆盖区间，每个文本位置至多属于一个命中；结果按出现先后（Start 升序）排序
- 单元测试（含中文、最左最长语义、跳跃不漏报的随机对照测试、`-race` 并发测试）、使用示例、Benchmark

## Impact
- Affected specs: 无（本仓库首个 spec）
- Affected code: 全新仓库；新增 `go.mod`、`ratchetsearch.go`（API）、`build.go`（Trie/失败指针/自动机/跳跃表）、`search.go`（扫描）、`ratchetsearch_test.go`、`example_test.go`、`bench_test.go`

## Non-Goals
- 大小写折叠、全半角归一化、正则、编辑距离匹配
- 流式（分块）输入接口；关键词的动态增删（构建后不可变）
- 重叠模式的按需查找（`FindNextOverlapping`）：重叠语义与「从 offset 重扫的无状态迭代」天然不合——返回中国人[0,9) 后以 End 推进必然漏掉其内部更早起点的出现（如 国[3,6)），而带回退的游标 API 属另一量级改动，不做
- 基于 rune 下标的位置/长度 API（决策依据与量化代价见「偏移计量单位」需求）

## ADDED Requirements

### Requirement: 词库构建与校验
系统 SHALL 接受一个非空关键词列表，构建不可变的 `Matcher`：
- 词库为空（nil 或长度为 0）或包含空字符串 `""` 时，`New` 返回错误
- 重复关键词自动去重（只保留一份）
- **关键词不得包含规范编码的 U+FFFD（REPLACEMENT CHARACTER，字节 `EF BF BD`）**：`New` 返回错误。理由（2026-08-31 fuzz 发现）：查询端 `utf8.DecodeRuneInString` 把非法字节按 RuneError 处理且仅前进 1 字节，与该字符的规范编码（3 字节）存在长度歧义——若允许入词，命中区间无法同时满足 `text[Start:End] == Keyword` 与 rune 边界不变量（极端情形回推出负偏移导致 panic）。含非法 UTF-8 字节的关键词不受影响：其按 rune 迭代得到的 RuneError 与查询端逐字节解码行为自洽
- Trie 按 **rune**（Unicode 码点）构建，中文按整字符处理，绝不按 UTF-8 字节碎片转移
- 每个关键词的末节点带终止标记，表示该关键词的完整结束（必须完整匹配才命中）

#### Scenario: 中文词库构建成功
- **WHEN** `New([]string{"你好", "世界", "你好世界"})`
- **THEN** 构建成功无错误，后续可命中全部三个关键词

#### Scenario: 非法词库报错
- **WHEN** `New(nil)`、`New([]string{})` 或 `New([]string{"a", ""})`
- **THEN** 返回非 nil error，且 error 信息可区分原因

#### Scenario: 拒绝非法 UTF-8 与含 U+FFFD 的关键词
- **WHEN** `New([]string{"a\uFFFD"})`、`New([]string{string([]byte{0xB8})})` 或 `New([]string{"a\xffb"})`
- **THEN** 均返回非 nil error，错误信息可区分原因（分别含 "U+FFFD" 与 "not valid UTF-8" 字样）；合法 UTF-8 词库不受影响。文本侧的非法字节仍按 RuneError 逐字节处理，不 panic、不漏扫

### Requirement: 失败指针（回退指针）
系统 SHALL 在构建期通过 BFS 为每个 Trie 节点计算失败指针，语义为：
- 指向「当前已匹配部分的最长真后缀（该后缀同时是词库中某关键词的前缀）」对应的 Trie 节点
- 若不存在这样的后缀，则指向 root（表示需从 root 重新开始）
- 构建完成后，节点持有稀疏转移表（仅自有 trie 边）与失败指针：转移为段内查找 + 至多沿失败链回退重试（回退链平均 1–2 步，摊还 O(1)），无构建期 DFA 全量解析的表膨胀

#### Scenario: 失败指针正确性
- **WHEN** 词库含 {"上海", "海口"}，文本为 "上海口"
- **THEN** 匹配完 "上海" 后遇到 '口' 无法下行时，失败转移仍使扫描继续；最终按最左最长语义输出 "上海"

### Requirement: 转移表内存布局（perf 优化，perf/optimize-automaton 分支）
系统 SHALL 以「稀疏转移 + 查询期失败指针回退」取代 DFA 全量表，控制中文大字符集下的内存膨胀：
- 每节点转移表仅存**自有 trie 边**（该节点在 Trie 中的直接孩子），展平进全局 CSR 有序数组（`keys []rune` 升序 / `vals []int32` 平行），节点只存 `base/count/fail`；DFA 全量解析会使非叶节点表膨胀至 root 扇出（中文词库 ~百键），内存不可接受，故废弃
- 查询期转移：先在节点段内查找（段宽 ≤16 线性扫描，>16 二分），未命中沿 `fail` 回退重试，直到 root；回退链平均长度 1–2，摊还 O(1)
- root 态转移与跳跃判断共用一张 `map[rune]int32`（词库**首字符集**）：任何匹配的起始 rune 必为词首字符，以其做跳跃判据等价安全且比全字符 runeSet 更精确（跳过「词中字符非词首」的徒劳停留）
- 构建期失败指针计算改用带回退的查找（不再依赖已解析的全量表）；输出链 outLens 语义不变
- 目标：扫描 ns/op 相对 map 全量表基线无明显回退（≤5%）；Matcher 成品内存与构建分配显著下降

#### Scenario: 构建内存缩减
- **WHEN** 以 BenchmarkNew100/1k/10k 三档词库规模对比优化前后
- **THEN** B/op 与 allocs/op 显著下降，ns/op 不显著回退

#### Scenario: 扫描性能不回退
- **WHEN** 运行既有 BenchmarkFindAllChinese/Mixed 与 FindNextFirst
- **THEN** ns/op 相对 map 基线无明显回退（混合文本跳跃收益 ~1.4x、FindNext ~10x 基线保持）

#### Scenario: 语义与并发不变
- **WHEN** 全部既有测试（含随机对照、FindNext 迭代一致性、-race 并发）
- **THEN** 结果与 map 版完全一致；Matcher 构建后仍为只读、查询期零分配

### Requirement: 单遍扫描与单调前进
系统 SHALL 对输入文本 T 按 rune 单遍扫描：
- 当前 rune 在自动机中完成转移（可下行则下行，否则沿失败指针回退后重试，摊还 O(1)）
- T 上的指针单调向前，每消费一个 rune 前进一次，绝不回退；只有自动机状态在移动
- 到达终止状态时，取以当前文本位置**结尾**的最长关键词作为候选
- 文本中出现非法 UTF-8 字节时按 `utf8.RuneError` 处理并前进 1 字节，不 panic、不漏扫后续内容

#### Scenario: 长文本单遍完成
- **WHEN** 输入任意长中文文本
- **THEN** 扫描严格一遍结束，时间复杂度为 O(|T|/rune × 每 rune 摊还 O(1) 转移 + 命中处理)

### Requirement: 偏移计量单位（设计决策，勿重复讨论）
系统 SHALL 维持「rune 转移 + 字节计量」，不提供基于 rune 下标的位置/长度 API（2026-08-29 评估后否决）：
- 自动机本已按 rune 转移；仅位置（`Match.Start/End`、`FindNext.offset`）与长度（outLens）按字节计量，保证 `text[Start:End] == Keyword` 可直接切片（Go 惯用法）
- `[]rune` 预转换路径的代价：瞬时内存纯 ASCII +300%、中文 +33%；字节级 BM 跳跃失效（全部字节已解码，混合文本 ~1.4x 收益归零）；FindNext 首命中即停被全量解码破坏（~10x 基线）；查询期由零分配变为一次大分配
- 并行维护 runeIdx 路径无净收益：字节位置因 API 契约仍须保留，双份簿记
- 该方案可消除的仅 FindNext 入口 rune 对齐检查（至多 3 次迭代，O(1) 极小常数）；终节点结束标记（outLens）与偏移单位无关，仍须保留
- 改 rune 下标属 **BREAKING**（切片惯用法失效），除非出现明确新需求，不重启讨论

### Requirement: 节点表示（设计决策，勿重复讨论）
系统 SHALL 维持「下标 + 值切片」表示（`map[rune]int32` 构建期 → CSR 数组成品；节点存于连续 `[]node`），不采用指针图 `map[rune]*node`（2026-08-29 评估后否决）：
- 前提澄清：`nodes[i]` 与指针解引用同为一次地址算术加一次加载，「下标多一次数组查找」不成立；真实差异在数据布局
- 空间：边目标 4B（int32）vs 8B（指针），`transVals` 减半；指针方案每节点是独立堆对象（分配器尺寸类对齐 + GC 位图元数据）且每节点一个 map 头（空表 ~48B 起），下标方案节点是切片元素零额外开销、表结构展平后消失
- 时间：转移路径上指针图 = map 哈希 + 探针 + 指针追逐（节点散落堆中，cache miss 密集）；下标方案 = CSR 段内连续查找（1-4 条边常单缓存行）+ 地址算术取节点（与当前节点邻接）
- GC：成品 Matcher 几乎 pointer-free，标记近常数；指针图每条边/每个节点/每个 bucket 均入扫描集，长驻服务的后台 GC 成本随边数线性增长
- 指针方案唯一实质优势是节点地址稳定（支持增量增删与外部持有引用）——本库构建后只读、一次性展平，不需要；int32 节点上限 2^31 对词库场景无约束
- 除非引入节点动态增删需求，不重启讨论

### Requirement: BM 坏字符跳跃（root 态安全跳过）
系统 SHALL 在自动机处于 root 态时应用坏字符跳跃以加速扫描，且保证不漏报：
- 构建期生成「词库首字符集」（所有关键词首个 rune 的集合）与 256 位字节过滤器（仅置位各关键词**首 rune 的 UTF-8 首字节**）：首字节集是安全判定的最小集——多位置位续字节只会让过滤器更频繁命中、触发无谓的 rune 解码，不改变可跳过的区域
- 安全性依据：处于 root 态（位置 p，无任何关键词前缀是 text[..p] 的后缀）时，任何匹配都不可能覆盖 p；而匹配的起始 rune 必为某关键词的首字符，故一段全部 rune 均不在词库首字符集中的区域 (p, n) 内不可能有任何匹配起始。因此可将文本指针直接跳到该区域之后第一个属于词库首字符集的 rune 处（位置 n），状态保持 root
- 实现上先按字节过滤器批量跳过（未命中过滤器的字节可直接按字节跳过，无需 rune 解码），命中过滤器的字节再做 rune 解码与词库首字符集判断；若该 rune 实际不在词库首字符集中（仅字节前缀撞上过滤器），前进一个 rune 宽度并保持 root
- 非法 UTF-8 字节视为不在词库首字符集中

#### Scenario: 跳过不漏报
- **WHEN** 词库为中文关键词、文本为中英混合长文本
- **THEN** 所有命中结果与朴素逐 rune 扫描（禁用跳跃的参照实现）完全一致

#### Scenario: ASCII 段快速跳过
- **WHEN** 词库仅含中文关键词，文本含大段 ASCII/标点
- **THEN** 这些区段被字节级快速跳过，结果正确

### Requirement: 非重叠最左最长结果语义（leftmost-longest）
系统 SHALL 采用非重叠**最左最长**语义收集命中（2026-08-29 依用户设计意图修订，取代原「先命中优先」贪心），在单遍扫描中经「待提交链」实现，T 指针不回退：
- 语义定义：从左到右，在起点不早于已提交命中结束位置的全部候选中，取**起点最小**者；同一起点取**最长**（该起点处完整出现的全部关键词中最长者）——关键词为真包含关系时一律输出最长匹配
- 候选仍按结束位置升序到达（同一结束位置按长度降序），经链规则归并（k 为弹出后基准，链前缀 chain[:k] 保留）：
  - 候选起点 < 链尾起点：候选更左，**弹出链尾**后继续向链左比较；链空后入链
  - 候选起点 == 基准起点：**取更长**（替换或维持），取代被弹出者
  - 候选起点 >= 基准结束位置（不重叠）：**入链**接续，取代被弹出者
  - 其余（与基准重叠且起点更晚）：候选**必被遮蔽**，链原样保留（弹出者恢复）——必死候选无权改变链。2026-08-31 fuzz 发现：若允许其弹出链尾，会出现「为必死候选让位而丢弃本可提交的命中」的空档（词库 {0,000} 文本 "000000000001" 在 [9,10) 处空档），破坏最左最长语义，且无状态 `FindNext` 以 End 推进永远无法复现该空档，导致两种迭代结果不一致
- **提交时机**：自动机回到 root（state==0）时提交整链，扫描结束时提交剩余——安全性：state==0 时不存在任何「起点不晚于当前位置而结束于其后」的候选（否则该候选的前缀仍是词库前缀，与 state==0 矛盾），链不会再被覆盖；链内起点天然升序
- 最终结果按 Start 升序；命中之间互不重叠，每个文本位置至多属于一个命中
- 词库去重后，同一关键词同一位置至多输出一次；无命中或 text 为空时返回 nil（长度为 0）
- 待提交链常规 ≤ 4 条走栈上内联数组（零分配）；仅「连续不重叠命中且自动机长期不回 root」的病态文本才可能溢出产生一次小分配

#### Scenario: 前缀关系取最长
- **WHEN** 词库 {"中国", "中国人"}，文本 "我是中国人"
- **THEN** 仅输出 "中国人"(Start=9, End=15)，不输出 "中国"

#### Scenario: 前缀未完整出现时取短词
- **WHEN** 词库 {"中", "中毒"}，文本 "中x"
- **THEN** 输出 "中"(Start=0, End=3)（更长前缀未在文本中完整出现）

#### Scenario: 重叠时起点更左者胜
- **WHEN** 词库 {"上海", "海口"}，文本 "上海口"
- **THEN** 仅输出 "上海"(Start=0, End=6)；与 "上海" 重叠且起点更晚的 "海口" 不输出

#### Scenario: 同结尾取更长（起始更早）
- **WHEN** 词库 {"他", "其他"}，文本 "其他"
- **THEN** 仅输出 "其他"(Start=0, End=6)（以同一位置结尾的命中中取最长，即起始更早且更长者）

#### Scenario: 不重叠命中按序全部输出
- **WHEN** 词库 {"上海", "北京"}，文本 "上海人北京"
- **THEN** 输出 "上海"(Start=0, End=6) 与 "北京"(Start=9, End=15)，按出现先后排序

#### Scenario: 真包含关系一律取最长
- **WHEN** 词库 {"国", "人", "中国人"}，文本 "中国人"
- **THEN** 仅输出 "中国人"(Start=0, End=9)："中国人" 起点更左，遮蔽链上 "国"、"人"；原「先命中优先」语义会输出 国、人，不符合设计意图，已废弃

#### Scenario: 断词后 fail 结算短词
- **WHEN** 词库 {"国", "人", "中国人"}，文本 "中国梦"
- **THEN** 输出 "国"(Start=3, End=6)："梦" 与 "人" 不匹配，"中国人" 断词，链中暂存的 "国" 在回到 root 时结算

#### Scenario: 更左候选逐级弹出链尾
- **WHEN** 词库 {"a", "ba", "cba"}，文本 "cba"
- **THEN** 仅输出 "cba"(Start=0, End=3)："a"(0,3 处 start=2) 先入链，"ba"(start=1) 弹出它，"cba"(start=0) 再弹出 "ba"——同一起点链条完全收敛于最左最长

#### Scenario: 链跨 root 前的不重叠接续
- **WHEN** 词库 {"上海", "北京"}，文本 "上海北京"
- **THEN** 输出 "上海"(0,6) 与 "北京"(6,12)（链内不重叠接续，扫完提交整链）

#### Scenario: 无空档（必死候选不弹链）
- **WHEN** 词库 {"0", "000"}，文本 "000000000001"（11 个 '0' + '1'）
- **THEN** 输出 000(0,3)、000(3,6)、000(6,9)、0(9,10)、0(10,11)：候选 0(8,11) 虽比链尾 0(9,10) 更左，但被已入链的 000(6,9) 遮蔽（必死），不得弹出 0(9,10)——否则 [9,10) 成为空档，`FindNext` 以 End 推进的迭代与 `FindAll` 不一致（2026-08-31 fuzz 发现并修复）

### Requirement: FindAllOverlapping 重叠全量返回
系统 SHALL 提供 `(*Matcher) FindAllOverlapping(text string) []Match`，返回 text 中全部关键词出现（含互相重叠者），服务词频统计、关键词提取、索引构建等统计类场景：
- 语义：每个（关键词，出现位置）对输出一次；同一关键词同一位置至多一次；**不做**非重叠筛选——互为包含/重叠的出现全部保留（`outLens` 本就携带以每个结束位置结尾的全部关键词，fail 链输出继承即全量信息）
- 输出顺序：按 **End 升序**、同一 End 按**关键词长度降序**（单遍扫描的天然产出序；注意与 `FindAll` 的 Start 升序不同序）
- 复用与开销：与 `FindAll` 共用自动机与 BM 跳跃（跳跃安全性只依赖词首字符判据，与输出模式无关）；时间 O(n + K)、空间 O(K)，K = 总出现数（**输出敏感**，病态后缀链词库下 K = O(n·m)，调用方自行评估输出规模）；查询期除结果切片外零分配
- 与默认方法的关系：`FindAll` 语义（非重叠最左最长）不变；重叠模式**不提供 FindNext 对应版**（理由见 Non-Goals）
- 无命中或 text 为空返回 nil

#### Scenario: 包含关系全量保留
- **WHEN** 词库 {"国", "人", "中国人"}，文本 "中国人"
- **THEN** 输出 3 条：国(3,6)、中国人(0,9)、人(6,9)——End 升序（6 < 9），同 End=9 长度降序（9 字节者先于 3 字节者）

#### Scenario: 重叠邻居均保留
- **WHEN** 词库 {"上海", "海口"}，文本 "上海口"
- **THEN** 输出 上海(0,6) 与 海口(3,9)（`FindAll` 只返回 上海；全量模式两者都在）

#### Scenario: 词频统计
- **WHEN** 对任意文本以 `len(FindAllOverlapping(text))` 或逐条计数统计各关键词出现次数
- **THEN** 与 `strings.Count` 逐词统计结果一致

### Requirement: FindNext 按需查找（超长文本友好）
系统 SHALL 提供 `(*Matcher) FindNext(text string, offset int) (Match, bool)`（comma-ok 形式；裸零值 `Match` 无法与「文本起点命中」区分，`*Match` nil 返回值语义含混，均被否决）：
- **无状态**：Matcher 不保存查询进度，可并发调用；调用方利用返回 Match 的 End 作为下次调用的 offset 实现「按需迭代」
- 从 text 的 offset（字节偏移）处开始扫描，返回**第一个**满足最左最长语义的命中；找到即终止扫描，不遍历剩余文本（对超长文本达到目的即停）
- 无命中时返回 `(Match{}, false)`；命中返回 `(Match, true)`
- 边界处理：offset < 0 按 0 处理；offset >= len(text) 返回 `(Match{}, false)`；offset 落在 UTF-8 字符中间时，向后对齐到下一个 rune 边界再开始扫描
- 扫描语义（最左最长）与 FindAll 完全一致：首条命中即 FindAll 的第一条；对同一 text，从 offset=0 反复 FindNext 并以 End 推进得到的序列与 FindAll 完全一致
- offset 处自动机从 root 开始（匹配不会跨越 offset 边界向左延伸）

#### Scenario: 首个命中
- **WHEN** 词库 {"中国", "北京"}，文本 "xx中国yy北京"，`FindNext(text, 0)`
- **THEN** 返回 "中国"(Start=2, End=8)，且不会扫描到 "北京" 之后

#### Scenario: 按需迭代
- **WHEN** 同上，随后调用 `FindNext(text, 8)`
- **THEN** 返回 "北京"(Start=10, End=16)

#### Scenario: 无命中返回 false
- **WHEN** 词库 {"中国"}，文本 "abc"，任意合法 offset
- **THEN** 返回 `(Match{}, false)`

#### Scenario: offset 对齐 rune 边界
- **WHEN** offset 落在某个多字节字符中间
- **THEN** 不 panic，从下一个完整 rune 开始扫描

#### Scenario: 与 FindAll 一致性
- **WHEN** 对任意词库与文本，循环 `FindNext(text, off)` 并以 `End` 推进直到零值
- **THEN** 得到的命中序列与 `FindAll(text)` 完全一致

### Requirement: 并发安全
`Matcher` 构建完成后 SHALL 为只读数据结构（无查询期可变状态），允许无锁并发调用 `FindAll` 与 `FindNext`；`-race` 下并发测试通过。

#### Scenario: 并发查询
- **WHEN** 多个 goroutine 同时对同一 Matcher 调用 FindAll 与 FindNext
- **THEN** 各自得到正确结果，`go test -race` 无告警

### Requirement: 质量与工程化
- 代码通过 `gofmt`、`go vet`，全部测试（含 `-race`）通过
- 提供 `example_test.go` 可运行示例（含 FindAll 与 FindNext 按需迭代）与 `bench_test.go` 基准（中文长文本 × 中文词库、中英混合文本两种场景，并对照禁用跳跃的参照实现）
- **Go 原生 fuzz 测试**（`fuzz_test.go`，2026-08-31 引入）：对（任意字节文本 × 关键词组合）验证——
  - 绝不 panic（含非法 UTF-8 文本、词库拒绝路径）
  - `New` 契约：非法 UTF-8 / 含 U+FFFD 的关键词必须拒绝（错误类别与首个非法关键词对应）
  - `FindAll`：区间合法、切片恒等、起点 rune 边界、Start 升序、相邻不重叠
  - `FindAllOverlapping`：总数与逐词 `strings.Index` 枚举一致，End 升序、同 End 长度降序，且蕴含 `FindAll` 的每条命中
  - `FindNext`：以 End 迭代与 `FindAll` 完全一致；任意逐字节 offset（含 rune 中间与非法字节）的结果与「后缀 FindAll 首条（坐标平移）」一致
  - 种子语料经 `f.Add` 随源码提交；引擎发现的崩溃样本回归于 `testdata/fuzz/`（普通 `go test` 自动重放）
