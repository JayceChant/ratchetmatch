// 本文件为大小写折叠匹配的专项测试（package ratchetmatch）：
//   - 折叠轨道工具的白盒校验；
//   - 语义表用例（合一、宽度差、无展开式折叠、中文不受影响）与模式判别；
//   - 随机对照：fold 三 API 与「逐位置 strings.EqualFold 枚举」oracle 一致；
//   - 两套实现（exact / fold）并发查询的稳定性（数据竞争由 -race 检出）。
package ratchetmatch

import (
	"math/rand"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// mustNewFold 构建折叠模式 Matcher，意外失败时立即终止当前测试。
func mustNewFold(t *testing.T, keywords []string) Matcher {
	t.Helper()
	m, err := New(keywords, WithCaseFold())
	if err != nil {
		t.Fatalf("New(%q, WithCaseFold) 意外失败: %v", keywords, err)
	}
	if !m.CaseFold() {
		t.Fatalf("New(WithCaseFold) 应返回折叠模式: %T", m)
	}
	return m
}

// ---------------------------------------------------------------------------
// 白盒：折叠轨道工具
// ---------------------------------------------------------------------------

func TestFoldKeyOrbit(t *testing.T) {
	// ASCII 单成员轨道外的典型轨道：2 成员 / 3 成员 / 含兼容字符
	cases := []struct {
		rs    []rune // 同一折叠轨道的全部成员（人工确认）
		width int    // 成员数
	}{
		{[]rune{'K', 'k', '\u212A'}, 3}, // 开尔文度与拉丁 K 同轨道
		{[]rune{'Σ', 'σ', 'ς'}, 3},      // 希腊终格 σ
		{[]rune{'ß', '\u1E9E'}, 2},      // ß 与大写 ẞ（无 s-s 展开）
		{[]rune{'ü', 'Ü'}, 2},           // 变音字母
		{[]rune{'ⅸ', 'Ⅸ'}, 2},           // 罗马数字
		{[]rune{'世'}, 1},                // 中文单成员
	}
	for _, tc := range cases {
		key := foldKey(tc.rs[0])
		for _, r := range tc.rs {
			if got := foldKey(r); got != key {
				t.Errorf("foldKey(%q) = %q, 与成员 %q 的代表 %q 不一致", r, got, tc.rs[0], key)
			}
		}
		orbit := foldOrbit(key)
		if len(orbit) != tc.width {
			t.Errorf("foldOrbit(%q) 成员数 = %d, 期望 %d: %q", key, len(orbit), tc.width, orbit)
		}
		for _, r := range tc.rs {
			if !slices.Contains(orbit, r) {
				t.Errorf("foldOrbit(%q) 缺少成员 %q", key, r)
			}
		}
		// 轨道成员逐一折叠等价（foldKey 同一即 SimpleFold 同轨道）
		for _, a := range tc.rs {
			for _, b := range tc.rs {
				if foldKey(a) != foldKey(b) {
					t.Errorf("foldKey(%q) != foldKey(%q), 同轨道应相等", a, b)
				}
			}
		}
	}
	if foldKey('a') == foldKey('b') || foldKey('a') != foldKey('A') {
		t.Error("foldKey 基础语义错误")
	}
}

// ---------------------------------------------------------------------------
// 语义表用例
// ---------------------------------------------------------------------------

func TestCaseFoldSemantics(t *testing.T) {
	// 希腊语用例用显式 rune 构造，避免手写字符串的码点身份混淆：
	// σ（U+03C3）与终格 ς（U+03C2）同折叠轨道，大写 Σ（U+03A3）亦然。
	grLower := string([]rune{'κ', 'ο', 'σ', 'μ', 'ο', 'σ'}) // 12 字节
	grUpper := string([]rune{'Κ', 'Ο', 'Σ', 'Μ', 'Ο', 'Σ'}) // 12 字节
	grFinal := string([]rune{'κ', 'ο', 'ς', 'μ', 'ο', 'ς'}) // 12 字节
	tests := []struct {
		name      string
		keywords  []string
		text      string
		wantFold  []Match // 折叠 Matcher 的 FindAll
		wantExact []Match // 精确 Matcher 的 FindAll
	}{
		// 组号说明：fold 模式词身份为折叠归一形（去重词库下标），exact 为
		// 原词库下标——两套 Matcher 的组号空间彼此独立。
		{
			name:      "ASCII 大小写",
			keywords:  []string{"hello", "世界"},
			text:      "Hello, WORLD! 世界",
			wantFold:  []Match{{Start: 0, End: 5, Keyword: "Hello"}, {Start: 14, End: 20, Keyword: "世界", Group: 1}},
			wantExact: []Match{{Start: 14, End: 20, Keyword: "世界", Group: 1}},
		},
		{
			name:      "词库大小写变体合一不漏报",
			keywords:  []string{"Stop", "stop"},
			text:      "SToP sTop",
			wantFold:  []Match{{Start: 0, End: 4, Keyword: "SToP"}, {Start: 5, End: 9, Keyword: "sTop"}}, // 归一形 stop 同组 0
			wantExact: []Match{},
		},
		{
			name:      "折叠变体关键词只出一条（outRunes 去重）",
			keywords:  []string{"ss", "sS", "Ss", "SS"},
			text:      "Ss",
			wantFold:  []Match{{Start: 0, End: 2, Keyword: "Ss"}}, // 归一形 ss 单词库项，组 0
			wantExact: []Match{{Start: 0, End: 2, Keyword: "Ss", Group: 2}},
		},
		{
			name:      "开尔文度宽度差按文本侧提取",
			keywords:  []string{"\u212A"},
			text:      "k \u212A",
			wantFold:  []Match{{Start: 0, End: 1, Keyword: "k"}, {Start: 2, End: 5, Keyword: "\u212A"}},
			wantExact: []Match{{Start: 2, End: 5, Keyword: "\u212A"}},
		},
		{
			name:      "前缀包含折叠取最长",
			keywords:  []string{"中国", "zhongguo", "ZhongGuo"},
			text:      "ZHONGGUO中国",
			wantFold:  []Match{{Start: 0, End: 8, Keyword: "ZHONGGUO", Group: 1}, {Start: 8, End: 14, Keyword: "中国"}}, // 归一形 [中国, zhongguo]
			wantExact: []Match{{Start: 8, End: 14, Keyword: "中国"}},                                                    // 原词库 [中国, zhongguo, ZhongGuo]
		},
		{
			name:      "无展开式折叠：ß 不匹配 ss",
			keywords:  []string{"straße"},
			text:      "STRASSE straße",
			wantFold:  []Match{{Start: 8, End: 15, Keyword: "straße"}},
			wantExact: []Match{{Start: 8, End: 15, Keyword: "straße"}},
		},
		{
			name:      "希腊三成员轨道含终格 ς",
			keywords:  []string{grLower},
			text:      grUpper + " " + grLower + " " + grFinal,
			wantFold:  []Match{{Start: 0, End: 12, Keyword: grUpper}, {Start: 13, End: 25, Keyword: grLower}, {Start: 26, End: 38, Keyword: grFinal}},
			wantExact: []Match{{Start: 13, End: 25, Keyword: grLower}}, // 精确模式：grFinal ≠ grLower 不命中
		},
		{
			name:      "中文不受折叠影响",
			keywords:  []string{"上海"},
			text:      "上海",
			wantFold:  []Match{{Start: 0, End: 6, Keyword: "上海"}},
			wantExact: []Match{{Start: 0, End: 6, Keyword: "上海"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fm := mustNewFold(t, tc.keywords)
			if !fm.CaseFold() {
				t.Fatal("折叠 Matcher 的 CaseFold 应为 true")
			}
			got := fm.FindAll(tc.text)
			assertMatches(t, tc.text, "fold", got, tc.wantFold)

			em := mustNew(t, tc.keywords)
			if em.CaseFold() {
				t.Fatal("精确 Matcher 的 CaseFold 应为 false")
			}
			gotExact := em.FindAll(tc.text)
			assertMatches(t, tc.text, "exact", gotExact, tc.wantExact)
		})
	}
}

// ---------------------------------------------------------------------------
// 随机 oracle 对照
// ---------------------------------------------------------------------------

// foldOracleAll 枚举 text 中全部折叠匹配出现（每个 rune 对齐的 (起点, 关键词)
// 组合一一对照 strings.EqualFold），同区间去重后按 End 升序、同 End 关键词
// 长度降序输出——即 FindAllOverlapping（折叠）的朴素 oracle。
func foldOracleAll(kws []string, text string) []Match {
	// 关键词按 rune 数分桶，逐结束位置只检查等 rune 数的关键词
	byLen := make(map[int][][]rune)
	seen := make(map[string]struct{}, len(kws))
	for _, kw := range kws {
		if _, ok := seen[kw]; ok {
			continue
		}
		seen[kw] = struct{}{}
		rs := []rune(kw)
		byLen[len(rs)] = append(byLen[len(rs)], rs)
	}
	var pos []int
	for i := range text {
		pos = append(pos, i)
	}
	pos = append(pos, len(text))
	n := len(pos) - 1
	// 词身份组号表：折叠归一形 → 去重词库下标（与 resolveSynonyms 无同义词
	// 分区的编号一致；Match.Group 的语义锚点由语义表用例手工固定）。
	groupOf := make(map[string]int32, len(kws))
	seenGrp := make(map[string]struct{}, len(kws))
	gi := 0
	for _, kw := range kws {
		id := foldNorm(kw)
		if _, dup := seenGrp[id]; dup {
			continue
		}
		seenGrp[id] = struct{}{}
		groupOf[id] = int32(gi)
		gi++
	}
	outSet := make(map[Match]struct{})
	var out []Match
	for end := 1; end <= n; end++ {
		for l, bucket := range byLen {
			if end < l {
				continue
			}
			start := end - l
			sub := text[pos[start]:pos[end]]
			for _, kr := range bucket {
				if strings.EqualFold(sub, string(kr)) {
					mt := Match{Start: pos[start], End: pos[end], Keyword: sub, Group: int(groupOf[foldNorm(sub)])}
					if _, dup := outSet[mt]; !dup {
						outSet[mt] = struct{}{}
						out = append(out, mt)
					}
				}
			}
		}
	}
	slices.SortFunc(out, func(a, b Match) int {
		if a.End != b.End {
			return a.End - b.End
		}
		return len(b.Keyword) - len(a.Keyword)
	})
	return out
}

// greedyLeftmostLongest 从全量出现（按 Start 升序、长度降序预排序）贪心
// 选择非重叠最左最长序列——FindAll（折叠）的朴素 oracle。
func greedyLeftmostLongest(ovl []Match) []Match {
	sorted := slices.Clone(ovl)
	slices.SortFunc(sorted, func(a, b Match) int {
		if a.Start != b.Start {
			return a.Start - b.Start
		}
		return len(b.Keyword) - len(a.Keyword)
	})
	var out []Match
	last := 0
	for _, mt := range sorted {
		if mt.Start >= last {
			out = append(out, mt)
			last = mt.End
		}
	}
	return out
}

// foldPool 随机对照词池：覆盖 ASCII、含变体词、宽度差、希腊轨道、ß、
// 全角、罗马数字与中文。
var foldPool = []string{
	"stop", "STOP", "Stop", "sToP",
	"ss", "sS",
	"straße", "STRASSE",
	"Tür", "tur",
	"ΚΟΣΜΟΣ", "κόσμος",
	"abc", "ＡＢＣ",
	"ⅸ", "Ⅸ",
	"K", "\u212A",
	"世界", "中国人", "中国",
	"world",
}

// foldMangle 把 rune 随机替换为其折叠轨道的另一成员（保持 fold 等价）。
func foldMangle(rng *rand.Rand, s string) string {
	var b strings.Builder
	for _, r := range s {
		if rng.Intn(2) == 0 {
			b.WriteRune(r)
			continue
		}
		o := foldOrbit(foldKey(r))
		b.WriteRune(o[rng.Intn(len(o))])
	}
	return b.String()
}

// TestCaseFoldRandomOracle 随机词库 × 随机折叠文本：
//   - 折叠 FindAllOverlapping 与逐位置 strings.EqualFold 枚举 oracle 完全一致（含顺序）；
//   - 折叠 FindAll == 贪心最左最长(oracle)；
//   - 折叠 FindNext 以 End 推进迭代 == FindAll；
//   - 精确命中 ⊆ fold 全量命中；不变量（切片恒等、rune 边界）全成立。
func TestCaseFoldRandomOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(20260903))
	for iter := range 300 {
		kws := drawKeywords(rng, foldPool, 2, 6)
		// 文本：随机折叠变形的关键词 + 噪声，词间可重叠拼接
		text := randomText(rng, kws, 4, 40)
		text = foldMangle(rng, text)
		fm := mustNewFold(t, kws)
		em := mustNew(t, kws)

		ovl := fm.FindAllOverlapping(text)
		wantOvl := foldOracleAll(kws, text)
		if !slices.Equal(ovl, wantOvl) {
			t.Fatalf("第 %d 组 Overlapping(fold) 与 oracle 不一致\n词库: %q\n文本: %q\ngot  (%d 条): %v\nwant (%d 条): %v",
				iter, kws, text, len(ovl), ovl, len(wantOvl), wantOvl)
		}
		all := fm.FindAll(text)
		wantAll := greedyLeftmostLongest(wantOvl)
		if !slices.Equal(all, wantAll) {
			t.Fatalf("第 %d 组 FindAll(fold) 与贪心 oracle 不一致\n词库: %q\n文本: %q\ngot  (%d 条): %v\nwant (%d 条): %v",
				iter, kws, text, len(all), all, len(wantAll), wantAll)
		}
		// 不变量：区间合法、切片恒等、起点 rune 边界
		for _, mt := range ovl {
			if text[mt.Start:mt.End] != mt.Keyword || !utf8.RuneStart(text[mt.Start]) {
				t.Fatalf("第 %d 组不变量破坏: %+v（文本 %q）", iter, mt, text)
			}
		}
		// 精确命中 ⊆ fold 全量命中（跨模式组号空间不同，按区间+文本切片对照）
		type span struct {
			start, end int
			kw         string
		}
		ovlSet := make(map[span]struct{}, len(ovl))
		for _, mt := range ovl {
			ovlSet[span{mt.Start, mt.End, mt.Keyword}] = struct{}{}
		}
		for _, mt := range em.FindAll(text) {
			if _, ok := ovlSet[span{mt.Start, mt.End, mt.Keyword}]; !ok {
				t.Fatalf("第 %d 组精确命中 %+v 未出现在 fold 全量集合（文本 %q）", iter, mt, text)
			}
		}
		// 折叠 FindNext 迭代 == 折叠 FindAll
		var iterHits []Match
		for off := 0; ; {
			mt, ok := fm.FindNext(text, off)
			if !ok {
				break
			}
			iterHits = append(iterHits, mt)
			off = mt.End
		}
		if !slices.Equal(iterHits, all) {
			t.Fatalf("第 %d 组 FindNext(fold) 迭代与 FindAll(fold) 不一致\n词库: %q\n文本: %q\n迭代 (%d 条): %v\nFindAll (%d 条): %v",
				iter, kws, text, len(iterHits), iterHits, len(all), all)
		}
	}
}

