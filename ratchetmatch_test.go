package ratchetmatch

import (
	"math/rand"
	"reflect"
	"slices"
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
func mustNew(t *testing.T, keywords []string) Matcher {
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
func findAllByFindNext(m Matcher, text string) []Match {
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

	// 非法 UTF-8 与含 U+FFFD 的关键词拒绝语义见 TestNewRejectsInvalidKeywords

	// 正常构建（含前缀关系词），并做白盒验证：
	// 每个关键词从 root 独立插入，故 "世界"(root→世→界) 与 "你好世界"
	// 中的 "世"(你→好→世) 是不同节点：root + 你/你好/好世/好世界/世/界 共 7 个；
	// root 首字符表恰为 {你,世} 2 个（词首字符去重）；root 不进 CSR（走 map）。
	m := mustNew(t, []string{"你好", "世界", "你好世界"})
	em := m.(*exactMatcher)
	if len(em.nodes) != 7 {
		t.Errorf("白盒: trie 节点数 = %d, 期望 7", len(em.nodes))
	}
	if len(em.rootNext) != 2 {
		t.Errorf("白盒: root 首字符表大小 = %d, 期望 2", len(em.rootNext))
	}
}

// ---------------------------------------------------------------------------
// 1b. CSR 展平不变量（spec「转移表内存布局」）
// ---------------------------------------------------------------------------

// TestCSRLayout 白盒验证展平结果的结构不变量：
//   - root 不进 CSR；transKeys/transVals 等长，非 root 节点区间落在数组范围内；
//   - 每个节点区间内键严格升序（查找的正确性前提）；
//   - 转移目标均为合法节点下标且非 0（0 保留给「未含 → 回退/留在 root」）；
//   - 失败指针指向合法节点且严格更浅（无环，回退必然终止）；
//   - root 首字符表与各关键词首 rune 集一致（跳跃判据正确性前提）。
func TestCSRLayout(t *testing.T) {
	m := mustNew(t, benchKeywordsSparse)
	em := m.(*exactMatcher)
	if len(em.transKeys) != len(em.transVals) {
		t.Fatalf("transKeys(%d) 与 transVals(%d) 长度不一致", len(em.transKeys), len(em.transVals))
	}
	if em.nodes[0].base != 0 || em.nodes[0].count != 0 {
		t.Fatalf("root 不应有 CSR 区间: base=%d count=%d", em.nodes[0].base, em.nodes[0].count)
	}
	for i := 1; i < len(em.nodes); i++ {
		nd := &em.nodes[i]
		if nd.count < 0 || nd.base < 0 || int(nd.base+nd.count) > len(em.transKeys) {
			t.Fatalf("节点 %d 区间非法: base=%d count=%d（总长 %d）", i, nd.base, nd.count, len(em.transKeys))
		}
		for j := nd.base + 1; j < nd.base+nd.count; j++ {
			if em.transKeys[j-1] >= em.transKeys[j] {
				t.Fatalf("节点 %d 区间键非严格升序: keys[%d]=%U >= keys[%d]=%U", i, j-1, em.transKeys[j-1], j, em.transKeys[j])
			}
		}
		for j := nd.base; j < nd.base+nd.count; j++ {
			if to := em.transVals[j]; to <= 0 || int(to) >= len(em.nodes) {
				t.Fatalf("节点 %d 转移目标非法: vals[%d]=%d（节点总数 %d）", i, j, to, len(em.nodes))
			}
		}
		if nd.fail < 0 || int(nd.fail) >= len(em.nodes) {
			t.Fatalf("节点 %d 失败指针非法: fail=%d", i, nd.fail)
		}
	}
	// root 首字符表与词库首 rune 集一致
	firsts := make(map[rune]struct{})
	for _, kw := range benchKeywordsSparse {
		for _, r := range kw {
			firsts[r] = struct{}{}
			break
		}
	}
	if len(em.rootNext) != len(firsts) {
		t.Fatalf("root 首字符表大小 %d 与词库首字符集 %d 不一致", len(em.rootNext), len(firsts))
	}
	for r := range firsts {
		if _, ok := em.rootNext[r]; !ok {
			t.Fatalf("root 首字符表缺少词首字符 %U", r)
		}
	}
	// outLens 严格降序不变量（同状态多关键词时链规则「同起点取更长」的前提）
	for i := range em.nodes {
		ls := em.nodes[i].outLens
		for j := 1; j < len(ls); j++ {
			if ls[j-1] <= ls[j] {
				t.Fatalf("节点 %d outLens 非严格降序: [%d]=%d <= [%d]=%d", i, j-1, ls[j-1], j, ls[j])
			}
		}
	}
}

// TestAutomatonSemantics 白盒重推导验证自动机语义：以 DFS 还原每个节点的
// 路径字符串后逐节点核对——
//   - fail 恰指向「路径的最长真后缀（且是某关键词前缀）」对应的节点；
//   - outLens 恰为「路径的全部关键词后缀字节长度」降序集合（含失败链继承）；
//   - byteFilter 与词库首字节集逐位一致（跳跃安全性前提，rootNext 校验首 rune，
//     过滤器校验首字节，二者共同保证跳跃判据无漏）。
func TestAutomatonSemantics(t *testing.T) {
	kws := benchKeywordsSparse
	m := mustNew(t, kws)
	em := m.(*exactMatcher)

	// DFS 还原每个节点的路径字符串（root 为空串，不入表）
	paths := make([]string, len(em.nodes))
	type frame struct {
		node int32
		path string
	}
	stack := make([]frame, 0, len(em.nodes))
	for r, c := range em.rootNext {
		stack = append(stack, frame{c, string(r)})
	}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		paths[f.node] = f.path
		nd := &em.nodes[f.node]
		for j := nd.base; j < nd.base+nd.count; j++ {
			stack = append(stack, frame{em.transVals[j], f.path + string(em.transKeys[j])})
		}
	}
	// 前缀 → 节点下标（Trie 是树，路径与节点一一对应；root 空串不参与后缀匹配）
	nodeOf := make(map[string]int32, len(paths))
	for i := 1; i < len(paths); i++ {
		if paths[i] == "" {
			t.Fatalf("节点 %d 不可从 root 到达", i)
		}
		if _, dup := nodeOf[paths[i]]; dup {
			t.Fatalf("节点 %d 与前节点路径重复: %q", i, paths[i])
		}
		nodeOf[paths[i]] = int32(i)
	}

	// fail 指针：最长真后缀（且是词库前缀）
	for i := 1; i < len(em.nodes); i++ {
		rs := []rune(paths[i])
		want := int32(0)
		for k := len(rs) - 1; k > 0; k-- {
			if n, ok := nodeOf[string(rs[k:])]; ok {
				want = n
				break
			}
		}
		if em.nodes[i].fail != want {
			t.Fatalf("节点 %d（%q）fail = %d，期望最长真后缀前缀节点 %d",
				i, paths[i], em.nodes[i].fail, want)
		}
	}

	// outLens：路径的关键词后缀长度集合，降序
	for i := 1; i < len(em.nodes); i++ {
		var want []int32
		for _, kw := range kws {
			if strings.HasSuffix(paths[i], kw) {
				want = append(want, int32(len(kw)))
			}
		}
		slices.Sort(want)
		slices.Reverse(want) // 降序
		if !slices.Equal(em.nodes[i].outLens, want) {
			t.Fatalf("节点 %d（%q）outLens = %v，期望关键词后缀长度降序 %v",
				i, paths[i], em.nodes[i].outLens, want)
		}
	}

	// byteFilter：与词库首字节集逐位一致
	firstBytes := make(map[byte]struct{}, len(kws))
	for _, kw := range kws {
		firstBytes[kw[0]] = struct{}{}
	}
	for b := range 256 {
		set := em.byteFilter[b>>3]&(1<<(b&7)) != 0
		_, want := firstBytes[byte(b)]
		if set != want {
			t.Fatalf("字节 0x%02X 过滤器位 = %v，期望 %v", b, set, want)
		}
	}
}

// TestFindBinaryBranch 白盒验证 find 的二分分支：构造 >16 个孩子的节点
// （公共前缀 P + 17 个不同次字符），确认转移正确——该分支此前从未被用例触发。
func TestFindBinaryBranch(t *testing.T) {
	prefix := "P"
	suffixes := []rune("ABCDEFGHIJKLMNOPQ") // 17 个 → P 节点 count=17 > 16
	kws := make([]string, 0, len(suffixes))
	for _, s := range suffixes {
		kws = append(kws, prefix+string(s))
	}
	m := mustNew(t, kws)
	em := m.(*exactMatcher)
	// 前置白盒断言：P 节点确实宽于 16，扫描将走二分分支
	pNode := int(em.rootNext['P'])
	if len(em.rootNext) != 1 || em.nodes[pNode].count <= 16 {
		t.Fatalf("前置条件不满足：root 首字符 %d 个，P 节点 count=%d（需 >16）",
			len(em.rootNext), em.nodes[pNode].count)
	}
	// 每个词独立命中
	for _, s := range suffixes {
		kw := prefix + string(s)
		got := m.FindAll(kw)
		if len(got) != 1 || got[0].Keyword != kw {
			t.Errorf("FindAll(%q) = %v, 期望 1 条 %q", kw, got, kw)
		}
	}
	// 混排文本一次扫描全部命中（逐词转移均过二分分支）
	var b strings.Builder
	for _, s := range suffixes {
		b.WriteString(prefix)
		b.WriteRune(s)
		b.WriteByte(',')
	}
	if got := m.FindAll(b.String()); len(got) != len(suffixes) {
		t.Errorf("混排扫描命中 %d 条, 期望 %d 条", len(got), len(suffixes))
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
// 2b. 黑盒边界与极端场景
// ---------------------------------------------------------------------------

// TestEdgeCases 覆盖此前缺口的黑盒场景：自重叠词、词长于文本、单字节词
// 逐字符全命中、词库含 Emoji（4 字节 rune）、重复输入词去重。
func TestEdgeCases(t *testing.T) {
	t.Run("自重叠关键词", func(t *testing.T) {
		// "AA" 在 "AAAA" 中自重叠两次：最左最长取 [0,2)，随后 [2,4) 再取
		m := mustNew(t, []string{"AA"})
		assertMatches(t, "AAAA", "自重叠", m.FindAll("AAAA"),
			[]Match{{0, 2, "AA"}, {2, 4, "AA"}})
		// "ABA" 在 "ABABA"：[0,3) 与 [2,5) 重叠，最左最长取 [0,3)，余下 "BA" 无命中
		m2 := mustNew(t, []string{"ABA"})
		assertMatches(t, "ABABA", "自重叠2", m2.FindAll("ABABA"),
			[]Match{{0, 3, "ABA"}})
	})
	t.Run("关键词长于文本", func(t *testing.T) {
		// 长词 "人工智能" 未完整出现；短词 "人" 在 [0,3) 完整出现 → 正常命中
		m := mustNew(t, []string{"人工智能", "人"})
		assertMatches(t, "人工", "长词截断", m.FindAll("人工"),
			[]Match{{0, 3, "人"}})
		// 词库只有长词时，截断文本无命中
		m2 := mustNew(t, []string{"人工智能"})
		if got := m2.FindAll("人工"); got != nil {
			t.Errorf(`FindAll("人工") = %v, 期望 nil`, got)
		}
	})
	t.Run("单字节词逐字符全命中", func(t *testing.T) {
		m := mustNew(t, []string{"a"})
		assertMatches(t, "aaaa", "单字节", m.FindAll("aaaa"),
			[]Match{{0, 1, "a"}, {1, 2, "a"}, {2, 3, "a"}, {3, 4, "a"}})
	})
	t.Run("Emoji 四字节 rune", func(t *testing.T) {
		// 😀 = F0 9F 98 80（4 字节）：a[0,1) 笑[1,4) 😀[4,8) b[8,9)
		m := mustNew(t, []string{"😀", "笑😀"})
		assertMatches(t, "a笑😀b", "emoji", m.FindAll("a笑😀b"),
			[]Match{{1, 8, "笑😀"}})
		// 单 Emoji 词库：😀 在文本中单独出现
		m2 := mustNew(t, []string{"😀"})
		assertMatches(t, "x😀y", "emoji2", m2.FindAll("x😀y"),
			[]Match{{1, 5, "😀"}})
	})
	t.Run("重复输入词去重不重复输出", func(t *testing.T) {
		m := mustNew(t, []string{"中国", "中国", "中国"})
		assertMatches(t, "中国中国", "去重", m.FindAll("中国中国"),
			[]Match{{0, 6, "中国"}, {6, 12, "中国"}})
	})
	t.Run("关键词即整个文本", func(t *testing.T) {
		m := mustNew(t, []string{"中国人"})
		assertMatches(t, "中国人", "整文本", m.FindAll("中国人"),
			[]Match{{0, 9, "中国人"}})
	})
}

// TestNewRejectsInvalidKeywords 验证词库校验拒绝非法 UTF-8 与 U+FFFD 关键词
// （fuzz 发现的两类 rune 歧义：非法字节身份坍缩 / U+FFFD 长度不一致）。
func TestNewRejectsInvalidKeywords(t *testing.T) {
	for _, tc := range []struct {
		name string
		kws  []string
		want string // 错误信息应包含的片段
	}{
		{"单非法字节", []string{string([]byte{0xB8})}, "not valid UTF-8"},
		{"词中混非法字节", []string{"a\xffb"}, "not valid UTF-8"},
		{"残缺多字节序列", []string{string([]byte{0xE6, 0x88})}, "not valid UTF-8"},
		{"含 U+FFFD", []string{"a\uFFFD"}, "U+FFFD"},
		{"仅 U+FFFD", []string{"\uFFFD"}, "U+FFFD"},
	} {
		m, err := New(tc.kws)
		if err == nil || m != nil {
			t.Errorf("%s: 期望报错，实际 err=%v m=%v", tc.name, err, m)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: 错误信息应含 %q，实际: %q", tc.name, tc.want, err.Error())
		}
	}
	// 对照：非法字节只出现在文本侧时行为健全（每个坏字节按 RuneError 前进 1 字节）
	m := mustNew(t, []string{"中", "国"})
	text := string([]byte{0xB8, 0xAD, 0xE4, 0xB8, 0xAD, 0xE5, 0x9B, 0xBD}) // 坏×2 + 中 + 国
	assertMatches(t, text, "非法文本不漏扫", m.FindAll(text), []Match{{2, 5, "中"}, {5, 8, "国"}})
}

// ---------------------------------------------------------------------------
// 3. 非重叠贪心语义
// ---------------------------------------------------------------------------

// TestFindNextEdgeConsistency 复现 2026-08-31 fuzz 发现的缺陷：FindNext 迭代
// 与 FindAll 在「词宽 >1 且互为周期倍数」的边界分歧（kws {0,000} 文本 "000…1"），
// 见 tasks.md 实现期修正记录。
func TestFindNextEdgeConsistency(t *testing.T) {
	cases := []struct {
		kws  []string
		text string
	}{
		{[]string{"0", "000"}, "000000000001"},  // 9 个 '0' + 终止噪声
		{[]string{"ab", "abab"}, "abababababZ"}, // 词宽 2 与 4，周期 2
		{[]string{"ab", "abab"}, "ababababZ"},   // 4 个 'ab'（8 字节）+ 噪声
		{[]string{"0", "000"}, "00000000"},      // 无噪声纯重复
		{[]string{"ab", "abab"}, "abababZ"},     // 奇数个 'ab' 段
	}
	for _, tc := range cases {
		m := mustNew(t, tc.kws)
		got := findAllByFindNext(m, tc.text)
		want := m.FindAll(tc.text)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("词库 %q 文本 %q\n迭代   (%d 条): %v\nFindAll(%d 条): %v",
				tc.kws, tc.text, len(got), got, len(want), want)
		}
	}
}

// TestGreedySemantics 验证非重叠最左最长语义（leftmost-longest）。
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
		{"重叠取更左", []string{"上海", "海口"}, "上海口", []Match{{0, 6, "上海"}}},
		{"同结尾取更长", []string{"他", "其他"}, "其他", []Match{{0, 6, "其他"}}},
		// 上海=[0,6) 人=[6,9) 北京=[9,15)
		{"不重叠全输出", []string{"上海", "北京"}, "上海人北京", []Match{{0, 6, "上海"}, {9, 15, "北京"}}},
		{"嵌套前缀链", []string{"a", "ab", "abc"}, "xabcx", []Match{{1, 4, "abc"}}},
		{"连续同词", []string{"a", "ab"}, "abab", []Match{{0, 2, "ab"}, {2, 4, "ab"}}},
		// 真包含关系一律取最长："中国人"(0,9) 起点最左，遮蔽 "国"(3,6) 与 "人"(6,9)
		{"真包含取最长", []string{"国", "人", "中国人"}, "中国人", []Match{{0, 9, "中国人"}}},
		// 断词结算："梦" 与 "人" 不匹配 → "中国人" 断词，fail 规则结算 "国"(3,6)
		{"断词后fail结算短词", []string{"国", "人", "中国人"}, "中国梦", []Match{{3, 6, "国"}}},
		// 更左候选逐级弹出链尾：a(start=2)→被 ba(1) 弹出→被 cba(0) 弹出
		{"逐级弹出至最左", []string{"a", "ba", "cba"}, "cba", []Match{{0, 3, "cba"}}},
		// 长词断词后，其内部短词按最左最长独立结算："中国人" 断于 "梦"，
		// [0,9) "中国人" 已入链；随后 root 重新扫描，无更左候选 → 输出 "中国人"
		{
			"断词后短词独立结算",
			[]string{"国", "人", "中国人"},
			"中国人梦国人",
			[]Match{{0, 9, "中国人"}, {12, 15, "国"}, {15, 18, "人"}},
		},
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
// 3b. 重叠全量返回（FindAllOverlapping）
// ---------------------------------------------------------------------------

// TestFindAllOverlapping 验证重叠全量语义与输出顺序（End 升序、同 End 长度降序）。
func TestFindAllOverlapping(t *testing.T) {
	tests := []struct {
		name     string
		keywords []string
		text     string
		want     []Match
	}{
		// 以 pos=6 结束：国(3,6)；以 pos=9 结束：中国人(0,9) 长 9 先于 人(6,9) 长 3
		{
			"包含关系全量保留",
			[]string{"国", "人", "中国人"},
			"中国人",
			[]Match{{3, 6, "国"}, {0, 9, "中国人"}, {6, 9, "人"}},
		},
		// 上海(0,6)、海口(3,9) 重叠邻居均保留（FindAll 仅返回 上海）
		{
			"重叠邻居均保留",
			[]string{"上海", "海口"},
			"上海口",
			[]Match{{0, 6, "上海"}, {3, 9, "海口"}},
		},
		{"无命中返回 nil", []string{"中国"}, "abc", nil},
		{"空文本返回 nil", []string{"中国"}, "", nil},
		// x=[0,1)：嵌套前缀 a(0,1)、ab(0,2)、abc(0,3) 同 End=3 依长度降序
		{
			"嵌套前缀同End降序",
			[]string{"a", "ab", "abc"},
			"xabc",
			[]Match{{1, 2, "a"}, {1, 3, "ab"}, {1, 4, "abc"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := mustNew(t, tc.keywords)
			got := m.FindAllOverlapping(tc.text)
			assertMatches(t, tc.text, tc.name, got, tc.want)
		})
	}
}

// TestFindAllOverlappingRandom 随机对照：全量出现集合与逐词 strings.Count 枚举一致，
// 输出顺序满足 End 升序、同 End 长度降序。
func TestFindAllOverlappingRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(20260830))
	pool := []string{"中", "中国", "中国人", "国", "人", "上海", "海口", "北京", "a", "ab", "口", "大", "海", "上"}
	for i := range 500 {
		kws := drawKeywords(rng, pool, 2, 8)
		text := randomText(rng, kws, 20, 80)
		m := mustNew(t, kws)
		got := m.FindAllOverlapping(text)
		// 期望集：逐词枚举全部出现（strings.Count 风格），按 End 升序、长度降序排序
		type occ struct{ start, end int }
		var want []occ
		for _, kw := range kws {
			for j := 0; ; {
				k := strings.Index(text[j:], kw)
				if k < 0 {
					break
				}
				s := j + k
				want = append(want, occ{s, s + len(kw)})
				j = s + 1
			}
		}
		sort.Slice(want, func(a, b int) bool {
			if want[a].end != want[b].end {
				return want[a].end < want[b].end
			}
			return want[a].end-want[a].start > want[b].end-want[b].start
		})
		if len(got) != len(want) {
			t.Fatalf("第 %d 组出现数不符\n词库: %q\n文本: %q\ngot (%d): %v\nwant (%d): %v",
				i, kws, text, len(got), got, len(want), want)
		}
		for j := range want {
			if got[j].Start != want[j].start || got[j].End != want[j].end {
				t.Fatalf("第 %d 组第 %d 条不符\ngot: %+v want: %+v\n文本: %q",
					i, j, got[j], want[j], text)
			}
			if text[got[j].Start:got[j].End] != got[j].Keyword {
				t.Fatalf("第 %d 组第 %d 条切片与关键词不一致", i, j)
			}
		}
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
	for i := range 50 {
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

// naiveSearch 是朴素参照实现：教科书式最左最长——每轮从 pos 起在全部关键词中
// 找「起点最小的出现，同起点取最长」，提交后从其 End 继续。它不依赖自动机、
// 失败指针、跳跃与待提交链，因此 FindAll 与其结果一致即说明实现无漏报/误报、
// 语义符合最左最长（含无空档：被提交命中之间不存在可再匹配的未覆盖区间）。
func naiveSearch(keywords []string, text string) []Match {
	var out []Match
	for pos := 0; pos < len(text); {
		bestStart, bestEnd := -1, -1
		for _, kw := range keywords {
			j := strings.Index(text[pos:], kw)
			if j < 0 {
				continue
			}
			s, e := pos+j, pos+j+len(kw)
			if bestStart < 0 || s < bestStart || (s == bestStart && e > bestEnd) {
				bestStart, bestEnd = s, e
			}
		}
		if bestStart < 0 {
			break
		}
		out = append(out, Match{bestStart, bestEnd, text[bestStart:bestEnd]})
		pos = bestEnd
	}
	return out
}

func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20260828)) // 固定种子保证可复现
	pool := []string{"中", "中国", "中国人", "国", "人", "上海", "海口", "北京", "a", "ab", "口", "大", "海", "上"}
	for i := range 500 {
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
	for g := range 8 {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range 100 {
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
