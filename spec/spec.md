# ratchetsearch：中文优化 ACBM 多模式匹配库 Spec

## Why
全新 Go 1.27 组件仓库需要实现一个针对中文优化的 ACBM（Aho-Corasick + Boyer-Moore）字符串匹配库：给定词库 V（多个关键词 W），对每条输入的长文本 T 单遍扫描，返回 T 中命中的关键词位置，采用非重叠贪心语义（先命中优先、前缀关系取最长）。现有通用实现未针对中文 rune 特点优化，且缺少明确的命中优先级语义。

## What Changes
- 新建 Go module（`go.mod` 声明 `go 1.27`，module 名 `ratchetsearch`）
- 核心公共 API：
  - `New(keywords []string) (*Matcher, error)`：构建不可变 Matcher
  - `(*Matcher) FindAll(text string) []Match`：返回全部命中（按 Start 升序）
  - `(*Matcher) FindNext(text string, offset int) (Match, bool)`：无状态按需查找，从 offset（字节偏移）开始查找第一个命中，找到即终止扫描；comma-ok 形式返回，无命中时返回零值 Match 与 false
- 构建期：rune 级 Trie（支持中文多字节字符）+ 失败指针（BFS 构建），节点持稀疏转移表与失败指针（查询期段内查找 + 沿失败链回退，摊还 O(1)，表不膨胀）
- BM 风格跳跃（坏字符规则）：自动机处于 root 态时，利用「词库首字符集 + 256 位字节过滤器」直接跳过不可能出现匹配起始的文本段
- 匹配语义（非重叠贪心）：先命中优先；同一起始位置前缀关系取最长；命中后跳过被覆盖区间，每个文本位置至多属于一个命中；结果按出现先后（Start 升序）排序
- 单元测试（含中文、贪心语义、跳跃不漏报的随机对照测试、`-race` 并发测试）、使用示例、Benchmark

## Impact
- Affected specs: 无（本仓库首个 spec）
- Affected code: 全新仓库；新增 `go.mod`、`ratchetsearch.go`（API）、`build.go`（Trie/失败指针/自动机/跳跃表）、`search.go`（扫描）、`ratchetsearch_test.go`、`example_test.go`、`bench_test.go`

## Non-Goals
- 大小写折叠、全半角归一化、正则、编辑距离匹配
- 流式（分块）输入接口；关键词的动态增删（构建后不可变）
- 重叠命中的全量返回模式（仅实现非重叠贪心）

## ADDED Requirements

### Requirement: 词库构建与校验
系统 SHALL 接受一个非空关键词列表，构建不可变的 `Matcher`：
- 词库为空（nil 或长度为 0）或包含空字符串 `""` 时，`New` 返回错误
- 重复关键词自动去重（只保留一份）
- Trie 按 **rune**（Unicode 码点）构建，中文按整字符处理，绝不按 UTF-8 字节碎片转移
- 每个关键词的末节点带终止标记，表示该关键词的完整结束（必须完整匹配才命中）

#### Scenario: 中文词库构建成功
- **WHEN** `New([]string{"你好", "世界", "你好世界"})`
- **THEN** 构建成功无错误，后续可命中全部三个关键词

#### Scenario: 非法词库报错
- **WHEN** `New(nil)`、`New([]string{})` 或 `New([]string{"a", ""})`
- **THEN** 返回非 nil error，且 error 信息可区分原因

### Requirement: 失败指针（回退指针）
系统 SHALL 在构建期通过 BFS 为每个 Trie 节点计算失败指针，语义为：
- 指向「当前已匹配部分的最长真后缀（该后缀同时是词库中某关键词的前缀）」对应的 Trie 节点
- 若不存在这样的后缀，则指向 root（表示需从 root 重新开始）
- 构建完成后，节点持有稀疏转移表（仅自有 trie 边）与失败指针：转移为段内查找 + 至多沿失败链回退重试（回退链平均 1–2 步，摊还 O(1)），无构建期 DFA 全量解析的表膨胀

#### Scenario: 失败指针正确性
- **WHEN** 词库含 {"上海", "海口"}，文本为 "上海口"
- **THEN** 匹配完 "上海" 后遇到 '口' 无法下行时，失败转移仍使扫描继续；最终按贪心语义输出 "上海"

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
- 当前 rune 在自动机中完成转移（可下行则下行，否则走已解析的失败转移），单次转移为一次有序数组二分
- T 上的指针单调向前，每消费一个 rune 前进一次，绝不回退；只有自动机状态在移动
- 到达终止状态时，取以当前文本位置**结尾**的最长关键词作为候选
- 文本中出现非法 UTF-8 字节时按 `utf8.RuneError` 处理并前进 1 字节，不 panic、不漏扫后续内容

#### Scenario: 长文本单遍完成
- **WHEN** 输入任意长中文文本
- **THEN** 扫描严格一遍结束，时间复杂度为 O(|T|/rune × 每 rune 摊还 O(1) 转移 + 命中处理)

