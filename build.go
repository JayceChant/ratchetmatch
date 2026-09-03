// 本文件为两套自动机的构建管线（公共接口见 matcher.go，查询引擎见
// engine.go，选项见 option.go）：分组解析（resolveSynonyms）、精确构建
// （trie 插入 → BFS 失败指针 → CSR 展平 → BM 字节过滤器）与折叠构建
// （SimpleFold 轨道代表合一 + 轨道展开，见文件末段）。两套管线均直接从
// 关键词列表构建，构建后即定型为对应模式；命名与 engine.go 的 exact*/
// fold* 系列保持对偶（exactBuilder/trieNode ↔ foldBuilder/foldBuilderNode）。
package ratchetmatch

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// 同义词分组解析（WithSynonyms → 词库扩充 + 组号表）
// ---------------------------------------------------------------------------

// synonymPart 聚合同义词分组的解析产物：
//   - groups：WithSynonyms 声明组成员表（nil = 未使用 WithSynonyms）；
//   - singletons：未声明分组的词库下标表（词库序；其组号 = len(groups)+表内序号）；
//   - groupOf：词→组号（词身份由 norm 决定：折叠模式为折叠归一形）。
type synonymPart struct {
	groups     [][]string
	singletons []int32
	groupOf    map[string]int32
}

