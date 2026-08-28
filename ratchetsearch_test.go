package ratchetsearch

import (
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// 通用辅助
// ---------------------------------------------------------------------------

// mustNew 构建 Matcher，意外失败时立即终止当前测试。
func mustNew(t *testing.T, keywords []string) *Matcher {
	t.Helper()
	m, err := New(keywords)
	if err != nil {
		t.Fatalf("New(%q) 意外失败: %v", keywords, err)
	}
	return m
}

// assertMatches 逐一断言命中序列的 Start/End/Keyword 完全一致，
// 并自检 text[Start:End] == Keyword 以及两个偏移均落在 rune 边界上。
func assertMatches(t *testing.T, text, label string, got, want []Match) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: 命中数量不符\ngot  (%d 条): %v\nwant (%d 条): %v", label, len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s: 第 %d 条命中不符: got %+v, want %+v", label, i, got[i], want[i])
		}
	}
	for _, mt := range got {
		if text[mt.Start:mt.End] != mt.Keyword {
			t.Errorf("%s: 字节偏移与关键词不一致: text[%d:%d] = %q, Keyword = %q",
				label, mt.Start, mt.End, text[mt.Start:mt.End], mt.Keyword)
		}
		if !utf8.RuneStart(text[mt.Start]) || (mt.End < len(text) && !utf8.RuneStart(text[mt.End])) {
			t.Errorf("%s: 命中 [%d,%d) 未落在 rune 边界上", label, mt.Start, mt.End)
		}
	}
}

// noiseChars 是拼接随机文本用的噪声字符，用于打断关键词的连续性。
var noiseChars = []string{"x", "y", "，", "。"}

// drawKeywords 从 pool 中随机抽取 [minK,maxK] 个互不重复的关键词。
func drawKeywords(rng *rand.Rand, pool []string, minK, maxK int) []string {
	k := minK + rng.Intn(maxK-minK+1)
	idx := rng.Perm(len(pool))[:k]
	kws := make([]string, 0, k)
	for _, i := range idx {
		kws = append(kws, pool[i])
	}
	return kws
}

// randomText 用词库关键词与噪声字符随机拼接，直到 rune 数达到 [minR,maxR] 中的随机目标值。
func randomText(rng *rand.Rand, kws []string, minR, maxR int) string {
	target := minR + rng.Intn(maxR-minR+1)
	var b strings.Builder
	for n := 0; n < target; {
		var tok string
		if rng.Intn(2) == 0 {
			tok = kws[rng.Intn(len(kws))]
		} else {
			tok = noiseChars[rng.Intn(len(noiseChars))]
		}
		b.WriteString(tok)
		n += utf8.RuneCountInString(tok)
	}
	return b.String()
}

// findAllByFindNext 用 FindNext 从 0 开始、以上一次命中的 End 为下一 offset 迭代收集全部命中。
func findAllByFindNext(m *Matcher, text string) []Match {
	var out []Match
	for off := 0; ; {
		mt, ok := m.FindNext(text, off)
		if !ok {
			return out
		}
		out = append(out, mt)
		off = mt.End
	}
}

// ---------------------------------------------------------------------------
// 1. 构建期校验
// ---------------------------------------------------------------------------

