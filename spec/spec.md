# ratchetmatch：中文优化 ACBM 多模式匹配库 Spec

## 概述

给定词库（多个关键词），对每条输入的长文本单遍扫描，返回全部命中关键词及位置，采用非重叠最左最长语义。现有通用实现未针对中文 rune 特点优化，且缺少明确的命中优先级语义，本库补此空白。Go 1.27、零第三方依赖。

公共 API：`New` / `FindAll` / `FindAllOverlapping` / `FindNext`（签名与契约见各 Requirement）。**Matcher 为导出接口**（经未导出方法密封，仅本包提供实现），匹配模式由 `New` 的选项一次性定型：默认精确匹配（exactMatcher），`New(kws, WithCaseFold())` 为 SimpleFold 轨道折叠匹配（foldMatcher）；两套实现独立，需同时使用时分别 `New`（2026-09-03 定型，取代早先的「struct 外壳 + 查询期选项/惰性构建」方案）。**本 spec 为 API 契约与匹配语义的权威描述，修改行为须先改本文件。**

## Non-Goals

- 全半角归一化、正则、编辑距离匹配（大小写折叠已支持，见 caseFold Requirement；多 rune 展开式全折叠如 ß→ss 不支持，fold 语义为逐 rune SimpleFold 轨道等价）
- 流式（分块）输入接口；关键词动态增删（构建后不可变）
- 重叠模式的按需查找（`FindNextOverlapping`）：重叠语义与「从 offset 重扫的无状态迭代」不兼容——返回最长的出现后以 End 推进必然漏掉其内部更早起点的出现（如 国[3,6)）
- 基于 rune 下标的位置/长度 API（决策依据见「偏移计量单位」需求）

## Requirements

### Requirement: caseFold 匹配（WithCaseFold 构建选项）

`New` 接受变参 `opts ...Option`，`WithCaseFold() Option` 构建按 Unicode SimpleFold 轨道折叠语义匹配的自动机（`strings.EqualFold` 等价：文本 rune 与关键词 rune fold 相等即匹配）。变参为空 = 精确匹配。**模式构建期定型且不可更改**；Matcher 接口提供 `CaseFold() bool` 供调用方判别（2026-09-03 定型，用户决策：不再提供查询期选项与惰性构建——折叠改变自动机结构而非可比对属性，查询期切换在语义上不健全，且主流引擎均以 caseless 为构建期属性）：

- **类型即模式**：导出的 `Matcher` 为密封接口（未导出方法 `isInternal` 保证仅本包实现），`exactMatcher` / `foldMatcher` 两套私有类型（各嵌入共用泛型引擎 `machine[N]`）分别实现之——无运行时模式分支，无「字段二选一」的中间状态；需同一词库的两种模式时分别 `New` 两个实例（与 aho-corasick / Hyperscan / RE2 的 caseless 构建模式一致，业界均不支持查询期切换）
- **折叠自动机构建期生成**：fold-equal 的边在插入时合一（共享目标节点，outRunes 为折叠等价关键词的并集）——查询期在同一精确自动机上换 EqualFold 比较不可行（折叠冲突的兄弟分支只走其一，必漏报变体词典）
- **首字符筛选 = 轨道展开**：rootNext/byteFilter/CSR 边 key 均展开为各 rune 的全部 SimpleFold 轨道成员（轨道互不相交、每轨道成员数 ≤4，词库规模膨胀为常数倍）——CSR 段内仍严格升序（二分/线性查找零改动），skipForward 判据不变，查询热路径与精确模式完全共用、零分支零归一
- 匹配语义（FindAll / FindAllOverlapping / FindNext）与精确模式逐条对应：非重叠最左最长、End 升序全量、首命中即停，均以折叠等价替换精确相等；**fold Matcher 的输出 ≡ 先把词库全部折叠归一去重、再按同等最左最长语义对折叠后文本扫描的结果**（等价 oracle 见测试）
- **命中区间按文本侧实际消耗提取**：fold 轨道成员 UTF-8 宽度可不同（如 K U+212A 3 字节 vs k 1 字节），字节长不可直接回退起点；fold 自动机输出存关键词 rune 数，命中时按 rune 数从 End 向前走 rune 边界提取 Start，`Match.Keyword = text[Start:End]`（文本原样切片，非关键词原件——同一关键词可折叠匹配出多种大小写形态）
- fold 匹配按逐 rune SimpleFold 比较：文本非法字节按 RuneError 处理（不在任何折叠轨道，等同精确模式）；SimpleFold 不做多 rune 展开（ß→ss）——关键词含 ß 时只匹配含 ß 的文本
- fold 模式下 BM 跳跃安全性：折叠自动机首字符集已含全部轨道成员，root 态跳跃判据（起始 rune 必在首字符集）不变

