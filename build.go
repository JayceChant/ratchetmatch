package ratchetmatch

import (
	"cmp"
	"slices"
	"unicode"
	"unicode/utf8"
)

// node 是自动机查询期的一个状态。转移表不直接存于节点：全局 CSR 数组中
// [base, base+count) 区间仅存该节点的自有 trie 边（键升序）。
// 未含的 rune 沿 fail 链回退重试；回退到 root 仍未含则留在 root。
type node struct {
	base     int32   // 自有边区间在 Matcher.transKeys/transVals 中的起始下标
	count    int32   // 自有边条数
	fail     int32   // 失败指针：已匹配部分的最长真后缀（且是词库中某关键词前缀）对应的节点；无则指向 root
	outLens  []int32 // 以当前状态结束的全部关键词字节长度，严格降序（自身 + 失败链继承）；nil 表示无
	outRunes []int32 // 折叠自动机专用：与 outLens 平行的关键词 rune 数；精确自动机恒 nil（见 runeStartBack）
}

// builder 聚集构建期状态：root 即 nodes[0]（下标 0 = root，其余节点从 1 起，
// 与成品 Matcher.nodes 下标完全一致），root 的 children map 成品直接复用为
// Matcher.rootNext，兼任跳跃判断集。
// 注意：insert 阶段会 append 扩容 nodes，底层数组可能重新分配，故不可跨
// append 缓存 &b.nodes[i]，一律按下标访问（BFS/flatten 阶段不再 append，
// 局部指针安全）。
type builder struct {
	nodes []trieNode
}

// trieNode 构建期节点：children 即自有 trie 边。
type trieNode struct {
	children map[rune]int32
	fail     int32
	termLen  int32
	outLens  []int32
}

// newBuilder 创建只含 root（nodes[0]）的 builder。
func newBuilder(capHint int) *builder {
	b := &builder{nodes: make([]trieNode, 1, capHint+1)}
	b.nodes[0].children = make(map[rune]int32)
	return b
}

// insert 从 cur 经 rune r 下行，孩子不存在则新建（cur==0 即 root，同一路径）。
func (b *builder) insert(cur int32, r rune) int32 {
	if t, ok := b.nodes[cur].children[r]; ok {
		return t
	}
	t := int32(len(b.nodes))
	b.nodes = append(b.nodes, trieNode{children: make(map[rune]int32)})
	b.nodes[cur].children[r] = t // append 可能扩容，须重新按 cur 下标访问
	return t
}

// gotoWithFail 返回状态 s 在 rune r 上的转移目标（带失败指针回退）。
// 先查 s 的自有边，未命中沿 fail 链逐级回退重试；到 root（nodes[0]）查其
// 孩子表，仍未含则返回 0（留在 root）。构建期失败指针计算与查询期 step 同一语义。
// BFS 约束保证 s 的 fail 指向更浅节点、其 fail 已算好，回退安全。
func (b *builder) gotoWithFail(s int32, r rune) int32 {
	for {
		if t, ok := b.nodes[s].children[r]; ok {
			return t
		}
		if s == 0 {
			return 0
		}
		s = b.nodes[s].fail
	}
}

// buildAutomaton 用 BFS 计算失败指针与输出信息。
// BFS 按层处理，处理某节点时其失败指针指向的更浅节点必已算好。
func (b *builder) buildAutomaton() {
	queue := make([]int32, 0, len(b.nodes))
	for _, c := range b.nodes[0].children { // root（nodes[0]）的孩子 fail 一律为 root
		b.nodes[c].fail = 0
		queue = append(queue, c)
	}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		nd := &b.nodes[n]
		fd := &b.nodes[nd.fail]
		// 1) 用「fail 态带回退查找」设置孩子们的失败指针：
		//    fail(child(n,r)) = goto(fail(n), r)
		for r, c := range nd.children {
			b.nodes[c].fail = b.gotoWithFail(nd.fail, r)
			queue = append(queue, c)
		}
		// 2) 输出信息：以该状态结束的全部关键词长度（降序）。
		//    自身关键词（若有）必最长：失败链上的关键词都是其真后缀，严格更短，
		//    因此 [termLen] ++ fail.outLens 天然严格降序且无重复。
		if nd.termLen > 0 {
			nd.outLens = append([]int32{nd.termLen}, fd.outLens...)
		} else {
			nd.outLens = fd.outLens // 无自身输出时直接共享失败链的切片（构建后只读）
		}
	}
}

