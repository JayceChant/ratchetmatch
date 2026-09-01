// 本文件为 ratchetmatch 的内部测试包（package ratchetmatch），
// 以便访问自动机私有字段复用同一份构建结果。
// 基准含三组「无自动机 / 半自动机」参照，从不同侧面量化正式实现（ACBM）的收益：
//   - trieFindAll：纯 Trie 重启扫描——只走 trie 自有边，去掉失败指针回退
//     与 BM 跳跃，失配即回 root 重启（量化 fail 链 + 跳跃的合并收益）；
//   - bmFindAll：纯 Boyer-Moore——逐关键词坏字符规则整文搜索
//     （量化「有单串跳跃、无自动机」与 ACBM 的差距）；
//   - stringsIndexFindAll / stringsIndexFindNext：逐关键词 strings.Index
//     （标准库 SIMD 单串搜索，量化自动机单遍扫描的整体收益）。
//
// 三组参照与正式 API 的等价性由 TestBaselineEquiv 守卫。
// example_test.go 为外部测试包（package ratchetmatch_test），
// 两者并存是 Go 允许的。
package ratchetmatch

import (
	"cmp"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// benchKeywords 为 50 个中文关键词（城市、科技、成语等），作为基准词库。
var benchKeywords = []string{
	// 城市
	"上海", "北京", "广州", "深圳", "杭州", "南京", "成都", "重庆", "武汉", "西安",
	"苏州", "天津", "长沙", "郑州", "青岛", "大连", "厦门", "宁波", "无锡", "佛山",
	// 科技
	"人工智能", "机器学习", "深度学习", "自然语言处理", "计算机视觉", "大数据",
	"云计算", "分布式系统", "微服务", "知识图谱",
	// 成语
	"画蛇添足", "守株待兔", "亡羊补牢", "掩耳盗铃", "买椟还珠", "拔苗助长",
	"卧薪尝胆", "破釜沉舟", "闻鸡起舞", "刻舟求剑",
	// 其他
	"一带一路", "粤港澳大湾区", "宏观调控", "供给侧", "碳中和", "新能源",
	"量子计算", "区块链", "物联网", "数字化转型",
}

// benchNoiseRunes 为常见中文噪声字，用于构造不含关键词的长噪声段。
var benchNoiseRunes = []rune("的在了是和有就不人都一个上中大来时地过说小后于会也这没能为着想看生那起把还进好们")

var (
	benchMatcher *Matcher // 由 benchKeywords 构建的自动机，两个文本共用
	benchTextZh  string   // 纯中文长文本：约 10 万 rune（~300KB）
	benchTextMix string   // 中英混合长文本：约 10 万 rune（约一半 ASCII，一半中文）
	// benchBMTables 为每关键词预构建的 BM 坏字符表：与自动机构建同置循环外，
	// 使三组参照的构建成本口径一致（搜索本身计入基准）。
	benchBMTables [][256]int
)

// benchSink 防止编译器把基准循环内的被测调用优化掉。
var benchSink []Match

func init() {
	var err error
	benchMatcher, err = New(benchKeywords)
	if err != nil {
		panic(err)
	}
	const targetRunes = 100_000
	benchTextZh = buildChineseText(targetRunes)
	benchTextMix = buildMixedText(targetRunes)
	benchBMTables = make([][256]int, len(benchKeywords))
	for i, kw := range benchKeywords {
		benchBMTables[i] = buildBadCharTable(kw)
	}
}

// buildChineseText 构造约 n rune 的纯中文文本：噪声字循环为主体，
// 每隔一段噪声插入一个词库关键词，保证扫描路径上存在足够命中。
func buildChineseText(n int) string {
	var b strings.Builder
	b.Grow(n * 3)
	runes := 0
	i := 0
	for runes < n {
		for range 91 {
			if runes >= n {
				break
			}
			b.WriteRune(benchNoiseRunes[i%len(benchNoiseRunes)])
			runes++
			i++
		}
		if runes >= n {
			break
		}
		kw := benchKeywords[i%len(benchKeywords)]
		b.WriteString(kw)
		runes += len([]rune(kw))
		i++
	}
	return b.String()
}

// buildMixedText 构造约 n rune 的中英混合文本：英文句子（含数字、标点）
// 与中文噪声段交替，各约占一半 rune，并穿插关键词；开头约 10% 为纯噪声
// （不含关键词），供 FindNext 基准体现「找到即停」。
func buildMixedText(n int) string {
	engLine := "The quick brown fox 42 jumps over the lazy dog, 2024-08-28 09:30; id=10086, rate=3.14% [ok]\n"
	var b strings.Builder
	b.Grow(n * 3)
	runes := 0
	i := 0
	// 前 ~10% 纯噪声：第一个命中约出现在文本 10% 处。
	for runes < n/10 {
		b.WriteString(engLine)
		runes += len(engLine) // 纯 ASCII：字节数即 rune 数
		for range len(engLine) {
			if runes >= n/10 {
				break
			}
			b.WriteRune(benchNoiseRunes[i%len(benchNoiseRunes)])
			runes++
			i++
		}
	}
	for runes < n {
		b.WriteString(engLine)
		runes += len(engLine)
		for range 88 {
			if runes >= n {
				break
			}
			b.WriteRune(benchNoiseRunes[i%len(benchNoiseRunes)])
			runes++
			i++
		}
		if runes >= n {
			break
		}
		kw := benchKeywords[i%len(benchKeywords)]
		b.WriteString(kw)
		runes += len([]rune(kw))
		i++
	}
	return b.String()
}

// interval 是一个候选命中的字节区间 [start,end)。
type interval struct{ start, end int }

// collectLeftmostLongest 把逐词枚举出的全部出现归并为非重叠最左最长结果：
// 按起点升序（同起点更长在前）排序后扫描，丢弃与已提交命中重叠者。
// 与 FindAll 语义一致，供 strings.Index / Boyer-Moore 两组逐词参照共用。
func collectLeftmostLongest(text string, occs []interval) []Match {
	slices.SortFunc(occs, func(a, b interval) int {
		if c := cmp.Compare(a.start, b.start); c != 0 {
			return c
		}
		return cmp.Compare(b.end, a.end) // 同起点长的在前
	})
	var out []Match
	for _, o := range occs {
		if n := len(out); n > 0 && o.start < out[n-1].End {
			continue // 与已提交命中重叠：更左/更长候选已被归并，跳过
		}
		out = append(out, Match{Start: o.start, End: o.end, Keyword: text[o.start:o.end]})
	}
	return out
}

// trieFindAll 是纯 Trie 重启参照实现：只使用正式自动机的 trie 自有边
// （rootNext + 各节点 CSR 段），不走失败指针（step 换成单次 find，失配即
// 回 root 重启），也不做 BM 跳跃。每轮从 root 起沿一条 trie 路径下行，
// 记录途径的最后一个词尾（该前缀下完整出现的最长关键词），路径终止即
// 提交并从命中起点的下一 rune 重启；无命中则从起点的下一 rune 重启。
// 与正式实现共用同一份 trie，对比差异恰为「fail 链 + BM 跳跃」两个优化。
// 逐 rune 解码（无效字节按 RuneError 前进 1 字节），非重叠最左最长语义
// 与 FindAll 一致。词尾判定用 outLens[0] == 起点至今路径的字节长度：成品
// 节点不存 termLen，而 outLens 严格降序、首元素即自身词长（fail 链继承的
// 真后缀关键词严格更短），该等式成立当且仅当路径节点是词尾。
func trieFindAll(m *Matcher, text string) []Match {
	n := len(text)
	pos := 0
	var out []Match
	for pos < n {
		// 从 root 起尝试一条 trie 路径：起点 rune 无对应边则跳过一个 rune
		r, startSize := utf8.DecodeRuneInString(text[pos:])
		node, ok := m.rootNext[r]
		if !ok {
			pos += startSize
			continue
		}
		p := pos + startSize // 起点 rune 已消费为路径首步
		end := -1            // 当前路径上最后一个词尾位置（-1 表示无完整命中）
		for {
			if lens := m.nodes[node].outLens; len(lens) > 0 && int(lens[0]) == p-pos {
				end = p // 词尾判定：outLens[0] 恰为自身词长（见函数注释）
			}
			if m.nodes[node].count == 0 || p >= n {
				break // 叶子或文本耗尽：路径终止
			}
			r, size := utf8.DecodeRuneInString(text[p:])
			next := m.find(node, r)
			if next == 0 {
				break // 失配：路径终止（无 fail 链，不回退）
			}
			node = next
			p += size
		}
		if end < 0 {
			pos += startSize // 本起点无完整命中：前进一个 rune 重新起步
			continue
		}
		out = append(out, Match{Start: pos, End: end, Keyword: text[pos:end]})
		_, sz := utf8.DecodeRuneInString(text[end:]) // 命中后从其下一 rune 重启
		pos = end + sz
	}
	return out
}

// buildBadCharTable 为单个关键词构建 Boyer-Moore 坏字符表：
// table[b] 为字节 b 在关键词内最后一次出现位置到串尾的距离，
// 未出现则为 len(kw)（失配时窗口滑过整个关键词）。
func buildBadCharTable(kw string) [256]int {
	var table [256]int
	for i := range table {
		table[i] = len(kw)
	}
	for i := range len(kw) - 1 {
		table[kw[i]] = len(kw) - 1 - i
	}
	return table
}

// bmFindAll 是纯 Boyer-Moore 参照实现：逐关键词做教科书式坏字符搜索
// （窗口右端对齐、自右向左逐字节比较、失配按坏字符表跳跃），枚举全部
// 出现后归并为最左最长。无自动机、无 fail 链，量化「单串跳跃」相对
// ACBM 的差距；与 stringsIndexFindAll（无跳跃、SIMD）互为另一端参照。
// 坏字符表由调用方预构建传入：其他参照的构建成本（自动机、归并表）
// 均在基准循环外，BM 表同口径处理。
func bmFindAll(keywords []string, tables [][256]int, text string) []Match {
	n := len(text)
	var occs []interval
	for i, kw := range keywords {
		m := len(kw)
		if m == 0 || m > n {
			continue
		}
		table := tables[i] // 表由调用方预构建（与其他参照的构建成本同置循环外）
		for i := 0; i+m <= n; {
			// 窗口 [i, i+m)：自右向左比较，失配按坏字符表跳跃
			j := m - 1
			for j >= 0 && text[i+j] == kw[j] {
				j--
			}
			if j < 0 {
				occs = append(occs, interval{i, i + m})
				i += m // 同关键词非重叠枚举；跨词重叠在归并时处理
				continue
			}
			// 坏字符 text[i+j]：滑动窗口使其最后一次出现对齐到失配位置；
			// 表值不足 1 时（坏字符在失配位右侧重复出现）至少前进 1
			i += max(table[text[i+j]]-(m-1-j), 1)
		}
	}
	return collectLeftmostLongest(text, occs)
}

// stringsIndexFindAll 是逐关键词 strings.Index 参照实现：不建自动机，
// 逐关键词独立搜索全量枚举出现，再按最左最长语义归并。用于量化自动机
// 单遍扫描（ACBM）相对朴素做法的整体收益；结果须与 FindAll 完全一致。
//
// 基线形态说明（2026-09-01 分析定论，勿重复实验）：strings.Index 在
// amd64 走 internal/bytealg 的 SIMD 实现（AVX2/SSE2，一条指令比对
// 16–32 字节），已是单串搜索的最快形态；换成「文本下标外循环 × 逐关键
// 词逐字节比较」的纯标量双循环，同为 O(K·n) 但常数差一个向量宽度
// （约 1500 万次标量迭代 vs 约 50 万次向量迭代），只会持平或更慢。
func stringsIndexFindAll(keywords []string, text string) []Match {
	var occs []interval
	for _, kw := range keywords {
		for i := 0; ; {
			j := strings.Index(text[i:], kw)
			if j < 0 {
				break
			}
			i += j
			occs = append(occs, interval{i, i + len(kw)})
			i += len(kw) // 同关键词非重叠枚举；跨词重叠在归并时处理
		}
	}
	return collectLeftmostLongest(text, occs)
}

// stringsIndexFindNext 是逐词 strings.Index 的「首命中即停」参照：每个
// 关键词只做一次全文搜索取最左出现，比较后返回全局第一个——未触及的
// 文本不再有任何搜索调用。与 FindNext 语义一致，用于量化首命中即停在
// 朴素做法下的收益，对照 BenchmarkFindNextFirst。
func stringsIndexFindNext(keywords []string, text string, offset int) (Match, bool) {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(text) {
		return Match{}, false
	}
	best := Match{}
	found := false
	for _, kw := range keywords {
		j := strings.Index(text[offset:], kw)
		if j < 0 {
			continue
		}
		s, e := offset+j, offset+j+len(kw)
		if !found || s < best.Start || (s == best.Start && e > best.End) {
			best, found = Match{Start: s, End: e, Keyword: kw}, true
		}
	}
	return best, found
}

// TestBaselineEquiv 校验三组参照在基准语料下与正式 API 等价（FindAll
// 全量逐条相等、首命中与 FindAll 首条相等），防止基准对比的参照实现
// 悄悄偏离语义（对比失真）。任意词库/文本的随机等价性由
// ratchetmatch_test.go 的 naiveSearch oracle 测试覆盖，此处只守基准语料。
func TestBaselineEquiv(t *testing.T) {
	for _, text := range []struct{ name, body string }{
		{"benchTextZh", benchTextZh},
		{"benchTextMix", benchTextMix},
	} {
		want := benchMatcher.FindAll(text.body)
		for _, got := range [][]Match{
			trieFindAll(benchMatcher, text.body),
			bmFindAll(benchKeywords, benchBMTables, text.body),
			stringsIndexFindAll(benchKeywords, text.body),
		} {
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s: 参照实现与 FindAll 不一致（%d vs %d 条）",
					text.name, len(got), len(want))
			}
		}
		next, ok := stringsIndexFindNext(benchKeywords, text.body, 0)
		if !ok {
			t.Fatalf("%s: stringsIndexFindNext 应有命中", text.name)
		}
		if first := want[0]; next != first {
			t.Fatalf("%s: stringsIndexFindNext = %+v, FindAll 首条 = %+v", text.name, next, first)
		}
	}
}

