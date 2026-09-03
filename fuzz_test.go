// 本文件为 ratchetmatch 的 Go 原生 fuzz 测试（package ratchetmatch）。
// 核心价值：任意字节文本（含非法 UTF-8）× 任意关键词组合下，
// 1) 绝不 panic；2) 三种查询满足一组可判定的不变量与朴素 oracle。
// 运行：go test -fuzz FuzzMatch -fuzztime 30s（短跑）；种子语料经 f.Add 随源码提交，
// 引擎生成的新语料默认落在 GOCACHE/fuzz；发现的崩溃样本回归于
// testdata/fuzz/FuzzMatch/（随仓库提交，普通 go test 自动重放）。
package ratchetmatch

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// countOverlapping 统计 kw 在 s 中的全部出现次数（步进 1 字节，允许自重叠），
// 作为 FindAllOverlapping 的朴素 oracle。注意 strings.Count 是非重叠计数，
// 不能用于此处。
func countOverlapping(s, kw string) int {
	n := 0
	for i := 0; ; {
		j := strings.Index(s[i:], kw)
		if j < 0 {
			return n
		}
		n++
		i += j + 1
	}
}

// checkMatchInvariants 校验单条命中的固有性质：区间落在文本内、切片等于
// 关键词、起点为 rune 边界、关键词确属词库。End 不要求 rune 边界：文本中的
// 非法字节按 RuneError 逐字节前进（宽度 1），若关键词自身亦以单字节
// RuneError 身份入词（如 "\xbc"），命中 End 可能落在原始文本的续字节上，
// 此时 text[Start:End] == Keyword 仍成立，属规范允许的退化情形。
func checkMatchInvariants(t *testing.T, text string, kws []string, label string, ms []Match) {
	t.Helper()
	kwSet := make(map[string]struct{}, len(kws))
	for _, kw := range kws {
		kwSet[kw] = struct{}{}
	}
	for i, m := range ms {
		if m.Start < 0 || m.End > len(text) || m.Start >= m.End {
			t.Fatalf("%s 第 %d 条区间非法: %+v（文本长 %d）", label, i, m, len(text))
		}
		if text[m.Start:m.End] != m.Keyword {
			t.Fatalf("%s 第 %d 条切片与关键词不一致: text[%d:%d]=%q kw=%q",
				label, i, m.Start, m.End, text[m.Start:m.End], m.Keyword)
		}
		if !utf8.RuneStart(text[m.Start]) {
			t.Fatalf("%s 第 %d 条起点未落在 rune 边界: %+v", label, i, m)
		}
		if _, ok := kwSet[m.Keyword]; !ok {
			t.Fatalf("%s 第 %d 条关键词 %q 不在词库中", label, i, m.Keyword)
		}
	}
}