#### Scenario: fold 查询
- **WHEN** `m, _ := New([]string{"hello","世界"}); m.FindAll("Hello, WORLD! 世界")`（精确）与 `fm, _ := New([]string{"hello","世界"}, WithCaseFold()); fm.FindAll("Hello, WORLD! 世界")`（折叠）对照 **THEN** 折叠命中 `Hello`(0,5) 与 `世界`(14,20)，精确仅命中 `世界`
- **WHEN** `m, _ := New([]string{"hello","世界"}, WithCaseFold()); m.FindAll("Hello, WORLD! 世界")`（折叠 Matcher，不传任何查询选项）**THEN** 结果与「词库折叠归一去重后对折叠文本精确扫描」的 oracle 完全一致
- **WHEN** 词库 {"Stop","stop"}，文本 "SToP sTop"（fold 查询）**THEN** 两处均命中（精确查询漏报其一——同节点不可能走两条分支，构建期合一即修复）
- **WHEN** 词库 {"K"}（U+212A 开尔文度），文本 "k K"（fold 查询）**THEN** 命中 k(0,1) 与 K(2,5)：区间按文本侧宽度提取，Keyword 为文本切片
- **WHEN** 并发 8 goroutine 分别对精确 Matcher 与折叠 Matcher 查询 **THEN** `-race` 通过、结果稳定

### Requirement: 词库构建与校验

系统 SHALL 接受非空关键词列表，构建不可变 `Matcher`：

- 词库为空（nil 或长度 0）或含空字符串时 `New` 返回错误；重复关键词自动去重
- **关键词不得含非法 UTF-8 字节，亦不得含规范编码的 U+FFFD（字节 `EF BF BD`）**，`New` 返回错误且信息可区分原因（分别含 "not valid UTF-8" 与 "U+FFFD" 字样）。理由（2026-08-31 fuzz 发现）：查询端把非法字节按 RuneError 处理且仅前进 1 字节，与 U+FFFD 的 3 字节规范编码存在长度歧义；且非法字节在 rune 层坍缩为同一 RuneError 存在身份歧义（`"\xb8"` 与 `"\xff"` 同路径，极端情形回推出负偏移 panic）。文本侧非法字节不受影响，仍按 RuneError 逐字节处理
- Trie 按 **rune**（Unicode 码点）构建，中文按整字符处理，绝不按 UTF-8 字节碎片转移；末节点带终止标记，必须完整匹配才命中

#### Scenario: 中文词库构建成功
- **WHEN** `New([]string{"你好", "世界", "你好世界"})` **THEN** 构建成功，可命中全部三个关键词

#### Scenario: 非法词库报错
- **WHEN** `New(nil)`、`New([]string{})`、`New([]string{"a", ""})` 或含非法 UTF-8 / U+FFFD 的关键词
- **THEN** 返回非 nil error，且信息可区分原因

### Requirement: 失败指针与转移表内存布局（设计决策，勿重复讨论）

- 构建期 BFS 计算失败指针：指向「已匹配部分的最长真后缀（同时是某关键词前缀）」的节点，无则指向 root
- **稀疏转移 + 查询期失败指针回退，不做 DFA 全量转移表**：每节点仅存自有 trie 边，展平进全局 CSR 有序数组（`keys []rune` 升序 / `vals []int32` 平行），节点只存 `base/count/fail`。DFA 全量解析会使非叶节点表膨胀至 root 扇出（中文词库 ~百键），实测构建内存反升、扫描回退 22–48%，已废弃
- 查询期转移：段内查找（段宽 ≤16 线性扫描，>16 二分），未命中沿 `fail` 回退重试直到 root；回退链平均 1–2 步，摊还 O(1)
- root 态转移与跳跃判断共用一张 `map[rune]int32`（词库**首字符集**）：任何匹配的起始 rune 必为词首字符，以其做跳跃判据等价安全且比全字符集更精确
- 构建期失败指针计算用带回退的查找（不依赖全量表）；outLens 语义不变