// TestCaseFoldAdjacentOverlapping 锁定折叠下的重叠/包含链：
// k/kk/kkk 词族在 k/K/开尔文度混排文本上的全量出现与最左最长选择。
func TestCaseFoldAdjacentOverlapping(t *testing.T) {
	const kelvin = "\u212A"
	m := mustNewFold(t, []string{"k", "kk", "kkk"})
	// 文本 runes: k k K K(kelvin) K kelvin，全部折叠等价
	text := "k" + "k" + "K" + kelvin + "K" + kelvin
	ovl := m.FindAllOverlapping(text)
	wantOvl := foldOracleAll([]string{"k", "kk", "kkk"}, text)
	if !slices.Equal(ovl, wantOvl) {
		t.Fatalf("Overlapping(fold) 与 oracle 不一致\ntext: %q\ngot:  %v\nwant: %v", text, ovl, wantOvl)
	}
	all := m.FindAll(text)
	if !slices.Equal(all, greedyLeftmostLongest(wantOvl)) {
		t.Fatalf("FindAll(fold) 与贪心 oracle 不一致\ntext: %q\ngot:  %v\nwant: %v", text, all, greedyLeftmostLongest(wantOvl))
	}
}

// TestCaseFoldBinaryBranch fold 自动机的 find 二分分支（段宽 >16）专项：
// 折叠 CSR 键为轨道展开产物（段内可达精确版数倍宽），须在 >16 宽度下验证
// 段内仍严格升序、二分查找转移正确。
func TestCaseFoldBinaryBranch(t *testing.T) {
	// 17 个折叠互不等价的首字母（跨轨道），次字符各不相同：
	// root 无折叠合一、P 节点 17 个折叠不等价孩子 → CSR 段宽 17 > 16。
	prefix := "P"
	suffixes := "ABCDEFGHIJKLMNOPQ"
	kws := make([]string, 0, len(suffixes))
	for _, s := range suffixes {
		kws = append(kws, prefix+string(s))
	}
	m := mustNewFold(t, kws)
	em := m.(*foldMatcher)
	// 前置白盒断言：P 节点段确实宽于 16。17 个 ASCII 字母折叠互不等价，
	// 轨道展开后 root 为 P/p 两键同目标、P 节点段宽 17×2+1(K 为 p 轨道第三成员)=35。
	pNode := int(em.rootNext['P'])
	if em.rootNext['p'] != em.rootNext['P'] || em.nodes[pNode].count <= 16 {
		t.Fatalf("前置条件不满足：root 键 P/p 不同目标或 P 节点 count=%d（需 >16）",
			em.nodes[pNode].count)
	}
	// 每个词独立命中（每次转移过二分分支）
	for _, s := range suffixes {
		kw := prefix + string(s)
		got := m.FindAll(kw)
		if len(got) != 1 || got[0].Keyword != kw {
			t.Errorf("FindAll(%q) = %v, 期望 1 条 %q", kw, got, kw)
		}
	}
	// 混排大小写变体再过二分分支（文本侧与词侧轨道成员不同）
	mixed := "pA,pB,Pc,pD,Pe,Pf,PG,Ph,Pi,Pj,Pk,Pl,Pm,Pn,Po,Pp,Pq"
	got := m.FindAll(mixed)
	if len(got) != 17 {
		t.Errorf("混排扫描命中 %d 条, 期望 17 条: %v", len(got), got)
	}
}