// FuzzMatch 对（文本字节串 × 三个候选关键词）做不变量与 oracle 对照：
//   - FindAll：按 Start 升序、命中互不重叠、切片恒等
//   - FindAllOverlapping：总数与逐词 countOverlapping 之和一致（oracle），
//     按 End 升序、同 End 长度降序；FindAll 的每条命中必属于其集合
//   - FindNext 以 End 推进迭代得到的序列与 FindAll 完全一致
func FuzzMatch(f *testing.F) {
	// 种子语料：覆盖贪心链、自重叠、前缀包含、非法 UTF-8、Emoji、空文本
	f.Add([]byte("中国人"), "国", "中国人", "人")
	f.Add([]byte("上海口"), "上海", "海口", "")
	f.Add([]byte("AAAA"), "AA", "A", "AAA")
	f.Add([]byte("中x中毒"), "中", "中毒", "")
	f.Add([]byte{0xE4, 0xB8, 0xAD, 0xE6, 0x88, 'x'}, "中", "国", "\uFFFD")
	f.Add([]byte("x😀y😀"), "😀", "x😀", "")
	f.Add([]byte("aPQ,bPR,cPS"), "PQ", "PR", "PS")
	f.Add([]byte(""), "a", "ab", "abc")
	f.Add([]byte("北京欢迎您"), "北京", "欢迎", "京欢迎")
	// fold 种子：大小写变体、宽度差（开尔文度）、变音、希腊终格、ß
	f.Add([]byte("Go Go GO go"), "go", "Go", "")
	f.Add([]byte("k\u212A\u212Ak"), "\u212A", "k", "")
	f.Add([]byte("TÜR tür"), "tür", "TUR", "")
	f.Add([]byte("ΚΟΣΜΟΣ κόσμος"), "κοσμος", "ΣΊΣΥΦΟΣ", "")

	f.Fuzz(func(t *testing.T, text []byte, kwA, kwB, kwC string) {
		// 空串是合法哨兵（等价省略该关键词），剔除后构建词库
		kws := make([]string, 0, 3)
		for _, kw := range []string{kwA, kwB, kwC} {
			if kw != "" {
				kws = append(kws, kw)
			}
		}
		if len(kws) == 0 {
			t.Skip("无有效关键词")
		}
		// 契约 oracle：非法 UTF-8 或含 U+FFFD 的关键词 New 必须拒绝
		// （rune 歧义：身份坍缩 / 长度不一致，见 spec 词库校验需求）。
		// 与 New 同序：以首个非法关键词判定期望错误类别。
		bad := ""
		for _, kw := range kws {
			switch {
			case !utf8.ValidString(kw):
				bad = "not valid UTF-8"
			case strings.Contains(kw, "\uFFFD"):
				bad = "U+FFFD"
			}
			if bad != "" {
				break
			}
		}
		if bad != "" {
			_, err := New(kws)
			if err == nil || !strings.Contains(err.Error(), bad) {
				t.Fatalf("非法词库 %q 未按契约拒绝（期望错误含 %q），err=%v", kws, bad, err)
			}
			t.Skipf("词库非法（%s），拒绝语义已验证", bad)
		}
		// 重复输入词与库内去重行为等价，排序归一后构建
		slices.Sort(kws)
		kws = slices.Compact(kws)
		m, err := New(kws)
		if err != nil {
			t.Fatalf("New(%q) 意外失败: %v", kws, err)
		}
		s := string(text)

		// --- FindAll：顺序 + 互不重叠 + 基本不变量 ---
		all := m.FindAll(s)
		checkMatchInvariants(t, s, kws, "FindAll", all)
		for i := 1; i < len(all); i++ {
			if all[i-1].Start > all[i].Start {
				t.Fatalf("FindAll 非 Start 升序: [%d]=%+v > [%d]=%+v", i-1, all[i-1], i, all[i])
			}
			if all[i-1].End > all[i].Start {
				t.Fatalf("FindAll 相邻命中重叠: %+v 与 %+v", all[i-1], all[i])
			}
		}

		// --- FindAllOverlapping：朴素 oracle + 输出序 + 蕴含 FindAll ---
		ovl := m.FindAllOverlapping(s)
		checkMatchInvariants(t, s, kws, "Overlapping", ovl)
		wantTotal := 0
		for _, kw := range kws {
			wantTotal += countOverlapping(s, kw)
		}
		if len(ovl) != wantTotal {
			t.Fatalf("Overlapping 共 %d 条, oracle 期望 %d（词库 %q, 文本 %q）",
				len(ovl), wantTotal, kws, s)
		}
		for i := 1; i < len(ovl); i++ {
			a, b := ovl[i-1], ovl[i]
			if a.End > b.End || (a.End == b.End && len(a.Keyword) < len(b.Keyword)) {
				t.Fatalf("Overlapping 顺序破坏（应 End 升序、同 End 长度降序）: %+v → %+v", a, b)
			}
		}
		ovlSet := make(map[Match]struct{}, len(ovl))
		for _, m2 := range ovl {
			ovlSet[m2] = struct{}{}
		}
		for _, h := range all {
			if _, ok := ovlSet[h]; !ok {
				t.Fatalf("FindAll 命中 %+v 未出现在 Overlapping 结果中（文本 %q）", h, s)
			}
		}

		// --- FindNext 迭代 == FindAll ---
		iter := findAllByFindNext(m, s)
		if len(iter) != len(all) {
			t.Fatalf("FindNext 迭代 %d 条与 FindAll %d 条不一致（词库 %q, 文本 %q）",
				len(iter), len(all), kws, s)
		}
		for i := range all {
			if iter[i] != all[i] {
				t.Fatalf("第 %d 条不一致: 迭代 %+v vs FindAll %+v（词库 %q, 文本 %q）",
					i, iter[i], all[i], kws, s)
			}
		}

		// --- FindNext 任意 offset == 从该 offset 起的 FindAll 首条 ---
		// 逐字节 offset 对照（含非法 UTF-8 中间偏移），语义：对齐 rune 边界后
		// 与 suffix 上 FindAll 的第一条一致（坐标平移 +off）；offset >= len 时
		// 必为 false。
		for off := 0; off <= len(s); off++ {
			got, ok := m.FindNext(s, off)
			wantAll := m.FindAll(s[off:])
			if len(wantAll) > 0 {
				want := wantAll[0]
				want.Start += off
				want.End += off
				if !ok || got != want {
					t.Fatalf("FindNext(text,%d) = (%+v,%v)，期望后缀 FindAll 首条 %+v（词库 %q，文本 %q）",
						off, got, ok, want, kws, s)
				}
			} else if ok {
				t.Fatalf("FindNext(text,%d) = (%+v,%v)，期望 false（后缀无命中，词库 %q，文本 %q）",
					off, got, ok, kws, s)
			}
		}

		// --- fold：Overlapping 与逐位置 strings.EqualFold 枚举 oracle 一致 ---
		foldOvl := m.FindAllOverlapping(s, WithCaseFold())
		wantFoldOvl := foldOracleAll(kws, s)
		if !slices.Equal(foldOvl, wantFoldOvl) {
			t.Fatalf("Overlapping(fold) %d 条与 oracle %d 条不一致（词库 %q, 文本 %q）\ngot:  %v\nwant: %v",
				len(foldOvl), len(wantFoldOvl), kws, s, foldOvl, wantFoldOvl)
		}
		// --- fold：FindAll == 贪心最左最长(oracle) ---
		foldAll := m.FindAll(s, WithCaseFold())
		wantFoldAll := greedyLeftmostLongest(wantFoldOvl)
		if !slices.Equal(foldAll, wantFoldAll) {
			t.Fatalf("FindAll(fold) 与贪心 oracle 不一致（词库 %q, 文本 %q）\ngot:  %v\nwant: %v",
				kws, s, foldAll, wantFoldAll)
		}
		// --- fold-only Matcher 与惰性构建的 fold 结果一致 ---
		mo, err := New(kws, WithCaseFold())
		if err != nil {
			t.Fatalf("New(fold-only) 意外失败: %v", err)
		}
		if !slices.Equal(mo.FindAll(s), foldAll) {
			t.Fatalf("fold-only FindAll 与惰性 fold 不一致（词库 %q, 文本 %q）", kws, s)
		}
		// --- fold：FindNext 迭代 == FindAll；任意 offset == 后缀首条平移 ---
		var foldIter []Match
		for off := 0; ; {
			mt, ok := m.FindNext(s, off, WithCaseFold())
			if !ok {
				break
			}
			foldIter = append(foldIter, mt)
			off = mt.End
		}
		if !slices.Equal(foldIter, foldAll) {
			t.Fatalf("FindNext(fold) 迭代与 FindAll(fold) 不一致（词库 %q, 文本 %q）", kws, s)
		}
		for off := 0; off <= len(s); off++ {
			got, ok := m.FindNext(s, off, WithCaseFold())
			wantAll := m.FindAll(s[off:], WithCaseFold())
			if len(wantAll) > 0 {
				want := wantAll[0]
				want.Start += off
				want.End += off
				if !ok || got != want {
					t.Fatalf("FindNext(fold,%d) = (%+v,%v)，期望后缀 FindAll 首条 %+v（词库 %q，文本 %q）",
						off, got, ok, want, kws, s)
				}
			} else if ok {
				t.Fatalf("FindNext(fold,%d) = (%+v,%v)，期望 false（后缀无命中，词库 %q，文本 %q）",
					off, got, ok, kws, s)
			}
		}
	})
}
