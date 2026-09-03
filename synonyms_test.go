// 本文件为同义词分组（WithSynonyms）的专项测试（package ratchetmatch）：
//   - 分区语义：恒填充、单元素组、声明组编号与 GroupWords 往返；
//   - 语义正交：有/无分组构建的 FindAll / FindAllOverlapping / FindNext
//     区间裁决逐条一致（组号除外），含「同起点取更长时组号随更长关键词
//     转移」「必死候选不改变链」等链规则边角；
//   - 与 WithCaseFold 正交：归一形词身份（同组合一、跨组冲突）、fold 命中
//     的 Group 为规范词身份；
//   - 校验报错：空组 / 组员空串或非法 / 同词跨组冲突（原形与归一形）。
package ratchetmatch

import (
	"math/rand"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
)

// mustNewSyn 以 WithSynonyms 构建 Matcher，意外失败时立即终止当前测试。
func mustNewSyn(t *testing.T, keywords []string, groups [][]string) Matcher {
	t.Helper()
	m, err := New(keywords, WithSynonyms(groups))
	if err != nil {
		t.Fatalf("New(%q, WithSynonyms(%q)) 意外失败: %v", keywords, groups, err)
	}
	return m
}

// TestSynonymsPartition 分区语义：恒填充、组号规则与 GroupWords 往返。
func TestSynonymsPartition(t *testing.T) {
	m := mustNewSyn(t, []string{"服务器"}, [][]string{{"电脑", "计算机", "PC"}})
	// 组员自动入库可命中；组号：声明组 {电脑,计算机,PC}=0，服务器自成组 1
	text := "电脑和服务器"
	want := []Match{
		{Start: 0, End: 6, Keyword: "电脑", Group: 0},
		{Start: 9, End: 18, Keyword: "服务器", Group: 1},
	}
	assertMatches(t, text, "同义词分组", m.FindAll(text), want)

	// GroupWords 往返：声明组返回去重成员（词库序），单元素组返回词本身
	if got := m.GroupWords(0); !slices.Equal(got, []string{"电脑", "计算机", "PC"}) {
		t.Errorf("GroupWords(0) = %q, 期望 [电脑 计算机 PC]", got)
	}
	if got := m.GroupWords(1); !slices.Equal(got, []string{"服务器"}) {
		t.Errorf("GroupWords(1) = %q, 期望 [服务器]", got)
	}
	for _, g := range []int{-1, 2, 100} {
		if got := m.GroupWords(g); got != nil {
			t.Errorf("GroupWords(%d) = %q, 期望 nil", g, got)
		}
	}

	// 未声明分组的词自成单元素组：词库序为显式关键词在前、组员按组序在后。
	// 仅一组声明 → 组 0 = {丙,丁}（词库序：甲 乙 丙 丁 → 甲=1 乙=2）
	m2 := mustNewSyn(t, []string{"甲", "乙"}, [][]string{{"丙", "丁"}})
	for kw, g := range map[string]int{"甲": 1, "乙": 2, "丙": 0, "丁": 0} {
		hits := m2.FindAll(kw)
		if len(hits) != 1 || hits[0].Group != g {
			t.Errorf("词 %q 命中 = %v, 期望组号 %d", kw, hits, g)
		}
		if got := m2.GroupWords(hits[0].Group); g != 0 && !slices.Equal(got, []string{kw}) {
			t.Errorf("GroupWords(%d) 与词 %q 不对应: %q", g, kw, got)
		}
	}

	// 组内重复成员自动去重；同一词在显式关键词与组员中重复只入一次词库
	m3 := mustNewSyn(t, []string{"A", "A"}, [][]string{{"B", "B", "C"}})
	if got := m3.GroupWords(0); !slices.Equal(got, []string{"B", "C"}) {
		t.Errorf("组内去重失败: %q", got)
	}
	for _, kw := range []string{"A", "B", "C"} {
		if len(m3.FindAll(kw)) != 1 {
			t.Errorf("词 %q 应可命中", kw)
		}
	}

	// 词库可仅由组构成
	m4, err := New(nil, WithSynonyms([][]string{{"像素", "px"}}))
	if err != nil {
		t.Fatalf("New(nil, WithSynonyms) 意外失败: %v", err)
	}
	if got := m4.FindAll("px 与像素"); len(got) != 2 || got[0].Group != 0 || got[1].Group != 0 {
		t.Errorf("纯组词库命中不符: %v", got)
	}
	if len(m4.GroupWords(0)) != 2 {
		t.Errorf("纯组词库 GroupWords(0) = %q", m4.GroupWords(0))
	}
}