// TestCaseFoldRuneStartBackMixed 锁定宽度差回退（runeStartBack）跨混合宽度
// rune 的正确性：同轨道成员字节宽可不同（k 1B ↔ U+212A 3B；y 1B ↔ ÿ 2B），
// 命中起点必须按文本侧实际宽度前退。注意全角Ａ（U+FF21）与 ASCII a 是不同
// 轨道（SimpleFold 不跨全半角），不匹配是正确行为。
func TestCaseFoldRuneStartBackMixed(t *testing.T) {
	const kelvin = "\u212A" // 3B，与 k(1B)/K(1B) 同轨道
	// 关键词规范路径 aKbKc（9B/5 runes），文本窄宽交错（A 1B / k 1B / b 1B /
	// K 3B / c 1B = 7B/5 runes）：同 rune 数不同字节，回退按文本侧宽度执行。
	m := mustNewFold(t, []string{"a" + kelvin + "b" + kelvin + "c"})
	text := "A" + "k" + "b" + kelvin + "c"
	got := m.FindAll(text)
	want := []Match{{Start: 0, End: 7, Keyword: text}}
	if !slices.Equal(got, want) {
		t.Fatalf("混合宽度回退不符\ngot:  %v\nwant: %v", got, want)
	}
	// 嵌入噪声后仍正确（前退跨越 3B 宽 rune 与噪声）
	text2 := "x" + text + "y"
	got2 := m.FindAll(text2)
	want2 := []Match{{Start: 1, End: 8, Keyword: text}}
	if !slices.Equal(got2, want2) {
		t.Fatalf("带噪声混合宽度命中不符\ngot:  %v\nwant: %v", got2, want2)
	}
	// 2B 轨道（y/ÿ/Ÿ）：同 rune 数同 span，Keyword 为文本原样切片
	m2 := mustNewFold(t, []string{"aÿb"})
	got3 := m2.FindAll("aŸb")
	want3 := []Match{{Start: 0, End: 4, Keyword: "aŸb"}}
	if !slices.Equal(got3, want3) {
		t.Fatalf("2B 轨道命中不符\ngot:  %v\nwant: %v", got3, want3)
	}
}

