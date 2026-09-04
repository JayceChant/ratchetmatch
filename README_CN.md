# ratchetmatch

[![CI](https://github.com/JayceChant/ratchetmatch/actions/workflows/ci.yml/badge.svg)](https://github.com/JayceChant/ratchetmatch/actions/workflows/ci.yml)
[![CodeQL](https://github.com/JayceChant/ratchetmatch/actions/workflows/codeql.yml/badge.svg)](https://github.com/JayceChant/ratchetmatch/actions/workflows/codeql.yml)
[![govulncheck](https://github.com/JayceChant/ratchetmatch/actions/workflows/vulncheck.yml/badge.svg)](https://github.com/JayceChant/ratchetmatch/actions/workflows/vulncheck.yml)
[![codecov](https://codecov.io/gh/JayceChant/ratchetmatch/graph/badge.svg)](https://codecov.io/gh/JayceChant/ratchetmatch)
[![SonarCloud](https://sonarcloud.io/api/project_badges/measure?project=JayceChant_ratchetmatch&metric=alert_status)](https://sonarcloud.io/summary/new_code?id=JayceChant_ratchetmatch)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/JayceChant/ratchetmatch/badge)](https://scorecard.dev/viewer/?uri=github.com/JayceChant/ratchetmatch)
[![go.dev reference](https://pkg.go.dev/badge/github.com/JayceChant/ratchetmatch.svg)](https://pkg.go.dev/github.com/JayceChant/ratchetmatch)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

[English](README.md) | **简体中文**

针对中文优化的 ACBM（Aho-Corasick + Boyer-Moore）多模式匹配库：一次构建词库自动机，对每条长文本单遍扫描，返回全部关键词命中。零第三方依赖，仅使用 Go 标准库。

- **同义词分组**：`WithSynonyms` 声明同义词组，命中自带组号（`Match.Group`）——同义概念天然归并，无需词后查表归拢；分组不改变匹配语义，可与大小写折叠组合。
- **大小写折叠**：`WithCaseFold` 获得 `strings.EqualFold` 语义（逐 rune SimpleFold 轨道），大小写变体合一不漏报，查询开销可忽略。
- **三种查询形态**：`FindAll` 非重叠最左最长（高亮/过滤）、`FindAllOverlapping` 全量出现（词频/索引）、`FindNext` 首停（超长文本按需迭代，约 10x）。
- **为扫描而生**：稀疏转移表 + 失败指针摊还 O(1)，root 态坏字符跳跃（中英混合 ~1.4x）；构建后只读、查询零分配，可无锁并发常驻数月。

## 适用范围

**适合**：词库固定、目标文本多变——一次 `New` 构建后，对大量长文本反复查询（内容过滤、审计、风控等流式扫描场景）。

**不适合**：目标文本固定、关键词多变——此时更应对目标文本做一次性预处理（如关键词倒排索引），按关键词查询而非按文本扫描。

## 性能

```bash
go test -bench . -run '^$'
```

中英混合文本下坏字符跳跃约 1.4x 加速；`FindNext` 首停对长文本约 10x。中文纯文本词密场景跳跃空间有限，收益趋于持平，属预期行为。

与三类语义等价的参照基线对比（各 100 关键词的两套词库 × 约 10 万 rune 文本：**稀疏**词库互不重叠/包含；**重叠**词库大量前缀链、包含与子串关系并含单字词。参照实现与正式 API 的等价性由守卫测试锁定，见 `bench_test.go`）：

| 对比（纯中文长文本） | 稀疏词库 | 加速比 | 重叠词库 | 加速比 |
|---|---|---|---|---|
| ratchetmatch `FindAll` | 1.71 ms | — | 1.94 ms | — |
| 纯 Trie 重启扫描（无 fail 链、无跳跃） | 2.14 ms | ~1.2x | 2.19 ms | ~1.1x |
| 逐关键词 Boyer-Moore 坏字符搜索 | 16.4 ms | ~9.6x | 21.4 ms | ~11.0x |
| 逐关键词 strings.Index（标准库 SIMD） | 19.1 ms | ~11.1x | 19.2 ms | ~9.9x |

| 对比（中英混合长文本） | 稀疏词库 | 加速比 | 重叠词库 | 加速比 |
|---|---|---|---|---|
| ratchetmatch `FindAll` | 0.95 ms | — | 1.06 ms | — |
| 纯 Trie 重启扫描（无 fail 链、无跳跃） | 1.85 ms | ~1.9x | 1.36 ms | ~1.3x |
| 逐关键词 Boyer-Moore 坏字符搜索 | 10.9 ms | ~11.5x | 14.1 ms | ~13.3x |
| 逐关键词 strings.Index（标准库 SIMD） | 9.2 ms | ~9.7x | 10.0 ms | ~9.4x |

| 对比（FindNext 首命中，混合文本） | 稀疏词库 | 加速比 | 重叠词库 | 加速比 |
|---|---|---|---|---|
| ratchetmatch `FindNext` | 0.09 ms | — | 0.13 ms | — |
| 逐关键词 strings.Index 全文扫一轮 | 1.74 ms | ~19.7x | 1.67 ms | ~12.5x |

要点：逐词基线（BM / strings.Index）随词库线性变慢——每关键词各扫全文一遍，词库从 50 扩到 100 词耗时即翻倍；重叠词库使逐词 BM 的原始出现数暴涨而进一步变慢，自动机却仅小幅变慢（fail 链摊还 O(1)、outLens 继承摊平了包含关系）。纯 Trie 单遍扫描与词库大小基本无关，但无跳跃时混合文本收益全无（~1.9x）；重叠词库噪声字常命中词首字符（中、人、大…），跳跃与首停收益相应回落（~1.3x / ~12.5x），属预期。`strings.Index` 已是标准库 SIMD 加速的最快单串搜索，手写逐字节双循环只会白白丢掉向量化（分析结论，见 `bench_test.go`）。数值为量级参考，随硬件与数据分布浮动。

## 设计与权衡

- **rune 级自动机**：按 Unicode 码点构建 Trie 与转移表，中文按整字符转移，绝不按 UTF-8 字节碎片转移。
- **稀疏转移表 + 失败指针回退**（而非 DFA 全量转移表）：节点仅存自有 trie 边，展平进全局有序数组；查询期段内查找未命中沿失败链回退，摊还 O(1)。全量表在中文大字符集下每节点膨胀至 root 扇出（~百键），内存不可接受，已实测废弃。
- **BM 坏字符跳跃**：自动机处于 root 态时，用「词库首字符集 + 256 位字节过滤器」批量跳过不可能出现匹配起始的文本段，中英混合文本约 1.4x 加速（任何匹配的起始 rune 必为词首字符，跳跃判据等价安全、不漏报）。
- **位置按字节计量**：`Match.Start/End` 为字节偏移，`text[Start:End]` 可直接切片取关键词。不提供 rune 下标 API——`[]rune` 预转换会使 ASCII 文本瞬时内存 +300%、跳跃与首停优化失效。
- **非重叠最左最长语义**：起点最小优先、同一起点取最长（真包含关系一律输出最长匹配）；结果确定，与查找方式无关。
- **同义词分组下沉为输出元数据**：组员各自成词入 trie（整词级等价无法像大小写折叠那样按 rune 轨道合一），组号存为节点的平行数组、命中时随区间带出——分组绝不参与最左最长裁决，查询热路径零新增分支。
- **无锁并发**：`Matcher` 构建后只读，内部为紧凑连续数组布局（近乎无指针），查询零分配，Go GC 标记成本接近常数——适合词库常驻内存数月的服务。
- **容错**：非法 UTF-8 文本不 panic、不漏扫后续内容。

## 快速上手

要求 Go 1.27+，module 路径为 `github.com/JayceChant/ratchetmatch`。

```go
matcher, err := ratchetmatch.New([]string{"上海", "北京", "人工智能", "机器学习"})
if err != nil {
	panic(err)
}
for _, m := range matcher.FindAll(text) {
	fmt.Printf("%d-%d %s\n", m.Start, m.End, m.Keyword) // Start/End 为字节偏移
}
```

完整可运行示例（含输出）见 `example_test.go`；更多拿来即用的完整程序在 `example/` 目录（`go run ./basic/`、`./semantics/`、`./iterate/`、`./wordcount/`、`./synonyms/`——语义对照、按需迭代、词频统计、同义词分组，每个都是可整体复制到自己项目里的自包含单文件）。中文每字占 3 字节、ASCII 每字符 1 字节；匹配是精确匹配，`Beijing` 不会命中 `北京`。需要字符序号时用 `utf8.RuneCountInString(text[:m.Start])` 换算。

## 按需迭代：超长文本首命中即停

`FindNext(text, offset)` 从 `offset` 返回首个命中即终止扫描；用返回的 `Match.End` 作为下一次 `offset` 迭代，序列与 `FindAll` 完全一致——只需前几条时，其后的大段文本完全不会被扫描（长文本基准约 10x）。

## API

| 标识 | 说明 |
|---|---|
| `New(keywords []string, opts ...Option) (*Matcher, error)` | 构建不可变 `Matcher`。词库（关键词与 `WithSynonyms` 组员合并后）为空、含空串、含非法 UTF-8 或 U+FFFD 字节返回可区分的错误；重复关键词去重；同一词出现在两个声明同义词组报错 |
| `(*Matcher) FindAll(text string) []Match` | 全部命中，按 `Start` 升序；无命中返回 `nil` |
| `(*Matcher) FindAllOverlapping(text string) []Match` | 全部出现（含互相重叠者），按 `End` 升序、同 `End` 长度降序；适合词频统计、索引构建，开销输出敏感 O(n+K) |
| `(*Matcher) FindNext(text string, offset int) (Match, bool)` | 从 `offset` 返回首个命中，找到即停。`offset<0` 按 0；`>=len(text)` 或无命中返回 `(Match{}, false)`；落在多字节字符中间时向后对齐 rune 边界 |
| `(*Matcher) CaseFold() bool` | 报告是否为大小写折叠模式（`WithCaseFold` 构建） |
| `(*Matcher) WordGroup(g int) []string` | 返回同义词组 g 的成员词（折叠模式为归一形）；返回内部只读切片，越界组号返回 `nil` |
| `(*Matcher) WordGroups() [][]string` | 返回全部同义词组，按组号升序（下标即 `Match.Group`）；外层切片可自由重排，元素为内部只读切片 |
| `Match{Start, End int; Keyword string; Group int}` | 一次命中；`text[Start:End] == Keyword` 恒成立。`Group` 为命中词的同义词组号，恒有效（未声明的词自成单元素组） |

## 匹配语义（非重叠最左最长）

从左到右，起点最小者优先；同一起点取完整出现的最长关键词。命中之间互不重叠、不留空档，每个文本位置至多属于一个命中。

| 场景 | 词库 / 文本 | 输出 |
|---|---|---|
| 前缀关系取最长 | `{"中国", "中国人"}` / `"我是中国人"` | 仅 `中国人` |
| 前缀未完整出现时取短词 | `{"中", "中毒"}` / `"中x"` | 仅 `中` |
| 重叠时起点更左者胜 | `{"上海", "海口"}` / `"上海口"` | 仅 `上海` |
| 不重叠按序全部输出 | `{"上海", "北京"}` / `"上海人北京"` | `上海`、`北京` |
| 真包含关系一律取最长 | `{"国", "人", "中国人"}` / `"中国人"` | 仅 `中国人` |

需要**全部出现**（含互相重叠）时用 `FindAllOverlapping`：如词库 `{"国", "人", "中国人"}` 在 `"中国人"` 上返回 3 条。该模式无 `FindNext` 版本（重叠语义与无状态按需迭代不兼容）。

### 大小写折叠

匹配模式由 `New` 一次性决定、不可更改。默认大小写敏感的精确匹配；传 `WithCaseFold()` 获得等价于 `strings.EqualFold` 的折叠匹配（逐 rune 的 SimpleFold 轨道）：

```go
fm, _ := ratchetmatch.New([]string{"hello", "世界"}, ratchetmatch.WithCaseFold())
fm.FindAll("Hello, WORLD! 世界") // 命中 Hello(0,5) 与 世界(14,20)
fm.CaseFold()                    // true
```

词库中的大小写变体会合一，不漏报；`Match.Keyword` 为文本原样切片（如 `"Hello"`），`Start`/`End` 仍可直接切文本。同一词库需要两种模式时，分别 `New` 两个实例。

**性能提示**：折叠自动机在构建期把折叠轨道预展开进转移表，查询热路径零逐 rune 折叠比较。实测开销（`bench_fold_test.go`；轨道展开使键数约翻倍的 ASCII 词库为最坏情况，100 关键词 × 约 10 万 rune 英文文本）：

- **查询**：与精确模式基本持平（同词库同文本 `FindAll`：fold 3.05 ms vs exact 3.04 ms；中文词库各组差异均在 ±5% 噪声内）——展开后的键仍在同一条 CSR 二分查找路径上，命中提取的 rune 级回退（`runeStartBack`）只在命中位置做几次 rune 前退。
- **构建**：略高于精确模式（`New` 100 关键词约 1.4x 耗时、+20% 内存；随词库增大摊薄至约 1.1x——轨道展开成本被去重后的大词库摊薄）。SimpleFold 轨道解析与展开键写入均为 `New` 一次性成本。
- **语义收益**：大小写混排文本上 fold 能命中 exact 漏掉的全部变体——ASCII 基准中 fold 多报约 1000 条（首字母大写词），扫描耗时无可测差异。

经验法则：只要需要大小写不敏感就开 `WithCaseFold()`——查询侧开销可忽略；仅超大词库（>1 万关键词）构建时才可能感知到构建开销。

### 同义词分组

需要把同义词在结果里天然归成一组（语义上的「fold」），不必自己维护词→组的映射再事后归并：`WithSynonyms` 声明同义词组，组员自动并入词库，命中的 `Match.Group` 直接携带组号：

```go
m, _ := ratchetmatch.New([]string{"服务器"},
    ratchetmatch.WithSynonyms([][]string{
        {"电脑", "计算机", "PC"}, // 组 0：组员自动入库
    }))
m.FindAll("电脑和服务器")
// 电脑(0,6, Group=0)、服务器(9,18, Group=1)——未声明的词自成单元素组
m.WordGroup(0) // [电脑 计算机 PC]
m.WordGroups() // [[电脑 计算机 PC] [服务器]]——下标即组号
```

要点：

- **分组不改变匹配语义**：非重叠最左最长照旧裁决（词库同时含「电脑」与「电脑城」时，文本「电脑城」命中的是「电脑城」，带着它自己的组）。有/无分组构建的命中区间逐条一致，可直接按 `count[m.Group]++` 聚合。
- **分区编号、恒有效**：声明组按声明顺序编号 0..k-1；未声明分组的词按去重词库序获得单元素组。任何 `Match.Group` 都可直接传给 `WordGroup`；`WordGroups()` 一次返回全部组。
- **与 `WithCaseFold` 正交**：组合使用时词身份按折叠归一形判定——`"PC"` 与 `"pc"` 归一同形，同组自然合一、分属两组则报错；`Group` 兼作折叠命中的规范词身份（`Keyword` 是大小写不定的文本切片，按词计数无需再归一）。
- **校验**：空组、组员空串/非法 UTF-8/含 U+FFFD、同一词出现在两个声明组均报错（错误信息含组号与成员下标，可区分原因）；组内重复成员自动去重。词库可仅由组构成（`New(nil, WithSynonyms(...))`）。

算法原理、API 契约与验收场景的权威描述见 `spec/spec.md`。

## 许可证

本项目基于 [MIT License](LICENSE) 发布。