// TestSynonymsSemanticsUntouched 语义正交：分组不改变 FindAll / Overlapping
// / FindNext 的任何裁决——同（扩充后）词库有/无分组构建逐条对照（区间、
// Keyword 一致；无分组组号 = 词库下标）。注意组员自动入库，对照组词库须含
// 全部组员，否则「多命中」是词库差异而非语义差异。
func TestSynonymsSemanticsUntouched(t *testing.T) {
	kws := []string{"电脑", "电脑城", "计算机", "服务器", "主机", "国", "人", "中国人", "0", "000"}
	groups := [][]string{{"电脑", "计算机"}, {"服务器", "主机"}}
	texts := []string{
		"电脑城的服务器和计算机",  // 电脑城 胜 电脑（最左最长，带 电脑城 的组）
		"中国人中国梦",       // 真包含 / fail 结算
		"000000000001", // 必死候选不弹链（fuzz 发现的空档场景）
		"主机与电脑城之间",     // 组员与包含词混排
	}
	withSyn, err := New(kws, WithSynonyms(groups))
	if err != nil {
		t.Fatalf("New 意外失败: %v", err)
	}
	plain, err := New(kws)
	if err != nil {
		t.Fatalf("New 意外失败: %v", err)
	}
	for _, text := range texts {
		a, b := withSyn.FindAll(text), plain.FindAll(text)
		if len(a) != len(b) {
			t.Fatalf("FindAll 条数不符（文本 %q）: %v vs %v", text, a, b)
		}
		for i := range a {
			if a[i].Start != b[i].Start || a[i].End != b[i].End || a[i].Keyword != b[i].Keyword {
				t.Fatalf("FindAll 区间不符（文本 %q 第 %d 条）: %+v vs %+v", text, i, a[i], b[i])
			}
		}
		oa, ob := withSyn.FindAllOverlapping(text), plain.FindAllOverlapping(text)
		if len(oa) != len(ob) {
			t.Fatalf("Overlapping 条数不符（文本 %q）", text)
		}
		for i := range oa {
			if oa[i].Start != ob[i].Start || oa[i].End != ob[i].End || oa[i].Keyword != ob[i].Keyword {
				t.Fatalf("Overlapping 区间不符（文本 %q 第 %d 条）: %+v vs %+v", text, i, oa[i], ob[i])
			}
		}
	}
	// 关键裁决：文本 "电脑城" 命中的是 电脑城（Keyword 与组号均属 电脑城）
	m := mustNewSyn(t, []string{"电脑", "电脑城"}, [][]string{{"电脑", "计算机"}})
	hits := m.FindAll("电脑城")
	if len(hits) != 1 || hits[0].Keyword != "电脑城" {
		t.Fatalf("分组不得改变最左最长裁决: %v", hits)
	}
	if hits[0].Group != 1 { // 电脑城 未声明分组 → 单元素组（声明组 0 之后）
		t.Errorf("电脑城 的组号 = %d, 期望 1", hits[0].Group)
	}
	// 同起点真包含取最长：组号随更长关键词转移
	if hits := m.FindAll("x电脑"); len(hits) != 1 || hits[0].Group != 0 {
		t.Errorf("短词组号转移不符: %v", hits)
	}
	// FindNext 组号一致
	mt, ok := m.FindNext("电脑城", 0)
	if !ok || mt.Keyword != "电脑城" || mt.Group != 1 {
		t.Errorf("FindNext 组号不符: %+v", mt)
	}
}

