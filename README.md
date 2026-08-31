# ratchetsearch

针对中文优化的 ACBM（Aho-Corasick + Boyer-Moore）多模式匹配库：一次构建词库自动机，对每条长文本单遍扫描，返回全部关键词命中。零第三方依赖，仅使用 Go 标准库。

## 适用范围

**适合**：词库固定、目标文本多变——一次 `New` 构建自动机后，对大量随机到达的长文本反复查询（内容过滤、审计、风控等流式扫描场景）。

**不适合**：目标文本固定、查找关键词多变——此时更应对目标文本做一次性预处理（如关键词倒排索引等），按关键词查询而非按文本扫描；本库「按文本扫描」的模型与每次 `New` 的构建成本在该场景下不占优。

## 特性

- **rune 级自动机**：按 Unicode 码点构建 Trie 与转移表，中文按整字符处理，绝不按 UTF-8 字节碎片转移
- **稀疏转移 + 失败指针回退**：节点仅存自有 trie 边并展平进全局有序数组，查询期段内查找未命中沿失败链回退，摊还 O(1)
- **BM 坏字符跳跃**：自动机处于 root 态时，用「词库首字符集 + 256 位字节过滤器」批量跳过不可能出现匹配起始的文本段；中英混合文本约 1.4x 加速
- **FindNext 首命中即停**：超长文本按需查找，找到即返回、不遍历剩余文本（基准约 10x）
- **非重叠最左最长语义**：起点最小优先、同一起点取最长（真包含关系一律输出最长匹配）；结果确定，与查找方式无关
- **无锁并发**：`Matcher` 构建后只读，`FindAll` / `FindNext` 可并发调用（`-race` 验证）
- **容错**：非法 UTF-8 文本不 panic、不漏扫后续内容

## 安装

要求 Go 1.27+。本仓库 module 名为 `ratchetsearch`：

```bash
go get ratchetsearch
```

> 发布到代码托管平台时，将 `go.mod` 的 module 名替换为实际仓库路径，并同步修改导入路径。

## 快速上手：找出全部命中

```go
package main

import (
	"fmt"

	"ratchetsearch"
)

func main() {
	matcher, err := ratchetsearch.New([]string{"上海", "北京", "广州", "深圳", "人工智能", "机器学习"})
	if err != nil {
		panic(err)
	}
	text := "上海的人工智能产业发展迅速。Beijing is the capital. 广州与深圳同属粤港澳大湾区，机器学习应用广泛。"
	for _, m := range matcher.FindAll(text) {
		fmt.Printf("%d-%d %s\n", m.Start, m.End, m.Keyword)
	}
	// 输出：
	// 0-6 上海
	// 9-21 人工智能
	// 66-72 广州
	// 75-81 深圳
	// 108-120 机器学习
}
```

注意 `Match.Start/End` 为 **text 中的字节偏移**（`[Start, End)` 半开区间），中文每字占 3 字节、ASCII 每字符占 1 字节；匹配是精确的字符串匹配，`Beijing` 不会命中中文关键词 `北京`。

需要字符序号（第几个字）时，用 `utf8.RuneCountInString(text[:m.Start])` 按需换算即可。本库刻意不提供 rune 下标 API：匹配算法本身按字符（rune）转移，仅位置/长度用字节计量，这样 `text[m.Start:m.End]` 可直接切片取关键词，也让「不解码就跳过无关节节」的跳跃优化和 FindNext 的找到即停发挥最大效果。

## 按需迭代：超长文本首命中即停

`FindNext` 从 `offset` 开始查找第一个命中即终止扫描；用返回的 `Match.End` 作为下一次调用的 `offset` 迭代，得到的序列与 `FindAll` 完全一致。以下示例收集满 3 条就停止，其后的大段文本完全不会被扫描：