#### Scenario: 失败指针正确性
- **WHEN** 词库 {"上海", "海口"}，文本 "上海口"
- **THEN** 匹配完 "上海" 遇 '口' 无法下行时经失败转移继续；最终按最左最长输出 "上海"

### Requirement: 单遍扫描与单调前进

系统 SHALL 对文本按 rune 单遍扫描：

- 每 rune 一次转移（可下行则下行，否则沿失败指针回退重试）；文本指针单调向前绝不回退，只有自动机状态在移动
- 到达终止状态时，取以当前文本位置**结尾**的最长关键词作为候选
- 非法 UTF-8 字节按 `utf8.RuneError` 处理并前进 1 字节，不 panic、不漏扫后续内容
- 复杂度 O(|T| × 每 rune 摊还 O(1) 转移 + 命中处理)

### Requirement: 偏移计量单位（设计决策，勿重复讨论）

系统 SHALL 维持「rune 转移 + 字节计量」（2026-08-29 评估后否决 rune 下标 API）：

- 自动机按 rune 转移；仅位置（`Match.Start/End`、`FindNext.offset`）与长度（outLens）按字节计量，保证 `text[Start:End] == Keyword` 可直接切片（Go 惯用法）
- `[]rune` 预转换的代价：瞬时内存纯 ASCII +300%、中文 +33%；字节级 BM 跳跃失效（~1.4x 收益归零）；FindNext 首停被全量解码破坏（~10x）；查询期多一次大分配
- 并行维护 runeIdx 无净收益：字节位置因 API 契约仍须保留，双份簿记；可消除的仅 FindNext 入口对齐检查（O(1) 极小常数）
- 改 rune 下标属 **BREAKING**，除非出现明确新需求不重启讨论

### Requirement: 节点表示（设计决策，勿重复讨论）

系统 SHALL 采用「下标 + 值切片」表示（构建期 `map[rune]int32` → 成品 CSR 数组，节点存于连续 `[]node`），不采用指针图 `map[rune]*node`（2026-08-29 评估后否决）：

- 空间：边目标 4B vs 8B；指针方案每节点为独立堆对象（分配器尺寸类 + GC 位图）且各持一个 map 头（空表 ~48B 起），下标方案节点为切片元素零额外开销
- 时间：下标方案 = CSR 段内连续查找（1–4 条边常单缓存行）+ 地址算术取节点（与当前节点邻接）；指针图 = map 哈希 + 探针 + 指针追逐（节点散落堆中，cache miss 密集）。「下标多一次数组查找」的说法不成立，二者同为一次地址算术加一次加载
- GC：成品近乎 pointer-free，标记近常数；指针图每边/每节点/每 bucket 入扫描集，长驻服务 GC 成本随边数线性增长
- 指针方案唯一实质优势是节点地址稳定（支持增量增删与外部持有引用）——本库构建后只读、一次性展平，不需要；int32 节点上限 2^31 对词库场景无约束

### Requirement: BM 坏字符跳跃（root 态安全跳过）

系统 SHALL 在自动机处于 root 态时应用坏字符跳跃且保证不漏报：

- 构建期生成「词库首字符集」与 256 位字节过滤器（仅置位各关键词**首 rune 的 UTF-8 首字节**——首字节集是安全判定的最小集，多位置位续字节只会徒增 rune 解码）
- 安全性：root 态（位置 p，无任何关键词前缀是 text[..p] 的后缀）时任何匹配都不可能覆盖 p；而匹配的起始 rune 必为词首字符，故一段全部 rune 均不在首字符集的区域 (p, n) 内不可能有匹配起始，可直接跳到 n，状态保持 root
- 实现：先按字节过滤器批量跳过（未命中者无需 rune 解码），命中者再 rune 解码并判首字符集；仅字节前缀撞过滤器时前进一个 rune 宽度保持 root；非法 UTF-8 视为不在首字符集