// TestSynonymsRandomUntouched 随机对照：同词库有/无分组的 FindAll 区间序列
// 完全一致（200 组），naiveSearch（带组号）双 oracle 对照。
func TestSynonymsRandomUntouched(t *testing.T) {
	rng := rand.New(rand.NewSource(20260904))
	pool := []string{"中", "中国", "中国人", "国", "人", "上海", "海口", "北京", "a", "ab"}
	for i := range 200 {
		kws := drawKeywords(rng, pool, 3, 7)
		// 随机划分若干声明组（前 2 词为一组的概率各半）
		var groups [][]string
		if rng.Intn(2) == 0 {
			groups = append(groups, []string{kws[0], kws[1]})
		}
		if rng.Intn(2) == 0 {
			groups = append(groups, []string{kws[len(kws)-1]})
		}
		text := randomText(rng, kws, 20, 80)
		m1, err := New(kws, WithSynonyms(groups))
		if err != nil {
			t.Fatalf("第 %d 组 New 意外失败: %v", i, err)
		}
		m2, err := New(kws)
		if err != nil {
			t.Fatalf("第 %d 组 New 意外失败: %v", i, err)
		}
		g1, g2 := m1.FindAll(text), m2.FindAll(text)
		for j := range g1 {
			if g1[j].Start != g2[j].Start || g1[j].End != g2[j].End || g1[j].Keyword != g2[j].Keyword {
				t.Fatalf("第 %d 组区间不符: %+v vs %+v（词库 %q, 分组 %q, 文本 %q）",
					i, g1[j], g2[j], kws, groups, text)
			}
		}
		// 双 oracle：无分组构建的 FindAll == naiveSearch（含组号 = 词库下标）
		if want := naiveSearch(kws, text); !reflect.DeepEqual(g2, want) {
			t.Fatalf("第 %d 组 naive oracle 不符\n词库: %q\n文本: %q\ngot:  %v\nwant: %v", i, kws, text, g2, want)
		}
	}
}

// TestSynonymsWithCaseFold 分组与折叠正交：归一形词身份。
func TestSynonymsWithCaseFold(t *testing.T) {
	m, err := New(nil, WithCaseFold(), WithSynonyms([][]string{{"个人电脑", "PC", "pc"}}))
	if err != nil {
		t.Fatalf("New(fold+syn) 意外失败: %v", err)
	}
	hits := m.FindAll("pc 个人电脑 PC")
	if len(hits) != 3 {
		t.Fatalf("fold+syn 命中 %d 条: %v", len(hits), hits)
	}
	for i, h := range hits {
		if h.Group != 0 {
			t.Errorf("第 %d 条 Group = %d, 期望 0（归一形 pc 同组）", i, h.Group)
		}
	}
	if hits[0].Keyword != "pc" || hits[2].Keyword != "PC" {
		t.Errorf("Keyword 应为文本原样切片: %q %q", hits[0].Keyword, hits[2].Keyword)
	}
	// GroupWords 返回归一形成员（foldKey 取轨道最小 rune，ASCII 大写字母
	// 为代表：PC/pc 归一同形去重为 2 项）
	got := m.GroupWords(0)
	if !slices.Equal(got, []string{"个人电脑", "PC"}) {
		t.Errorf("GroupWords(0) = %q, 期望 [个人电脑 PC]（归一形）", got)
	}
	// 大小写变体词库在 fold 模式下归一同形：词库变体自动同组
	m2, err := New([]string{"Stop", "stop"}, WithCaseFold(), WithSynonyms([][]string{{"stop", "halt"}}))
	if err != nil {
		t.Fatalf("New 意外失败: %v", err)
	}
	if hits := m2.FindAll("SToP sTop halt"); len(hits) != 3 {
		t.Fatalf("命中 %d 条: %v", len(hits), hits)
	} else {
		for _, h := range hits {
			if h.Group != 0 {
				t.Errorf("折叠同形词 %q 组号 = %d, 期望 0", h.Keyword, h.Group)
			}
		}
	}
	if got := m2.GroupWords(0); !slices.Equal(got, []string{"STOP", "HALT"}) {
		t.Errorf("GroupWords(0) = %q, 期望 [STOP HALT]（归一形）", got)
	}
	// 分组在 fold 模式下不改变语义：与「含全部组员的无分组 fold 构建」对照
	m3, _ := New([]string{"Go", "中文", "Golang"}, WithCaseFold())
	m4, _ := New([]string{"Go", "中文", "Golang"}, WithCaseFold(), WithSynonyms([][]string{{"Go", "Golang"}}))
	text := "go 中国 GO Golang"
	if !slices.Equal(stripGroup(m4.FindAll(text)), stripGroup(m3.FindAll(text))) {
		t.Errorf("fold 下分组改变语义: %v vs %v", m4.FindAll(text), m3.FindAll(text))
	}
}

