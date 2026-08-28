package ratchetsearch

// node 是自动机的一个状态。构建完成后 next 为全量转移表：
// 任意 rune 查 next 命中即转移；未命中一律转移到 root（索引 0）。
type node struct {
	next    map[rune]int32
	fail    int32   // 失败指针：已匹配部分的最长真后缀（且是词库中某关键词前缀）对应的节点；无则指向 root
	termLen int32   // 恰好在当前状态结束的关键词字节长度，0 表示无（终止标记，保证完整匹配）
	outLens []int32 // 以当前状态结束的全部关键词字节长度，严格降序（自身 + 失败链继承）；nil 表示无
}

// buildAutomaton 用 BFS 计算失败指针与输出信息，并把失败指针解析进全量转移表。
// BFS 按层处理，处理某节点时其失败指针指向的更浅节点必已完成解析。
func (m *Matcher) buildAutomaton() {
	queue := make([]int32, 0, len(m.nodes))
	for _, c := range m.nodes[0].next { // root 的孩子 fail 一律为 root
		m.nodes[c].fail = 0
		queue = append(queue, c)
	}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		nd := &m.nodes[n]
		fd := &m.nodes[nd.fail]
		// 1) 先用自己的孩子（此刻 nd.next 还没被合并覆盖）设置孩子们的失败指针：
		//    fail(child(n,r)) = δ(fail(n), r)；fail(n) 更浅、其 next 已是解析后的全量表
		for r, c := range nd.next {
			if t, ok := fd.next[r]; ok {
				m.nodes[c].fail = t
			} else {
				m.nodes[c].fail = 0
			}
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
		// 3) 解析全量转移：merged = fail 的已解析表 ∪ 自己的孩子（孩子优先覆盖）
		//    查询期未命中的 rune 一律回 root（等价 DFA 的完备转移）
		if len(nd.next) == 0 {
			nd.next = fd.next // 叶子节点直接共享 fail 的表（构建后只读，安全省内存）
		} else {
			merged := make(map[rune]int32, len(fd.next)+len(nd.next))
			for k, v := range fd.next {
				merged[k] = v
			}
			for k, v := range nd.next {
				merged[k] = v
			}
			nd.next = merged
		}
	}
}