// flatten 把 trie 边一次性展平为全局 CSR 有序数组：每个非 root 节点收集
// 孩子键、排序后追加进 keys/vals，记录 base/count。root 的表不进 CSR
// （成品以 map 形式直接复用，兼任跳跃判断集）。
func (b *builder) flatten() ([]node, []rune, []int32) {
	total := 0
	for i := range b.nodes {
		if i > 0 {
			total += len(b.nodes[i].children)
		}
	}
	keys := make([]rune, 0, total)
	vals := make([]int32, 0, total)
	nodes := make([]node, len(b.nodes))
	for i := range b.nodes {
		if i == 0 {
			continue // root 走 map，不进 CSR
		}
		bn := &b.nodes[i]
		nd := &nodes[i]
		nd.fail = bn.fail
		nd.outLens = bn.outLens
		if len(bn.children) == 0 {
			continue // 叶子：count=0，查询期直接沿 fail 回退
		}
		ks := make([]rune, 0, len(bn.children))
		for r := range bn.children {
			ks = append(ks, r)
		}
		slices.Sort(ks)
		nd.base = int32(len(keys))
		nd.count = int32(len(ks))
		for _, r := range ks {
			keys = append(keys, r)
			vals = append(vals, bn.children[r])
		}
	}
	return nodes, keys, vals
}

// ---------- 折叠自动机（WithCaseFold 惰性构建，见 Matcher.resolve） ----------

// foldKey 返回 rune r 折叠轨道的规范代表（轨道最小 rune）。SimpleFold 轨道
// 是不相交的置换环（一般 2–3 成员），环上走一圈取最小值即可让同一轨道的
// 全部成员映射到同一身份：构建期以代表 rune 作 trie 边，fold 相等的关键词
// 分支天然合一；查询期 CSR/root 键已展开为轨道全部成员，文本 rune 直接
// 精确比较即可命中全轨道，热路径零折叠比较、零归一。
func foldKey(r rune) rune {
	c := r
	for t := unicode.SimpleFold(r); t != r; t = unicode.SimpleFold(t) {
		c = min(c, t)
	}
	return c
}

// foldOrbit 返回 rune r（须为规范代表）折叠轨道的全部成员（含自身）。
func foldOrbit(key rune) []rune {
	o := []rune{key}
	for t := unicode.SimpleFold(key); t != key; t = unicode.SimpleFold(t) {
		o = append(o, t)
	}
	return o
}

// foldBuilder 构建期折叠 trie：children 以折叠代表为键，fold 相等的关键词
// 路径共享节点（大小写变体合一），同节点可终止多个关键词（termLens 可含
// 等值重复，BFS 期去重）。
type foldBuilder struct {
	nodes []foldNode
	rootM *Matcher // 成品（byteFilter/rootNext 直接写入）
}

// foldNode 构建期节点：children 即自有折叠边。
type foldNode struct {
	children map[rune]int32 // 键 = 折叠代表 rune
	fail     int32
	termLens []int32 // 插入期收集：以此状态结束的各关键词规范字节长（可重复）
	termNrs  []int32 // 与 termLens 平行：关键词 rune 数
	outLens  []int32 // BFS 后定型：全部输出规范字节长，严格降序（见 flushFoldTerms）
	outRunes []int32 // 与 outLens 平行：关键词 rune 数
}