### Requirement: BM 坏字符跳跃（root 态安全跳过）
系统 SHALL 在自动机处于 root 态时应用坏字符跳跃以加速扫描，且保证不漏报：
- 构建期生成「词库首字符集」（所有关键词首个 rune 的集合）与 256 位字节过滤器（词库首字符 UTF-8 编码中出现的所有字节集合）
- 安全性依据：处于 root 态（位置 p，无任何关键词前缀是 text[..p] 的后缀）时，任何匹配都不可能覆盖 p；而匹配的起始 rune 必为某关键词的首字符，故一段全部 rune 均不在词库首字符集中的区域 (p, n) 内不可能有任何匹配起始。因此可将文本指针直接跳到该区域之后第一个属于词库首字符集的 rune 处（位置 n），状态保持 root
- 实现上先按字节过滤器批量跳过（未命中过滤器的字节可直接按字节跳过，无需 rune 解码），命中过滤器的字节再做 rune 解码与词库首字符集判断；若该 rune 实际不在词库首字符集中（仅字节前缀撞上过滤器），前进一个 rune 宽度并保持 root
- 非法 UTF-8 字节视为不在词库首字符集中

#### Scenario: 跳过不漏报
- **WHEN** 词库为中文关键词、文本为中英混合长文本
- **THEN** 所有命中结果与朴素逐 rune 扫描（禁用跳跃的参照实现）完全一致

#### Scenario: ASCII 段快速跳过
- **WHEN** 词库仅含中文关键词，文本含大段 ASCII/标点
- **THEN** 这些区段被字节级快速跳过，结果正确

### Requirement: 非重叠贪心结果语义
系统 SHALL 采用非重叠贪心语义收集命中，通过「暂存候选（pending）」机制在单遍扫描中实现，T 指针不回退：
- 候选按结束位置升序到达；同一结束位置的全部候选（以该位置结尾的所有关键词，含经失败指针可达者）按长度降序排列
- 对每个结束位置，从最长候选开始，选第一个与 pending 兼容的候选；兼容规则：
  - 候选起始位置 == pending 起始位置（前缀关系）：**替换** pending（取更长，如先命中 "中国" 再完整命中 "中国人" 时，最终取 "中国人"）
  - 候选起始位置 >= pending 结束位置（不重叠）：**提交** pending（输出），新候选成为新的 pending
  - 其余（与 pending 重叠）：跳过该候选，继续尝试更短者（保证长词被先命中者遮蔽时，其后接续的短词仍可命中）
- 扫描结束时若仍有 pending，提交之
- 最终结果按出现先后（Start 升序）排序；命中之间互不重叠，每个文本位置至多属于一个命中
- 词库去重后，同一关键词同一位置至多输出一次；无命中或 text 为空时返回 nil（长度为 0）

#### Scenario: 前缀关系取最长
- **WHEN** 词库 {"中国", "中国人"}，文本 "我是中国人"
- **THEN** 仅输出 "中国人"(Start=9, End=15)，不输出 "中国"

#### Scenario: 前缀未完整出现时取短词
- **WHEN** 词库 {"中", "中毒"}，文本 "中x"
- **THEN** 输出 "中"(Start=0, End=3)（更长前缀未在文本中完整出现）

#### Scenario: 重叠命中取先命中者
- **WHEN** 词库 {"上海", "海口"}，文本 "上海口"
- **THEN** 仅输出 "上海"(Start=0, End=6)；与 "上海" 重叠的 "海口" 不输出

#### Scenario: 同结尾取更长（起始更早）
- **WHEN** 词库 {"他", "其他"}，文本 "其他"
- **THEN** 仅输出 "其他"(Start=0, End=6)（以同一位置结尾的命中中取最长，即先命中且更长者）

#### Scenario: 不重叠命中按序全部输出
- **WHEN** 词库 {"上海", "北京"}，文本 "上海人北京"
- **THEN** 输出 "上海"(Start=0, End=6) 与 "北京"(Start=9, End=15)，按出现先后排序

#### Scenario: 长词遮蔽下接续命中
- **WHEN** 词库 {"国", "人", "中国人"}，文本 "中国人"
- **THEN** 输出 "国"(Start=3, End=6) 与 "人"(Start=6, End=9)："国" 先命中提交，"中国人" 与之重叠被跳过，同位置结束的更短候选 "人" 起始不早于已提交命中的结束位置、接续命中

### Requirement: FindNext 按需查找（超长文本友好）
系统 SHALL 提供 `(*Matcher) FindNext(text string, offset int) Match`：
- **无状态**：Matcher 不保存查询进度，可并发调用；调用方利用返回 Match 的 End 作为下次调用的 offset 实现「按需迭代」
- 从 text 的 offset（字节偏移）处开始扫描，返回**第一个**满足贪心语义的命中；找到即终止扫描，不遍历剩余文本（对超长文本达到目的即停）
- 无命中时返回零值 `Match{}`（可用 `Start == End == 0` 且 Keyword 为空判断）
- 边界处理：offset < 0 按 0 处理；offset >= len(text) 返回零值；offset 落在 UTF-8 字符中间时，向后对齐到下一个 rune 边界再开始扫描
- 扫描的贪心语义（前缀取最长、重叠取先命中者）与 FindAll 完全一致；对同一 text，FindAll 的结果等价于从 offset=0 开始反复 FindNext 并以 End 推进
- offset 处自动机从 root 开始（匹配不会跨越 offset 边界向左延伸）

#### Scenario: 首个命中
- **WHEN** 词库 {"中国", "北京"}，文本 "xx中国yy北京"，`FindNext(text, 0)`
- **THEN** 返回 "中国"(Start=2, End=8)，且不会扫描到 "北京" 之后

#### Scenario: 按需迭代
- **WHEN** 同上，随后调用 `FindNext(text, 8)`
- **THEN** 返回 "北京"(Start=11, End=17)

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