```go
matcher, err := ratchetsearch.New([]string{"上海", "北京", "人工智能", "机器学习"})
if err != nil {
	panic(err)
}
// 用拼接模拟长文档：2000 个噪声字（6000 字节）与关键词交替出现。
noise := strings.Repeat("的在了是和有就不人都一", 200)
text := noise + "上海" + noise + "人工智能" + noise + "机器学习" + noise + "北京" + noise

first, ok := matcher.FindNext(text, 0)
if !ok {
	fmt.Println("无命中")
	return
}
hits := []ratchetsearch.Match{first}
off := first.End
for len(hits) < 3 {
	m, ok := matcher.FindNext(text, off)
	if !ok {
		break
	}
	hits = append(hits, m)
	off = m.End
}
for _, m := range hits {
	fmt.Printf("%d-%d %s\n", m.Start, m.End, m.Keyword)
}
// 输出：
// 6600-6606 上海
// 13206-13218 人工智能
// 19818-19830 机器学习
```

## API

| 标识 | 说明 |
|---|---|
| `New(keywords []string) (*Matcher, error)` | 构建不可变 `Matcher`。词库为空或含空字符串返回可区分的错误；重复关键词自动去重 |
| `(*Matcher) FindAll(text string) []Match` | 返回全部命中，按 `Start` 升序；无命中或 text 为空返回 `nil` |
| `(*Matcher) FindAllOverlapping(text string) []Match` | 重叠全量返回：全部出现（含互相重叠者），按 `End` 升序、同 `End` 长度降序；适合词频统计、索引构建。开销输出敏感 O(n+K)，K 为出现总数 |
| `(*Matcher) FindNext(text string, offset int) (Match, bool)` | 无状态按需查找：从 `offset`（字节偏移）返回首个命中，找到即终止。`offset < 0` 按 0 处理；`offset >= len(text)` 或无命中返回 `(Match{}, false)`；`offset` 落在多字节字符中间时向后对齐 rune 边界 |
| `Match{Start, End int; Keyword string}` | 一次命中；`text[Start:End] == Keyword` 恒成立 |

## 匹配语义（非重叠最左最长）

| 场景 | 词库 / 文本 | 输出 |
|---|---|---|
| 前缀关系取最长 | `{"中国", "中国人"}` / `"我是中国人"` | 仅 `中国人` |
| 前缀未完整出现时取短词 | `{"中", "中毒"}` / `"中x"` | 仅 `中` |
| 重叠时起点更左者胜 | `{"上海", "海口"}` / `"上海口"` | 仅 `上海` |
| 同结尾取更长 | `{"他", "其他"}` / `"其他"` | 仅 `其他` |
| 不重叠按序全部输出 | `{"上海", "北京"}` / `"上海人北京"` | `上海`、`北京` |
| 真包含关系一律取最长 | `{"国", "人", "中国人"}` / `"中国人"` | 仅 `中国人` |
| 断词后按 fail 结算短词 | `{"国", "人", "中国人"}` / `"中国梦"` | 仅 `国` |

规则要点：从左到右，起点最小者优先；同一起点取完整出现的最长关键词（长词进行中优先延续，断词才结算短词）。命中之间互不重叠，每个文本位置至多属于一个命中；同一关键词同一位置至多输出一次。

需要**全部出现**（含互相重叠，用于词频统计/索引构建）时用 `FindAllOverlapping`：如词库 `{"国", "人", "中国人"}` 在 `"中国人"` 上返回 3 条（`国`、`中国人`、`人`），其输出按 `End` 升序而非 `Start` 升序。该模式无 `FindNext` 对应版本（重叠语义与无状态按需迭代不兼容）。

## 性能

```bash
go test -bench . -run '^$'
```

参考结论（详见 `bench_test.go`）：中英混合文本下坏字符跳跃约 1.4x 加速；`FindNext` 首命中即停对长文本约 10x。中文纯文本词密场景跳跃空间有限，收益趋于持平，属预期行为。

## 内存与 GC

`Matcher` 构建后为紧凑的连续数组布局（节点切片 + 全局转移数组，按下标索引），内部近乎没有指针引用：大词库的内存占用显著低于每节点持有 map 与对象指针的实现，且 Go GC 标记成本接近常数——对把词库常驻内存数月的过滤/审计类服务，这意味着更低的后台 CPU 占用与更平稳的延迟。查询过程零分配，除返回结果切片外不产生垃圾。

## 设计文档

算法原理、匹配语义与 API 契约的权威描述见 `spec/spec.md`；实现进度见 `spec/tasks.md`，验收清单见 `spec/checklist.md`。