// buildFold 从精确自动机 m 还原关键词集并构建折叠自动机，返回独立成品
// （folded=true）。流程与 New 相同：插词 → BFS 失败指针 → 展平 CSR；
// 差异仅在边按折叠代表合一、失败指针按折叠匹配计算、root 表/字节过滤器/
// CSR 键展开为全部轨道成员、输出附带关键词 rune 数。
// 词库不在 Matcher 中留存：关键词由精确 trie 无损还原（见 trieKeywords）。
func buildFold(m *Matcher) *Matcher {
	kws := trieKeywords(m)
	fm := &Matcher{folded: true}
	b := &foldBuilder{rootM: fm, nodes: make([]foldNode, 1, len(kws)+1)}
	b.nodes[0].children = make(map[rune]int32)
	for kw := range kws {
		cur := int32(0)
		var nr, bl int32
		for _, r := range kw {
			cur = b.insertFold(cur, r)
			nr++
			bl += int32(utf8.RuneLen(foldKey(r))) // 规范宽度：同轨道成员文本侧宽度可不同
		}
		b.nodes[cur].termLens = append(b.nodes[cur].termLens, bl)
		b.nodes[cur].termNrs = append(b.nodes[cur].termNrs, nr)
	}
	b.buildFoldAutomaton()
	b.flattenFold()
	return fm
}

// insertFold 从 cur 经 rune r 下行，按折叠代表合一：fold 相等的边共享同一
// 目标节点；新建边的键取规范代表（cur==0 即 root，同一路径）。
func (b *foldBuilder) insertFold(cur int32, r rune) int32 {
	key := foldKey(r)
	if t, ok := b.nodes[cur].children[key]; ok {
		return t
	}
	t := int32(len(b.nodes))
	b.nodes = append(b.nodes, foldNode{children: make(map[rune]int32)})
	b.nodes[cur].children[key] = t
	return t
}

// buildFoldAutomaton 用 BFS 计算折叠失败指针与输出信息（语义同 buildAutomaton，
// 区别：孩子键已是折叠代表，回退查找无需折叠比较；输出经 flushFoldTerms 去重）。
func (b *foldBuilder) buildFoldAutomaton() {
	queue := make([]int32, 0, len(b.nodes))
	for _, c := range b.nodes[0].children { // root 的孩子 fail 一律为 root
		b.nodes[c].fail = 0
		queue = append(queue, c)
	}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		nd := &b.nodes[n]
		fd := &b.nodes[nd.fail]
		// fail(child(n,r)) = goto(fail(n), r)：孩子键为折叠代表，精确查即可
		for r, c := range nd.children {
			b.nodes[c].fail = b.gotoFold(nd.fail, r)
			queue = append(queue, c)
		}
		b.flushFoldTerms(nd, fd)
	}
}

// gotoFold 折叠语义的带回退查找（同 gotoWithFail，作用于 foldBuilder）。
func (b *foldBuilder) gotoFold(s int32, key rune) int32 {
	for {
		if t, ok := b.nodes[s].children[key]; ok {
			return t
		}
		if s == 0 {
			return 0
		}
		s = b.nodes[s].fail
	}
}

// flushFoldTerms 定型节点 n 的输出：自身关键词（同节点重复合一）与失败链
// 继承合并为严格降序双数组。同节点全部输出都是规范路径 σ 的后缀（自身即
// σ 全串）：互不相同的后缀 rune 数严格递增 → 规范字节长也严格递增，故按
// (字节长, rune 数) 降序排序并对插入期重复（如 "sS"/"ss"）去重即得严格降序。
func (b *foldBuilder) flushFoldTerms(nd, fd *foldNode) {
	if len(nd.termLens) == 0 {
		nd.outLens = fd.outLens // 无自身输出时直接共享失败链切片（构建后只读）
		nd.outRunes = fd.outRunes
		return
	}
	type outPair struct{ bl, nr int32 }
	ps := make([]outPair, 0, len(nd.termLens)+len(fd.outLens))
	for i, bl := range nd.termLens {
		ps = append(ps, outPair{bl, nd.termNrs[i]})
	}
	for i, bl := range fd.outLens {
		ps = append(ps, outPair{bl, fd.outRunes[i]})
	}
	slices.SortFunc(ps, func(x, y outPair) int {
		if c := cmp.Compare(y.bl, x.bl); c != 0 {
			return c
		}
		return cmp.Compare(y.nr, x.nr)
	})
	ps = slices.CompactFunc(ps, func(x, y outPair) bool { return x == y })
	nd.outLens = make([]int32, len(ps))
	nd.outRunes = make([]int32, len(ps))
	for i, p := range ps {
		nd.outLens[i] = p.bl
		nd.outRunes[i] = p.nr
	}
}