// stripGroup 复制命中的同时抹掉 Group，供跨构建（组号空间不同）对照。
func stripGroup(ms []Match) []Match {
	out := make([]Match, len(ms))
	for i, m := range ms {
		out[i] = Match{Start: m.Start, End: m.End, Keyword: m.Keyword}
	}
	return out
}

// TestSynonymsValidation 分组校验报错（信息可区分原因）。
func TestSynonymsValidation(t *testing.T) {
	cases := []struct {
		name     string
		keywords []string
		groups   [][]string
		want     string
	}{
		{"空组", []string{"a"}, [][]string{{}}, "group at index 0 is empty"},
		{"组员空串", nil, [][]string{{"a", ""}}, "member 1 is empty"},
		{"组员非法UTF-8", nil, [][]string{{"\xff"}}, "not valid UTF-8"},
		{"组员含U+FFFD", nil, [][]string{{"a\uFFFD"}}, "U+FFFD"},
		{"同词跨组", nil, [][]string{{"A", "B"}, {"B", "C"}}, `both group 0 and group 1`},
		{"fold归一形跨组冲突", nil, [][]string{{"PC"}, {"pc"}}, `both group 0 and group 1`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := []Option{WithSynonyms(tc.groups)}
			if strings.HasPrefix(tc.name, "fold") {
				opts = []Option{WithCaseFold(), WithSynonyms(tc.groups)}
			}
			m, err := New(tc.keywords, opts...)
			if err == nil || m != nil {
				t.Fatalf("%s: 期望报错，实际 err=%v m=%v", tc.name, err, m)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s: 错误信息应含 %q，实际: %q", tc.name, tc.want, err.Error())
			}
		})
	}
	// 对照：组内重复成员不报错（去重），fold 同形同组合一也不报错
	if _, err := New(nil, WithSynonyms([][]string{{"a", "a"}, {"b"}})); err != nil {
		t.Errorf("组内重复不应报错: %v", err)
	}
	if _, err := New(nil, WithCaseFold(), WithSynonyms([][]string{{"PC", "pc"}})); err != nil {
		t.Errorf("fold 同组归一同形不应报错: %v", err)
	}
	// 显式关键词非法仍按关键词下标报错
	if _, err := New([]string{"ok", "\xff"}, WithSynonyms([][]string{{"a"}})); err == nil ||
		!strings.Contains(err.Error(), "index 1") {
		t.Errorf("显式关键词非法应按 index 报错: %v", err)
	}
}

// TestSynonymsConcurrent 分组 Matcher 并发查询：-race 无竞争、结果稳定。
func TestSynonymsConcurrent(t *testing.T) {
	m := mustNewSyn(t, []string{"北京", "上海"}, [][]string{{"帝都", "京城", "北京"}})
	text := "帝都上海京城北京上海"
	want := m.FindAll(text)
	if len(want) != 5 {
		t.Fatalf("预计算命中 %d 条: %v", len(want), want)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 50 {
				if got := m.FindAll(text); !slices.Equal(got, want) {
					t.Errorf("并发 FindAll 结果漂移: %v", got)
				}
				if ovl := m.FindAllOverlapping(text); len(ovl) != 5 {
					t.Errorf("并发 Overlapping 条数漂移: %d", len(ovl))
				}
			}
		})
	}
	wg.Wait()
}