// ---------------------------------------------------------------------------
// 模式判别与边界
// ---------------------------------------------------------------------------

func TestCaseFoldEdges(t *testing.T) {
	m := mustNewFold(t, []string{"Go", "中文"})
	// 空文本 / 无命中
	if got := m.FindAll(""); got != nil {
		t.Errorf("空文本 fold 应返回 nil, got %v", got)
	}
	if got := m.FindAll("xyz"); got != nil {
		t.Errorf("无命中 fold 应返回 nil, got %v", got)
	}
	if _, ok := m.FindNext("xyz", 0); ok {
		t.Error("无命中 FindNext(fold) 应返回 false")
	}
	// 非法 UTF-8：不 panic、不误命中（RuneError 不在折叠轨道）
	if got := m.FindAll("g\x88o Go"); len(got) != 1 || got[0] != (Match{Start: 4, End: 6, Keyword: "Go"}) {
		t.Errorf("非法字节 fold 结果不符: %v", got)
	}
	// offset 落在多字节字符中间：向后对齐（中=3B，\x96 为非法字节宽 1）
	if mt, ok := m.FindNext("中\x96中文 Go", 1); !ok || mt != (Match{Start: 4, End: 10, Keyword: "中文", Group: 1}) {
		t.Errorf("rune 对齐 fold 结果不符: (%+v, %v)", mt, ok)
	}
	// offset 语义：后缀首条平移
	if mt, ok := m.FindNext("go go", 3); !ok || mt != (Match{Start: 3, End: 5, Keyword: "go"}) {
		t.Errorf("FindNext(fold, 3) = (%+v, %v), 期望 go(3,5)", mt, ok)
	}
}