// BenchmarkFindAllChinese 纯中文长文本 FindAll。
func BenchmarkFindAllChinese(b *testing.B) {
	for b.Loop() {
		benchSink = benchMatcher.FindAll(benchTextZh)
	}
}

// BenchmarkFindAllMixed 中英混合长文本 FindAll。
func BenchmarkFindAllMixed(b *testing.B) {
	for b.Loop() {
		benchSink = benchMatcher.FindAll(benchTextMix)
	}
}

// BenchmarkFindNextFirst 中英混合文本只找第一个命中（体现达到目的即停）：
// 文本开头即为大段 ASCII 噪声，跳跃能快速越过，找到即返回。
func BenchmarkFindNextFirst(b *testing.B) {
	for b.Loop() {
		m, ok := benchMatcher.FindNext(benchTextMix, 0)
		if !ok {
			b.Fatal("应有命中")
		}
		benchSink = []Match{m}
	}
}

// BenchmarkTrieChinese 纯中文长文本、纯 Trie 重启扫描（参照：无 fail 链、无跳跃）。
func BenchmarkTrieChinese(b *testing.B) {
	for b.Loop() {
		benchSink = trieFindAll(benchMatcher, benchTextZh)
	}
}

// BenchmarkTrieMixed 中英混合长文本、纯 Trie 重启扫描（参照：无 fail 链、无跳跃）。
func BenchmarkTrieMixed(b *testing.B) {
	for b.Loop() {
		benchSink = trieFindAll(benchMatcher, benchTextMix)
	}
}