func TestNewValidation(t *testing.T) {
	// 空词库（nil 与空切片）必须报错，且错误信息含 "empty"（或中文 "空"）
	for _, tc := range []struct {
		name     string
		keywords []string
	}{
		{"nil 词库", nil},
		{"空切片", []string{}},
	} {
		m, err := New(tc.keywords)
		if err == nil {
			t.Fatalf("%s: 期望报错，实际构建成功", tc.name)
		}
		if m != nil {
			t.Errorf("%s: 出错时返回的 Matcher 应为 nil", tc.name)
		}
		if !strings.Contains(err.Error(), "empty") && !strings.Contains(err.Error(), "空") {
			t.Errorf("%s: 错误信息应含 \"empty\" 或 \"空\"，实际: %q", tc.name, err.Error())
		}
	}

	// 空字符串关键词：错误信息需包含其下标
	_, err := New([]string{"a", ""})
	if err == nil {
		t.Fatal(`New([]string{"a",""}): 期望报错，实际构建成功`)
	}
	if !strings.Contains(err.Error(), "index") {
		t.Errorf(`New([]string{"a",""}): 错误信息应含 "index"，实际: %q`, err.Error())
	}
	if !strings.Contains(err.Error(), "1") {
		t.Errorf(`New([]string{"a",""}): 错误信息应含具体下标 1，实际: %q`, err.Error())
	}

	// 正常构建（含前缀关系词），并做白盒验证：
	// 每个关键词从 root 独立插入，故 "世界"(root→世→界) 与 "你好世界"
	// 中的 "世"(你→好→世) 是不同节点：root + 你/你好/好世/好世界/世/界 共 7 个；
	// runeSet 恰为 4 个不同汉字；root 恰有 你、世 两个直接孩子。
	m := mustNew(t, []string{"你好", "世界", "你好世界"})
	if len(m.nodes) != 7 {
		t.Errorf("白盒: trie 节点数 = %d, 期望 7", len(m.nodes))
	}
	if len(m.runeSet) != 4 {
		t.Errorf("白盒: runeSet 大小 = %d, 期望 4", len(m.runeSet))
	}
	if len(m.nodes[0].next) != 2 {
		t.Errorf("白盒: root 直接孩子数 = %d, 期望 2", len(m.nodes[0].next))
	}
}

// ---------------------------------------------------------------------------
// 2. 中文匹配与字节偏移
// ---------------------------------------------------------------------------

func TestChineseMatching(t *testing.T) {
	m := mustNew(t, []string{"你好", "世界", "你好世界"})
	tests := []struct {
		text string
		want []Match
	}{
		// "你好" 与 "世界" 同被 "你好世界" 遮蔽/替换：只剩最长的一条
		{"你好世界", []Match{{0, 12, "你好世界"}}},
		// 逗号（3 字节）打断后各自独立命中
		{"你好，世界", []Match{{0, 6, "你好"}, {9, 15, "世界"}}},
	}
	for _, tc := range tests {
		got := m.FindAll(tc.text)
		// assertMatches 内部会额外验证 text[Start:End] == Keyword（字节偏移自检）
		assertMatches(t, tc.text, "FindAll("+tc.text+")", got, tc.want)
	}
}

// ---------------------------------------------------------------------------
// 3. 非重叠贪心语义
// ---------------------------------------------------------------------------