#### Scenario: 跳过不漏报
- **WHEN** 中文词库 × 中英混合长文本（或含大段 ASCII/标点）
- **THEN** 全部命中与禁用跳跃的朴素参照实现完全一致；ASCII 段被字节级跳过

### Requirement: 非重叠最左最长结果语义（leftmost-longest）

系统 SHALL 采用非重叠**最左最长**语义（2026-08-29 修订，取代原「先命中优先」贪心），单遍扫描经「待提交链」实现，文本指针不回退：

- 语义定义：从左到右，在起点不早于已提交命中结束位置的全部候选中取**起点最小**者；同一起点取完整出现的**最长**关键词——真包含关系一律输出最长
- 候选按结束位置升序到达（同结束位置按长度降序），经链规则归并（k 为弹出后基准，chain[:k] 保留）：
  - 候选起点 < 链尾起点：更左，弹出链尾继续向左比较；链空入链
  - 候选起点 == 基准起点：取更长（替换）
  - 候选起点 >= 基准结束位置：不重叠，入链接续
  - 其余（与基准重叠且起点更晚）：候选必被遮蔽，**链原样保留**（弹出可恢复）——必死候选无权改变链。2026-08-31 fuzz 发现：若允许其弹出链尾，会「为必死候选让位而丢弃本可提交的命中」制造空档，且无状态 `FindNext`（以 End 推进重扫）永远无法复现该空档，两种迭代结果不一致
- 提交时机：自动机回到 root（state==0）时提交整链，扫描结束提交剩余。安全性：state==0 时不存在任何「起点不晚于当前位置而结束于其后」的候选（否则其前缀仍是词库前缀，与 state==0 矛盾），链不会再被覆盖；链内起点天然升序
- 结果按 Start 升序；命中互不重叠、不留空档，每个文本位置至多属于一个命中；同一关键词同一位置至多输出一次；无命中或 text 为空返回 nil
- 待提交链常规 ≤ 4 条走栈上内联数组（零分配）；仅病态文本（连续命中且自动机长期不回 root）才可能溢出产生一次小分配

#### Scenario: 语义用例

| 场景 | 词库 / 文本 | 输出 |
|---|---|---|
| 前缀关系取最长 | `{"中国", "中国人"}` / `"我是中国人"` | 仅 `中国人`(9,15) |
| 前缀未完整出现取短词 | `{"中", "中毒"}` / `"中x"` | 仅 `中`(0,3) |
| 重叠时起点更左者胜 | `{"上海", "海口"}` / `"上海口"` | 仅 `上海`(0,6) |
| 同结尾取更长 | `{"他", "其他"}` / `"其他"` | 仅 `其他`(0,6) |
| 不重叠按序全部输出 | `{"上海", "北京"}` / `"上海北京"` | `上海`(0,6)、`北京`(6,12) |
| 真包含关系一律取最长 | `{"国", "人", "中国人"}` / `"中国人"` | 仅 `中国人`(0,9)；原「先命中优先」会输出 国、人，已废弃 |
| 断词后 fail 结算短词 | `{"国", "人", "中国人"}` / `"中国梦"` | 仅 `国`(3,6)（链中暂存、回 root 结算） |
| 更左候选逐级弹出链尾 | `{"a", "ba", "cba"}` / `"cba"` | 仅 `cba`(0,3) |
| 无空档（必死候选不弹链） | `{"0", "000"}` / `"000000000001"` | `000`×3、`0`×2；候选 0(8,11) 虽更左但被 000(6,9) 遮蔽，不得弹出 0(9,10)，否则 [9,10) 成空档（2026-08-31 fuzz 发现并修复） |

### Requirement: FindAllOverlapping 重叠全量返回

系统 SHALL 提供 `FindAllOverlapping(text string) []Match`，返回全部关键词出现（含互相重叠者），服务词频统计、索引构建：