// BenchmarkBMChinese 纯中文长文本、逐关键词 Boyer-Moore 坏字符搜索（参照）。
func BenchmarkBMChinese(b *testing.B) {
	for b.Loop() {
		benchSink = bmFindAll(benchKeywords, benchBMTables, benchTextZh)
	}
}

// BenchmarkBMMixed 中英混合长文本、逐关键词 Boyer-Moore 坏字符搜索（参照）。
func BenchmarkBMMixed(b *testing.B) {
	for b.Loop() {
		benchSink = bmFindAll(benchKeywords, benchBMTables, benchTextMix)
	}
}

// BenchmarkStringsIndexChinese 纯中文长文本、逐关键词 strings.Index（参照：标准库 SIMD 单串搜索）。
func BenchmarkStringsIndexChinese(b *testing.B) {
	for b.Loop() {
		benchSink = stringsIndexFindAll(benchKeywords, benchTextZh)
	}
}

// BenchmarkStringsIndexMixed 中英混合长文本、逐关键词 strings.Index（参照：标准库 SIMD 单串搜索）。
func BenchmarkStringsIndexMixed(b *testing.B) {
	for b.Loop() {
		benchSink = stringsIndexFindAll(benchKeywords, benchTextMix)
	}
}

// BenchmarkStringsIndexNextFirst 逐词 strings.Index 找第一个命中：50 个关键词
// 各做一次全文搜索（对照 BenchmarkFindNextFirst）。
func BenchmarkStringsIndexNextFirst(b *testing.B) {
	for b.Loop() {
		m, ok := stringsIndexFindNext(benchKeywords, benchTextMix, 0)
		if !ok {
			b.Fatal("应有命中")
		}
		benchSink = []Match{m}
	}
}
