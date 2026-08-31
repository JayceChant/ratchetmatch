package ratchetsearch

import "slices"

// node 是自动机查询期的一个状态。转移表不直接存于节点：全局 CSR 数组中
// [base, base+count) 区间仅存该节点的自有 trie 边（键升序）。
// 未含的 rune 沿 fail 链回退重试；回退到 root 仍未含则留在 root。
type node struct {
	base    int32   // 自有边区间在 Matcher.transKeys/transVals 中的起始下标
	count   int32   // 自有边条数
	fail    int32   // 失败指针：已匹配部分的最长真后缀（且是词库中某关键词前缀）对应的节点；无则指向 root
	outLens []int32 // 以当前状态结束的全部关键词字节长度，严格降序（自身 + 失败链继承）；nil 表示无
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
