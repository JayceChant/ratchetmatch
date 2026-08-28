// 本文件为 ratchetsearch 的内部测试包（package ratchetsearch），
// 以便访问自动机私有字段 m.nodes 复用同一份构建结果，只对比「扫描期有无跳跃」。
// example_test.go 为外部测试包（package ratchetsearch_test），两者并存是 Go 允许的。
package ratchetsearch

import (
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
		for j := 0; j < 91 && runes < n; j++ {
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
		for j := 0; j < len(engLine) && runes < n/10; j++ {
			b.WriteRune(benchNoiseRunes[i%len(benchNoiseRunes)])
			runes++
			i++
		}
	}
	for runes < n {
		b.WriteString(engLine)
		runes += len(engLine)
		for j := 0; j < 88 && runes < n; j++ {
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

// naiveFindAll 是 search.go 中 scan 的「无跳跃」参照实现：逐 rune 解码并查
// m.nodes[state].next 完成转移（state==0 时也不调用 skipForward，照常解码转移），
// pending 贪心收集逻辑与正式实现完全一致，用于量化 Boyer-Moore 跳跃的收益。
func naiveFindAll(m *Matcher, text string) []Match {
	n := len(text)
	pos := 0
	var state int32
	var pendStart, pendLen int32
	var out []Match
	flush := func() {
		if pendLen == 0 {
			return
		}
		out = append(out, Match{
			Start:   int(pendStart),
			End:     int(pendStart + pendLen),
			Keyword: text[pendStart : pendStart+pendLen],
		})
	}
	for pos < n {
		// 与正式实现的唯一区别：不调用 m.skipForward，root 态也逐 rune 转移。
		r, size := utf8.DecodeRuneInString(text[pos:])
		state = m.step(state, r) // 二分转移，未含回 root，语义与全量表查找一致
		pos += size
		// 与正式实现一致：候选按长度降序，选第一个与 pending 兼容的
		for _, l := range m.nodes[state].outLens {
			cs := int32(pos) - l
			switch {
			case pendLen == 0:
				pendStart, pendLen = cs, l
			case cs == pendStart:
				pendLen = l
			case cs >= pendStart+pendLen:
				flush()
				pendStart, pendLen = cs, l
			default:
				continue
			}
			break
		}
	}
	flush()
	return out
}

// BenchmarkFindAllChinese 纯中文长文本 FindAll。
func BenchmarkFindAllChinese(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchSink = benchMatcher.FindAll(benchTextZh)
	}
}

// BenchmarkFindAllMixed 中英混合长文本 FindAll。
func BenchmarkFindAllMixed(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchSink = benchMatcher.FindAll(benchTextMix)
	}
}

// BenchmarkFindNextFirst 中英混合文本只找第一个命中（体现达到目的即停）：
// 文本开头即为大段 ASCII 噪声，跳跃能快速越过，找到即返回。
func BenchmarkFindNextFirst(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m, ok := benchMatcher.FindNext(benchTextMix, 0)
		if !ok {
			b.Fatal("应有命中")
		}
		benchSink = []Match{m}
	}
}

// BenchmarkFindAllChineseNoSkip 纯中文长文本、无跳跃的朴素逐 rune 扫描（参照）。
func BenchmarkFindAllChineseNoSkip(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchSink = naiveFindAll(benchMatcher, benchTextZh)
	}
}

// BenchmarkFindAllMixedNoSkip 中英混合长文本、无跳跃的朴素逐 rune 扫描（参照）。
func BenchmarkFindAllMixedNoSkip(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchSink = naiveFindAll(benchMatcher, benchTextMix)
	}
}