// resolveSynonyms 合并显式关键词与同义词组员（组员自动入库、组内去重），
// 返回去重后词库与分组解析结果。fold 非 nil 时词身份为折叠归一形，返回的
// 词库亦为归一形——与折叠 trie 的边键（foldKey）同构，折叠等价词天然合一，
// 构建出的自动机与原词输入完全等价。显式关键词的合法性（空串/非法
// UTF-8/U+FFFD）由 New 在调用前按原始下标校验；组员的同类错误在此按
// 「组下标 + 组内下标」报出（信息可区分原因）。同一词出现在两个声明组
// 报错（含词与两组号，fold 模式按归一形判定）。
func resolveSynonyms(keywords []string, syn [][]string, norm func(string) string) ([]string, *synonymPart, error) {
	part := &synonymPart{}
	// 词库：显式关键词按词身份去重（保序），组员按组序并入
	seen := make(map[string]struct{}, len(keywords))
	kws := make([]string, 0, len(keywords))
	for _, kw := range keywords {
		id := kw
		if norm != nil {
			id = norm(kw)
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		kws = append(kws, id)
	}
	if len(syn) == 0 {
		// 未使用 WithSynonyms：每词一个单元素组，组号即去重词库序
		part.groupOf = make(map[string]int32, len(kws))
		for i, kw := range kws {
			part.groupOf[kw] = int32(i)
		}
		return kws, part, nil
	}
	part.groupOf = make(map[string]int32)
	part.groups = make([][]string, 0, len(syn))
	for gi, g := range syn {
		if len(g) == 0 {
			return nil, nil, fmt.Errorf("ratchetmatch: synonym group at index %d is empty", gi)
		}
		inGrp := make(map[string]struct{}, len(g))
		members := make([]string, 0, len(g))
		for mi, w := range g {
			switch {
			case w == "":
				return nil, nil, fmt.Errorf("ratchetmatch: synonym group %d member %d is empty", gi, mi)
			case !utf8.ValidString(w):
				return nil, nil, fmt.Errorf("ratchetmatch: synonym group %d member %d is not valid UTF-8", gi, mi)
			case strings.Contains(w, "\uFFFD"):
				return nil, nil, fmt.Errorf("ratchetmatch: synonym group %d member %d contains U+FFFD (replacement character)", gi, mi)
			}
			id := w
			if norm != nil {
				id = norm(w)
			}
			if prev, clash := part.groupOf[id]; clash && prev != int32(gi) {
				return nil, nil, fmt.Errorf("ratchetmatch: synonym %q belongs to both group %d and group %d", w, prev, gi)
			}
			if _, dup := inGrp[id]; dup {
				continue
			}
			inGrp[id] = struct{}{}
			part.groupOf[id] = int32(gi)
			members = append(members, id)
			if _, dup := seen[id]; !dup {
				seen[id] = struct{}{}
				kws = append(kws, id)
			}
		}
		part.groups = append(part.groups, members)
	}
	// 未声明分组的词按词库序获得单元素组（组号接在声明组之后）
	for i, kw := range kws {
		if _, declared := part.groupOf[kw]; declared {
			continue
		}
		part.groupOf[kw] = int32(len(part.groups) + len(part.singletons))
		part.singletons = append(part.singletons, int32(i))
	}
	return kws, part, nil
}

// foldNorm 返回词 w 的折叠归一形（逐 rune 取折叠轨道代表，与折叠 trie 边键
// foldKey 同源）：归一形相同的词在折叠自动机中共享同一路径与终止节点。
func foldNorm(w string) string {
	var b strings.Builder
	b.Grow(len(w))
	for _, r := range w {
		b.WriteRune(foldKey(r))
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// 精确自动机构建
// ---------------------------------------------------------------------------

// exactBuilder 聚集精确构建期状态：root 即 nodes[0]（下标 0 = root，其余节点
// 从 1 起，与成品 nodes 下标完全一致），root 的 children map 成品直接复用为
// rootNext，兼任跳跃判断集。命名与 foldBuilder 对偶（见文件末段）。
// 注意：insert 阶段会 append 扩容 nodes，底层数组可能重新分配，故不可跨
// append 缓存 &b.nodes[i]，一律按下标访问（BFS/flatten 阶段不再 append，
// 局部指针安全）。
type exactBuilder struct {
	nodes []exactBuilderNode
}

// exactBuilderNode 精确构建期节点：children 即自有 trie 边。
type exactBuilderNode struct {
	children  map[rune]int32
	fail      int32
	termLen   int32
	termGroup int32 // 终止关键词的组号（resolveSynonyms 的分区语义）
	outLens   []int32
	outGroups []int32 // 与 outLens 平行的组号
}

// newExactBuilder 创建只含 root（nodes[0]）的精确 builder。
func newExactBuilder(capHint int) *exactBuilder {
	b := &exactBuilder{nodes: make([]exactBuilderNode, 1, capHint+1)}
	b.nodes[0].children = make(map[rune]int32)
	return b
}

// insert 从 cur 经 rune r 下行，孩子不存在则新建（cur==0 即 root，同一路径）。
func (b *exactBuilder) insert(cur int32, r rune) int32 {
	if t, ok := b.nodes[cur].children[r]; ok {
		return t
	}
	t := int32(len(b.nodes))
	b.nodes = append(b.nodes, exactBuilderNode{children: make(map[rune]int32)})
	b.nodes[cur].children[r] = t // append 可能扩容，须重新按 cur 下标访问
	return t
}

// gotoWithFail 返回状态 s 在 rune r 上的转移目标（带失败指针回退）。
// 先查 s 的自有边，未命中沿 fail 链逐级回退重试；到 root（nodes[0]）查其
// 孩子表，仍未含则返回 0（留在 root）。构建期失败指针计算与查询期 step 同一语义。
// BFS 约束保证 s 的 fail 指向更浅节点、其 fail 已算好，回退安全。
func (b *exactBuilder) gotoWithFail(s int32, r rune) int32 {
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
func (b *exactBuilder) buildAutomaton() {
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
		// 2) 输出信息：以该状态结束的全部关键词长度（降序）与平行组号。
		//    自身关键词（若有）必最长：失败链上的关键词都是其真后缀，严格更短，
		//    因此 [termLen] ++ fail.outLens 天然严格降序且无重复。
		if nd.termLen > 0 {
			nd.outLens = append([]int32{nd.termLen}, fd.outLens...)
			nd.outGroups = append([]int32{nd.termGroup}, fd.outGroups...)
		} else {
			// 无自身输出时直接共享失败链的切片（构建后只读）
			nd.outLens = fd.outLens
			nd.outGroups = fd.outGroups
		}
	}
}

// flatten 把 trie 边一次性展平为全局 CSR 有序数组：每个非 root 节点收集
// 孩子键、排序后追加进 keys/vals，记录 base/count。root 的表不进 CSR
// （成品以 map 形式直接复用，兼任跳跃判断集）。
func (b *exactBuilder) flatten() ([]exactNode, []rune, []int32) {
	total := 0
	for i := range b.nodes {
		if i > 0 {
			total += len(b.nodes[i].children)
		}
	}
	keys := make([]rune, 0, total)
	vals := make([]int32, 0, total)
	nodes := make([]exactNode, len(b.nodes))
	for i := range b.nodes {
		if i == 0 {
			continue // root 走 map，不进 CSR
		}
		bn := &b.nodes[i]
		nd := &nodes[i]
		nd.fail = bn.fail
		nd.outLens = bn.outLens
		nd.outGroups = bn.outGroups
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

// setFilterBit 把 rune r 的 UTF-8 首字节置入字节过滤器（r 为合法关键词 rune，
// 编码必为 1–4 字节）。
func setFilterBit(bf *[32]byte, r rune) {
	var buf [utf8.UTFMax]byte
	utf8.EncodeRune(buf[:], r)
	bf[buf[0]>>3] |= 1 << (buf[0] & 7)
}

// buildExact 从去重后的关键词列表构建精确自动机（New 默认模式的实现体，
// 见 matcher.go）：part 携带同义词分组解析结果（词→组号表与 GroupWords
// 成员表；无 WithSynonyms 时为每词单元素组，见 synonymPart）。
func buildExact(keywords []string, part *synonymPart) *exactMatcher {
	b := newExactBuilder(len(keywords))
	em := &exactMatcher{}
	for _, kw := range keywords {
		cur := int32(0)
		for _, r := range kw {
			cur = b.insert(cur, r)
		}
		b.nodes[cur].termLen = int32(len(kw)) // 终止标记：关键词字节长度
		b.nodes[cur].termGroup = part.groupOf[kw]
		// 收集词库首字符字节集：首 rune 的 UTF-8 首字节（见 skipForward）
		for _, r := range kw {
			setFilterBit(&em.byteFilter, r)
			break
		}
	}
	b.buildAutomaton()
	em.nodes, em.transKeys, em.transVals = b.flatten()
	em.rootNext = b.nodes[0].children
	em.groups, em.singletons = part.groups, part.singletons
	em.words = keywords
	return em
}

// ---------------------------------------------------------------------------
// 折叠自动机构建（SimpleFold 轨道语义，见 WithCaseFold）
// ---------------------------------------------------------------------------

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
// 路径共享节点（大小写变体合一），同节点可终止多个关键词（termNrs 可含
// 等值重复，BFS 期去重；折叠同形词组号恒等，见 flushFoldTerms）。
type foldBuilder struct {
	nodes []foldBuilderNode
}

// foldBuilderNode 构建期节点：children 即自有折叠边。
type foldBuilderNode struct {
	children   map[rune]int32 // 键 = 折叠代表 rune
	fail       int32
	termNrs    []int32 // 插入期收集：以此状态结束的各关键词 rune 数（可重复）
	termGroups []int32 // 与 termNrs 平行的组号（折叠同形词组号恒等）
	outRunes   []int32 // BFS 后定型：全部输出 rune 数，严格降序无重复（见 flushFoldTerms）
	outGroups  []int32 // 与 outRunes 平行的组号
}

// buildFold 从去重后的关键词列表（折叠模式下为归一形，见 resolveSynonyms）
// 构建折叠自动机（New(WithCaseFold()) 的实现体，见 matcher.go）：part 携带
// 同义词分组解析结果。流程与精确构建相同：插词 → BFS 失败指针 → 展平 CSR；
// 差异仅在边按折叠代表合一、失败指针按折叠匹配计算、root 表/字节过滤器/
// CSR 键展开为全部轨道成员、输出为关键词 rune 数。
func buildFold(keywords []string, part *synonymPart) *foldMatcher {
	fm := &foldMatcher{}
	b := &foldBuilder{nodes: make([]foldBuilderNode, 1, len(keywords)+1)}
	b.nodes[0].children = make(map[rune]int32)
	for _, kw := range keywords {
		cur := int32(0)
		var nr int32
		for _, r := range kw {
			cur = b.insertFold(cur, r)
			nr++
		}
		b.nodes[cur].termNrs = append(b.nodes[cur].termNrs, nr)
		b.nodes[cur].termGroups = append(b.nodes[cur].termGroups, part.groupOf[kw])
	}
	b.buildFoldAutomaton()
	b.flattenFold(fm)
	fm.groups, fm.singletons = part.groups, part.singletons
	fm.words = keywords
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
	b.nodes = append(b.nodes, foldBuilderNode{children: make(map[rune]int32)})
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

// flushFoldTerms 定型节点 n 的输出：自身关键词（折叠变体去重）与失败链继承
// 合并为严格降序无重复的 rune 数数组。同节点全部输出都是规范路径 σ 的后缀
// （自身即 σ 全串）：折叠代表每位置宽 ≥1 字节，rune 数更少则字节必更少，
// 故 rune 数序与字节长序等价；按 rune 数降序排序并对重复（如 "sS"/"ss"）
// 去重即得严格降序无重复。组号随排序同步置换；去重丢弃的重复 rune 数对应
// 折叠同形词（归一形恒等，分组校验保证组号必等），任取其一。
func (b *foldBuilder) flushFoldTerms(nd, fd *foldBuilderNode) {
	if len(nd.termNrs) == 0 {
		// 无自身输出时直接共享失败链切片（构建后只读）
		nd.outRunes = fd.outRunes
		nd.outGroups = fd.outGroups
		return
	}
	type term struct {
		nr, grp int32
	}
	terms := make([]term, 0, len(nd.termNrs)+len(fd.outRunes))
	for i, nr := range nd.termNrs {
		terms = append(terms, term{nr, nd.termGroups[i]})
	}
	for i, nr := range fd.outRunes {
		terms = append(terms, term{nr, fd.outGroups[i]})
	}
	slices.SortFunc(terms, func(a, c term) int { return int(c.nr) - int(a.nr) })
	kept := terms[:0]
	for i, tm := range terms {
		if i == 0 || tm.nr != terms[i-1].nr {
			kept = append(kept, tm)
		}
	}
	nd.outRunes = make([]int32, len(kept))
	nd.outGroups = make([]int32, len(kept))
	for i, tm := range kept {
		nd.outRunes[i], nd.outGroups[i] = tm.nr, tm.grp
	}
}

// flattenFold 展平折叠自动机：CSR 每条自有边展开为其轨道全部成员（轨道互
// 不相交 → 段内仍严格升序，二分查找不受影响），目标指向同一节点；root 表
// 与字节过滤器同步展开（rootNext 含全部轨道成员，skipForward 无需归一）。
func (b *foldBuilder) flattenFold(fm *foldMatcher) {
	root := make(map[rune]int32, len(b.nodes[0].children)*2)
	for key, t := range b.nodes[0].children {
		for _, r := range foldOrbit(key) {
			root[r] = t
			setFilterBit(&fm.byteFilter, r) // 各轨道成员的 UTF-8 首字节都置位
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
	nodes := make([]foldNode, len(b.nodes))
	for i := range b.nodes {
		if i == 0 {
			continue // root 走 map，不进 CSR
		}
		bn := &b.nodes[i]
		nd := &nodes[i]
		nd.fail = bn.fail
		nd.outRunes = bn.outRunes
		nd.outGroups = bn.outGroups
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