// setFilterBit 把 rune r 的 UTF-8 首字节置入字节过滤器（折叠版 New 的
// 对应操作；r 为合法关键词 rune，编码必为 1–4 字节）。
func (m *Matcher) setFilterBit(r rune) {
	var buf [utf8.UTFMax]byte
	utf8.EncodeRune(buf[:], r)
	m.byteFilter[buf[0]>>3] |= 1 << (buf[0] & 7)
}

// flattenFold 展平折叠自动机：CSR 每条自有边展开为其轨道全部成员（轨道互
// 不相交 → 段内仍严格升序，二分查找不受影响），目标指向同一节点；root 表
// 与字节过滤器同步展开（rootNext 含全部轨道成员，skipForward 无需归一）。
func (b *foldBuilder) flattenFold() {
	fm := b.rootM
	root := make(map[rune]int32, len(b.nodes[0].children)*2)
	for key, t := range b.nodes[0].children {
		for _, r := range foldOrbit(key) {
			root[r] = t
			fm.setFilterBit(r) // 各轨道成员的 UTF-8 首字节都置位
		}
	}
	total := 0
	for i := range b.nodes {
		if i > 0 {
			total += len(b.nodes[i].children) * 2 // 预估：轨道成员普遍 2–3 个，不足由 append 扩容
		}
	}
	keys := make([]rune, 0, total)
	vals := make([]int32, 0, total)
	nodes := make([]node, len(b.nodes))
	for i := range b.nodes {
		if i == 0 {
			continue // root 走 map，不进 CSR
		}
		bn := &b.nodes[i]
		nd := &nodes[i]
		nd.fail = bn.fail
		nd.outLens = bn.outLens
		nd.outRunes = bn.outRunes
		if len(bn.children) == 0 {
			continue // 叶子：count=0，查询期直接沿 fail 回退
		}
		ks := make([]rune, 0, len(bn.children)*2)
		for key := range bn.children {
			ks = append(ks, foldOrbit(key)...)
		}
		slices.Sort(ks)
		nd.base = int32(len(keys))
		nd.count = int32(len(ks))
		for _, r := range ks {
			keys = append(keys, r)
			vals = append(vals, bn.children[foldKey(r)])
		}
	}
	fm.nodes = nodes
	fm.transKeys = keys
	fm.transVals = vals
	fm.rootNext = root
}

// trieKeywords 从成品精确自动机还原去重后的关键词集合（rune 路径拼接）。
// 词尾判定 outLens[0] == 路径字节长：termLen 即全路径字节长，fail 链继承的
// 真后缀严格更短，等式精确（与基准 trieFindAll 同一判据）；root 走 rootNext。
func trieKeywords(m *Matcher) map[string]struct{} {
	kws := make(map[string]struct{})
	var walk func(s int32, prefix []rune, bytes int)
	walk = func(s int32, prefix []rune, bytes int) {
		nd := &m.nodes[s]
		if len(nd.outLens) > 0 && int(nd.outLens[0]) == bytes {
			kws[string(prefix)] = struct{}{} // string() 拷贝前缀，DFS 复用底层数组安全
		}
		if s == 0 {
			for r, t := range m.rootNext {
				walk(t, append(prefix, r), bytes+utf8.RuneLen(r))
			}
			return
		}
		for i := range nd.count {
			r := m.transKeys[nd.base+i]
			walk(m.transVals[nd.base+i], append(prefix, r), bytes+utf8.RuneLen(r))
		}
	}
	walk(0, nil, 0)
	return kws
}
