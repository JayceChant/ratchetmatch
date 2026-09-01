// 本文件为 ratchetmatch 的内部测试包（package ratchetmatch），
// 以便访问自动机私有字段 m.nodes 复用同一份构建结果。
// 基准含两组参照：naiveFindAll 对比「扫描期有无 BM 跳跃」（同一自动机、
// 唯一差异是 root 态是否 skipForward，fail 链两边共用、相互抵消）；
// naiveMultiFindAll / naiveMultiFindNext 对比「朴素多模式匹配」（逐关键词
// strings.Index，量化自动机单遍扫描的整体收益）。example_test.go 为外部
// 测试包（package ratchetmatch_test），两者并存是 Go 允许的。
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

// naiveFindAll 是 search.go 中 scan 的「无跳跃」参照实现：逐 rune 解码并做
// 段内查找 + fail 回退（state==0 时也不调用 skipForward，照常解码转移），
// 最左最长链收集逻辑与正式实现完全一致，用于量化 Boyer-Moore 跳跃的收益。
func naiveFindAll(m *Matcher, text string) []Match {
	n := len(text)
	pos := 0
	var state int32
	var chain []pendHit // 待提交链（与 scan 相同的规则；基准中无需内联数组优化）
	var out []Match
	for pos < n {
		// 与正式实现的唯一区别：不调用 m.skipForward，root 态也逐 rune 转移。
		r, size := utf8.DecodeRuneInString(text[pos:])
		state = m.step(state, r) // 段内查找 + fail 回退，语义与正式实现一致
		pos += size
		// 与正式实现一致：最左最长链规则（更左弹出链尾、同起点取更长、
		// 不重叠入链、其余遮蔽）；自动机回 root 或扫描结束时提交整链
		for _, l := range m.nodes[state].outLens {
			cs, ce := int32(pos)-l, int32(pos)
			for len(chain) > 0 && cs < chain[len(chain)-1].start {
				chain = chain[:len(chain)-1]
			}
			if len(chain) == 0 {
				chain = append(chain, pendHit{cs, ce})
				continue
			}
			tail := &chain[len(chain)-1]
			switch {
			case cs == tail.start:
				if ce > tail.end {
					tail.end = ce
				}
			case cs >= tail.end:
				chain = append(chain, pendHit{cs, ce})
			}
		}
		if state == 0 {
			for _, p := range chain {
				out = append(out, Match{
					Start:   int(p.start),
					End:     int(p.end),
					Keyword: text[p.start:p.end],
				})
			}
			chain = chain[:0]
		}
	}
	for _, p := range chain {
		out = append(out, Match{
			Start:   int(p.start),
			End:     int(p.end),
			Keyword: text[p.start:p.end],
		})
	}
	return out
}

// naiveMultiFindAll 是朴素多模式参照实现：不建自动机，逐关键词独立
// strings.Index 全量枚举出现，再按最左最长语义归并——起点最小优先、
// 同起点取最长、已提交命中的 [Start,End) 区间不重叠。用于量化自动机
// 单遍扫描（ACBM）相对朴素做法的整体收益；结果须与 FindAll 完全一致。
//
// 基线形态说明（2026-09-01 分析定论，勿重复实验）：strings.Index 在
// amd64 走 internal/bytealg 的 SIMD 实现（AVX2/SSE2，一条指令比对
// 16–32 字节），已是单串搜索的最快形态；换成「文本下标外循环 × 逐关键
// 词逐字节比较」的纯标量双循环，同为 O(K·n) 但常数差一个向量宽度
// （约 1500 万次标量迭代 vs 约 50 万次向量迭代），只会持平或更慢。
// 朴素侧更强的形态只剩「首字节位图 + 同首字符桶」——但那本质是深度 1
// 的 trie（自动机的 root），与被测实现的 byteFilter/rootNext 同构，
// 不再是「朴素」基线；如需分层归因可另行实验，不入基准。
func naiveMultiFindAll(keywords []string, text string) []Match {
	type occ struct{ start, end int }
	var occs []occ
	for _, kw := range keywords {
		for i := 0; ; {
			j := strings.Index(text[i:], kw)
			if j < 0 {
				break
			}
			i += j
			occs = append(occs, occ{i, i + len(kw)})
			i += len(kw) // 与 FindAll 同为非重叠语义：同关键词的相邻出现不重叠
		}
	}
	slices.SortFunc(occs, func(a, b occ) int {
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

// naiveMultiFindNext 是朴素版「首命中即停」：仅对 50 个关键词做一轮
// strings.Index（各取最左出现、保留最长），比较后返回全局第一个——
// 未触及的文本不再有任何 strings.Index 调用。与 FindNext 语义一致，
// 用于量化首命中即停在朴素做法下的收益上限。
func naiveMultiFindNext(keywords []string, text string, offset int) (Match, bool) {
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

// TestNaiveMultiEquiv 校验两组朴素参照在基准语料下与正式 API 等价，
// 防止基准对比的参照实现悄悄偏离语义（对比失真）。
func TestNaiveMultiEquiv(t *testing.T) {
	for _, text := range []struct{ name, body string }{
		{"benchTextZh", benchTextZh},
		{"benchTextMix", benchTextMix},
	} {
		want := benchMatcher.FindAll(text.body)
		got := naiveMultiFindAll(benchKeywords, text.body)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: naiveMultiFindAll 与 FindAll 不一致（%d vs %d 条）",
				text.name, len(got), len(want))
		}
		next, ok := naiveMultiFindNext(benchKeywords, text.body, 0)
		if !ok {
			t.Fatalf("%s: naiveMultiFindNext 应有命中", text.name)
		}
		if first := want[0]; next != first {
			t.Fatalf("%s: naiveMultiFindNext = %+v, FindAll 首条 = %+v", text.name, next, first)
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

// BenchmarkFindAllChineseNoSkip 纯中文长文本、无跳跃的朴素逐 rune 扫描（参照）。
func BenchmarkFindAllChineseNoSkip(b *testing.B) {
	for b.Loop() {
		benchSink = naiveFindAll(benchMatcher, benchTextZh)
	}
}

// BenchmarkFindAllMixedNoSkip 中英混合长文本、无跳跃的朴素逐 rune 扫描（参照）。
func BenchmarkFindAllMixedNoSkip(b *testing.B) {
	for b.Loop() {
		benchSink = naiveFindAll(benchMatcher, benchTextMix)
	}
}

// BenchmarkNaiveMultiChinese 纯中文长文本、逐关键词 strings.Index 的朴素多模式匹配（参照）。
func BenchmarkNaiveMultiChinese(b *testing.B) {
	for b.Loop() {
		benchSink = naiveMultiFindAll(benchKeywords, benchTextZh)
	}
}

// BenchmarkNaiveMultiMixed 中英混合长文本、逐关键词 strings.Index 的朴素多模式匹配（参照）。
func BenchmarkNaiveMultiMixed(b *testing.B) {
	for b.Loop() {
		benchSink = naiveMultiFindAll(benchKeywords, benchTextMix)
	}
}

// BenchmarkNaiveMultiNextFirst 朴素做法找第一个命中：50 个关键词各做一次
// 全文 strings.Index（对照 BenchmarkFindNextFirst）。
func BenchmarkNaiveMultiNextFirst(b *testing.B) {
	for b.Loop() {
		m, ok := naiveMultiFindNext(benchKeywords, benchTextMix, 0)
		if !ok {
			b.Fatal("应有命中")
		}
		benchSink = []Match{m}
	}
}