- 每个（关键词，出现位置）对输出一次；不做非重叠筛选（outLens 本就携带以每个结束位置结尾的全部关键词，fail 链输出继承即全量信息）
- 输出按 **End 升序**、同一 End 按**长度降序**（单遍扫描的天然产出序，注意与 `FindAll` 的 Start 升序不同序）
- 与 `FindAll` 共用自动机与 BM 跳跃（跳跃安全性只依赖词首字符判据，与输出模式无关）；时间 O(n+K)、空间 O(K)，K 为总出现数（**输出敏感**，病态词库下 K = O(n·m)，调用方自行评估）；查询期除结果切片外零分配
- 不提供 FindNext 对应版（理由见 Non-Goals）；无命中或 text 为空返回 nil

#### Scenario: 全量保留与输出序
- **WHEN** 词库 `{"国", "人", "中国人"}`，文本 `"中国人"`：输出 国(3,6)、中国人(0,9)、人(6,9)——End 升序，同 End=9 长度降序
- **WHEN** 词库 `{"上海", "海口"}`，文本 `"上海口"`：输出 上海(0,6) 与 海口(3,9)（`FindAll` 只返回 上海）
- **WHEN** 逐词与 `strings.Count` / `strings.Index` 枚举对照 **THEN** 计数一致

### Requirement: FindNext 按需查找（超长文本友好）

系统 SHALL 提供 `FindNext(text string, offset int) (Match, bool)`（comma-ok 形式；裸零值 Match 无法与「文本起点命中」区分，`*Match` nil 语义含混，均被否决）：

- **无状态**：Matcher 不保存查询进度，可并发调用；调用方以返回 Match 的 End 作为下次 offset 实现按需迭代
- 从 offset（字节偏移）开始扫描，返回**第一个**满足最左最长语义的命中，找到即终止扫描；自动机从 root 开始（匹配不跨越 offset 向左延伸）
- 边界：offset < 0 按 0；offset >= len(text) 或无命中返回 `(Match{}, false)`；offset 落在多字节字符中间时向后对齐 rune 边界再扫描
- 一致性：首条命中即 `FindAll` 的第一条；从 offset=0 反复 FindNext 并以 End 推进得到的序列与 `FindAll` 完全一致

#### Scenario: 首命中即停与迭代
- **WHEN** 词库 {"中国", "北京"}，文本 "xx中国yy北京"：`FindNext(text, 0)` 返回 中国(2,8) 且不扫描其后；`FindNext(text, 8)` 返回 北京(10,16)
- **WHEN** 词库 {"中国"}，文本 "abc"，任意合法 offset **THEN** 返回 `(Match{}, false)`
- **WHEN** 对任意词库与文本以 End 推进迭代到底 **THEN** 序列与 `FindAll` 完全一致（500 组随机 + fuzz 覆盖）

### Requirement: 并发安全

`Matcher` 实现构建完成后 SHALL 为只读（无查询期可变状态），`FindAll` / `FindNext` 可无锁并发调用；`go test -race` 下并发测试通过（精确与折叠两套实现分别覆盖）。

### Requirement: 质量与工程化

- `gofmt`、`go vet`、`golangci-lint` 通过；`go test ./...` 与 `-race` 全部通过
- `example_test.go` 可运行示例（FindAll 与 FindNext 按需迭代）；`bench_test.go` 基准 = 三组参照基线 × 两套各 100 词的词库（稀疏：互不重叠/包含；重叠：大量前缀链、包含/子串与单字词）× 中文/混合长文本 × FindNext 首停：纯 Trie 重启（trieFindAll，去 fail 链与跳跃）、纯 Boyer-Moore 逐词坏字符（bmFindAll，坏字符表 init 期预构建）、逐关键词 `strings.Index`（stringsIndexFindAll/FindNext，标准库 SIMD）——参照与正式 API 在基准语料（两套词库 × 两份文本）下等价由 TestBaselineEquiv 守卫，防止对比失真
- **Go 原生 fuzz**（`fuzz_test.go`）对任意字节文本 × 关键词组合验证：绝不 panic；`New` 错误契约；`FindAll` 区间合法/切片恒等/Start 升序/相邻不重叠；`FindAllOverlapping` 与逐词枚举一致且蕴含 `FindAll`；`FindNext` 以 End 迭代 == `FindAll`、任意逐字节 offset == 后缀 FindAll 首条平移。种子语料随源码提交，崩溃样本回归 `testdata/fuzz/`