func TestGreedySemantics(t *testing.T) {
	tests := []struct {
		name     string
		keywords []string
		text     string
		want     []Match
	}{
		// "我是中国人"：我是 6 字节，"中国人" = [6,15)
		{"前缀取最长", []string{"中国", "中国人"}, "我是中国人", []Match{{6, 15, "中国人"}}},
		{"前缀未完整出现取短词", []string{"中", "中毒"}, "中x", []Match{{0, 3, "中"}}},
		{"重叠取先命中", []string{"上海", "海口"}, "上海口", []Match{{0, 6, "上海"}}},
		{"同结尾取更长", []string{"他", "其他"}, "其他", []Match{{0, 6, "其他"}}},
		// 上海=[0,6) 人=[6,9) 北京=[9,15)
		{"不重叠全输出", []string{"上海", "北京"}, "上海人北京", []Match{{0, 6, "上海"}, {9, 15, "北京"}}},
		{"嵌套前缀链", []string{"a", "ab", "abc"}, "xabcx", []Match{{1, 4, "abc"}}},
		{"连续同词", []string{"a", "ab"}, "abab", []Match{{0, 2, "ab"}, {2, 4, "ab"}}},
		// "国" 先命中 [3,6)；"中国人" [0,9) 与之重叠被跳过后，
		// 同位置结束的 "人" [6,9) 起始不早于 6，应当接续命中
		{"长词遮蔽下接续命中", []string{"国", "人", "中国人"}, "中国人",
			[]Match{{3, 6, "国"}, {6, 9, "人"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := mustNew(t, tc.keywords)
			got := m.FindAll(tc.text)
			assertMatches(t, tc.text, tc.name, got, tc.want)
		})
	}
}

// ---------------------------------------------------------------------------
// 4. 空文本与无命中
// ---------------------------------------------------------------------------

func TestEmptyAndNoMatch(t *testing.T) {
	m := mustNew(t, []string{"中国", "北京"})
	if got := m.FindAll(""); got != nil {
		t.Errorf(`FindAll("") = %v, 期望 nil`, got)
	}
	if got := m.FindAll("完全无关文本"); got != nil || len(got) != 0 {
		t.Errorf(`FindAll("完全无关文本") = %v, 期望 nil（长度 0）`, got)
	}
}

// ---------------------------------------------------------------------------
// 5. 非法 UTF-8 文本
// ---------------------------------------------------------------------------

func TestInvalidUTF8(t *testing.T) {
	// "中"(E4B8AD) + 两个非法字节(E6 88，形似某三字节 rune 的残缺前缀) + 'x' + "国"(E59BBD)
	text := string([]byte{0xE4, 0xB8, 0xAD, 0xE6, 0x88, 'x', 0xE5, 0x9B, 0xBD})
	m := mustNew(t, []string{"中", "国", "中x"})
	// 能正常返回即说明未 panic；同时要求坏字节段不造成漏扫："国" 仍命中
	got := m.FindAll(text)
	assertMatches(t, text, "非法UTF-8文本", got, []Match{{0, 3, "中"}, {6, 9, "国"}})

	// 从坏字节内部起步的 FindNext 也不 panic，且对齐后结果正确
	mt, ok := m.FindNext(text, 4) // 偏移 4 落在续字节 0x88 上，需向后对齐到 rune 边界
	if !ok || mt != (Match{Start: 6, End: 9, Keyword: "国"}) {
		t.Errorf("FindNext(text,4) = (%+v, %v), 期望 ({Start:6 End:9 Keyword:国}, true)", mt, ok)
	}
}

// ---------------------------------------------------------------------------
// 6. FindNext 语义
// ---------------------------------------------------------------------------

func TestFindNext(t *testing.T) {
	m := mustNew(t, []string{"中国", "北京"})
	// "xx中国yy北京" 共 16 字节：中国=[2,8)，北京=[10,16)
	text := "xx中国yy北京"
	tests := []struct {
		offset int
		want   Match
		ok     bool
	}{
		{0, Match{2, 8, "中国"}, true},
		{8, Match{10, 16, "北京"}, true},
		{16, Match{}, false},           // offset == len(text)
		{17, Match{}, false},           // offset 越界
		{-5, Match{2, 8, "中国"}, true},  // 负数按 0 处理
		{3, Match{10, 16, "北京"}, true}, // 3 落在 "中" 的续字节上，对齐后继续扫描
	}
	for _, tc := range tests {
		got, ok := m.FindNext(text, tc.offset)
		if ok != tc.ok {
			t.Errorf("FindNext(text,%d) 的 ok = %v, 期望 %v", tc.offset, ok, tc.ok)
			continue
		}
		if got != tc.want {
			t.Errorf("FindNext(text,%d) = %+v, 期望 %+v", tc.offset, got, tc.want)
		}
		if ok && text[got.Start:got.End] != got.Keyword {
			t.Errorf("FindNext(text,%d): text[%d:%d] 与 Keyword 不一致", tc.offset, got.Start, got.End)
		}
	}

	// 无命中时返回零值与 false
	m2 := mustNew(t, []string{"中国"})
	if got, ok := m2.FindNext("abc", 0); ok || got != (Match{}) {
		t.Errorf(`FindNext("abc",0) = (%+v, %v), 期望 (Match{}, false)`, got, ok)
	}
}

// ---------------------------------------------------------------------------
// 7. FindNext 迭代与 FindAll 一致性（随机）
// ---------------------------------------------------------------------------

func TestFindNextIterateEqualsFindAll(t *testing.T) {
	rng := rand.New(rand.NewSource(20260828)) // 固定种子保证可复现
	pool := []string{"中", "中国", "中国人", "国", "人", "上海", "海口", "北京", "a", "ab"}
	for i := 0; i < 50; i++ {
		kws := drawKeywords(rng, pool, 2, 6)
		text := randomText(rng, kws, 10, 40)
		m := mustNew(t, kws)
		iter := findAllByFindNext(m, text)
		all := m.FindAll(text)
		if !reflect.DeepEqual(iter, all) {
			t.Fatalf("第 %d 组 FindNext 迭代与 FindAll 不一致\n词库: %q\n文本: %q\n迭代    (%d 条): %v\nFindAll (%d 条): %v",
				i, kws, text, len(iter), iter, len(all), all)
		}
	}
}

// ---------------------------------------------------------------------------
// 8. 随机对照朴素参照（最重要的正确性测试）
// ---------------------------------------------------------------------------

// naiveSearch 是朴素参照实现：用 strings.Index 暴力枚举出全部出现位置，
// 再按实现的 pending 语义做贪心筛选。它不依赖自动机与 BM 跳跃，
// 因此 FindAll 与其结果一致即说明 AC 状态转移与跳跃优化均无漏报/误报。
//
// 语义要点（与 search.go 中 scan 的规则逐条对应，二者等价正是被本测试验证的命题）：
//  1. 候选按「结束位置」升序到达；同一结束位置按关键词长度降序逐个尝试，
//     选第一个与 pending 兼容的候选（优先最长；最长者重叠则尝试更短者）；
//  2. 新候选与当前候选同一起始位置 → 替换为更长者（前缀关系取最长）；
//  3. 新候选起始位置不早于当前候选结束 → 提交当前候选，新候选接任；
//  4. 其余（与 pending 重叠）→ 跳过该候选、继续尝试更短者。
//
// 注：最初按直觉写成「start 升序、end 降序」的标准区间贪心（最左最长贪心），
// 随机对照发现与实现不一致——那是另一种同样自洽但不同的贪心语义。
// 实现采用的是「先命中（先结束）优先」语义，故参照改为按结束位置排序以
// 匹配实现的文档语义。
func naiveSearch(keywords []string, text string) []Match {
	type occ struct{ start, end int }
	// 枚举每个关键词的全部（可重叠）出现位置
	var occs []occ
	for _, kw := range keywords {
		for i := 0; ; {
			j := strings.Index(text[i:], kw)
			if j < 0 {
				break
			}
			s := i + j
			occs = append(occs, occ{s, s + len(kw)})
			i = s + 1
		}
	}
	// 按 end 升序、长度降序排序：与 scan 中「结束位置先后、同位置先长后短」的候选顺序一致
	sort.Slice(occs, func(a, b int) bool {
		if occs[a].end != occs[b].end {
			return occs[a].end < occs[b].end
		}
		return occs[a].end-occs[a].start > occs[b].end-occs[b].start
	})
	// 与 scan 的 pending 规则逐条对应地筛选
	var out []Match
	pend := occ{start: -1, end: -1}
	for _, o := range occs {
		switch {
		case pend.start < 0:
			pend = o
		case o.start == pend.start:
			pend.end = o.end // 同一起始位置：取最长
		case o.start >= pend.end:
			out = append(out, Match{pend.start, pend.end, text[pend.start:pend.end]})
			pend = o
		default:
			// 与 pending 重叠：先命中优先，跳过（继续尝试更短的候选）
		}
	}
	if pend.start >= 0 {
		out = append(out, Match{pend.start, pend.end, text[pend.start:pend.end]})
	}
	return out
}

func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20260828)) // 固定种子保证可复现
	pool := []string{"中", "中国", "中国人", "国", "人", "上海", "海口", "北京", "a", "ab", "口", "大", "海", "上"}
	for i := 0; i < 500; i++ {
		kws := drawKeywords(rng, pool, 2, 8)
		text := randomText(rng, kws, 20, 80)
		m := mustNew(t, kws)
		got := m.FindAll(text)
		want := naiveSearch(kws, text)
		kwSet := make(map[string]struct{}, len(kws))
		for _, kw := range kws {
			kwSet[kw] = struct{}{}
		}
		if len(got) != len(want) {
			t.Fatalf("第 %d 组命中数不一致\n词库: %q\n文本: %q\nFindAll (%d 条): %v\nnaive   (%d 条): %v",
				i, kws, text, len(got), got, len(want), want)
		}
		for j := range got {
			if got[j].Start != want[j].Start || got[j].End != want[j].End || got[j].Keyword != want[j].Keyword {
				t.Fatalf("第 %d 组第 %d 条命中不一致\n词库: %q\n文本: %q\nFindAll: %v\nnaive  : %v",
					i, j, kws, text, got, want)
			}
			// 命中切片必须与 Keyword 逐字节一致，且 Keyword 必须来自词库
			if text[got[j].Start:got[j].End] != got[j].Keyword {
				t.Fatalf("第 %d 组第 %d 条: text[%d:%d] 与 Keyword %q 不一致",
					i, j, got[j].Start, got[j].End, got[j].Keyword)
			}
			if _, ok := kwSet[got[j].Keyword]; !ok {
				t.Fatalf("第 %d 组第 %d 条: 命中词 %q 不在词库中", i, j, got[j].Keyword)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 9. 并发安全（供 -race 检测）
// ---------------------------------------------------------------------------

func TestConcurrent(t *testing.T) {
	keywords := []string{
		"上海", "北京", "天津", "重庆", "广州", "深圳", "杭州", "苏州", "南京", "成都",
		"武汉", "西安", "中国", "中华", "人民", "共和国", "长江", "黄河", "长城", "故宫",
	}
	m := mustNew(t, keywords) // 只构建一次
	text := "中国人民共和国长江黄河上海北京故宫长城武汉广州深圳杭州苏州南京成都重庆天津西安中华共和国人民"

	wantAll := m.FindAll(text)
	if len(wantAll) == 0 {
		t.Fatal("预计算 FindAll 无命中，测试文本需包含词库关键词")
	}
	type nextResult struct {
		m  Match
		ok bool
	}
	wantNext := make([]nextResult, len(text)+1)
	for off := range wantNext {
		mt, ok := m.FindNext(text, off)
		wantNext[off] = nextResult{mt, ok}
	}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if got := m.FindAll(text); !reflect.DeepEqual(got, wantAll) {
					t.Errorf("goroutine %d 第 %d 次 FindAll 与预计算结果不一致", g, i)
				}
				off := (g*37 + i*13) % (len(text) + 1)
				mt, ok := m.FindNext(text, off)
				if ok != wantNext[off].ok || mt != wantNext[off].m {
					t.Errorf("goroutine %d 第 %d 次 FindNext(text,%d) 与预计算结果不一致", g, i, off)
				}
			}
		}(g)
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// 10. ASCII 段跳跃不漏报
// ---------------------------------------------------------------------------

func TestASCIISkipCorrectness(t *testing.T) {
	m := mustNew(t, []string{"上海", "北京"})
	// 前缀均为 ASCII：上海 = [20,26)，北京 = [32,38)
	text := "Hello, world! 12345 上海 test 北京 done"
	got := m.FindAll(text)
	assertMatches(t, text, "ASCII 混排文本", got, []Match{
		{Start: 20, End: 26, Keyword: "上海"},
		{Start: 32, End: 38, Keyword: "北京"},
	})
}