// TestCaseFoldMode 模式由 New 一次性定型：CaseFold 判别 + 各自语义。
func TestCaseFoldMode(t *testing.T) {
	text := "Hello, WORLD! 世界"
	em := mustNew(t, []string{"hello", "世界"})
	if em.CaseFold() {
		t.Error("默认 New 应为精确模式（CaseFold=false）")
	}
	if got := em.FindAll(text); len(got) != 1 || got[0].Keyword != "世界" {
		t.Errorf("精确模式结果不符: %v", got)
	}
	fm := mustNewFold(t, []string{"hello", "世界"})
	if !fm.CaseFold() {
		t.Error("WithCaseFold 构建应为折叠模式（CaseFold=true）")
	}
	want := []Match{{Start: 0, End: 5, Keyword: "Hello"}, {Start: 14, End: 20, Keyword: "世界", Group: 1}}
	if got := fm.FindAll(text); !slices.Equal(got, want) {
		t.Errorf("折叠模式结果不符: %v", got)
	}
	if ovl := fm.FindAllOverlapping("HeLLo hello"); len(ovl) != 2 {
		t.Errorf("折叠 Overlapping 应两处均命中: %v", ovl)
	}
	if mt, ok := fm.FindNext("say HELLO", 0); !ok || mt.Keyword != "HELLO" {
		t.Errorf("折叠 FindNext = (%+v, %v), 期望命中 HELLO", mt, ok)
	}
	// 校验错误语义与精确模式一致
	if _, err := New([]string{""}, WithCaseFold()); err == nil {
		t.Error("折叠模式空词应报错")
	}
}

// TestCaseFoldConcurrent 两套 Matcher 并发查询：-race 下无数据竞争、结果稳定。
func TestCaseFoldConcurrent(t *testing.T) {
	em := mustNew(t, []string{"Go", "中国"})
	fm := mustNewFold(t, []string{"Go", "中国"})
	text := "go 中国 GO Go 中国"
	want := fm.FindAll(text)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 50 {
				switch (g + i) % 4 {
				case 0:
					if got := fm.FindAll(text); !slices.Equal(got, want) {
						t.Errorf("并发 fold FindAll 结果漂移: %v", got)
					}
				case 1:
					_ = em.FindAll(text)
				case 2:
					if _, ok := fm.FindNext(text, 0); !ok {
						t.Error("并发 fold FindNext 应有命中")
					}
				case 3:
					_ = fm.FindAllOverlapping(text)
				}
			}
		})
	}
	wg.Wait()
}
