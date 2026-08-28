package ratchetsearch

import "unicode/utf8"

// step 返回状态 s 在 rune r 上的转移目标；未含则沿失败指针回退重试，
// 回退到 root 仍未含则留在 root（返回 0）。摊还 O(1)：中文词库的节点
// 自有边通常 1–几条，线性/二分段内查找极快；失败链平均 1–2 步。
func (m *Matcher) step(s int32, r rune) int32 {
	for s != 0 {
		if t := m.find(s, r); t != 0 {
			return t
		}
		s = m.nodes[s].fail
	}
	return m.rootNext[r] // root 的表（map）：未含时 map 返回 0，恰好即「留在 root」
}

// find 在节点 s 的 CSR 段内查找 rune r，返回转移目标（未含返回 0）。
// 段宽 ≤16 线性扫描（缓存友好，绝大多数段 1–4 条），>16 二分。
func (m *Matcher) find(s int32, r rune) int32 {
	nd := &m.nodes[s]
	n := nd.count
	if n == 0 {
		return 0
	}
	ks := m.transKeys[nd.base : nd.base+n]
	i := 0
	if n > 16 {
		lo, hi := 0, int(n)
		for lo < hi {
			mid := int(uint(lo+hi) >> 1)
			if ks[mid] < r {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		i = lo
	} else {
		for ; i < len(ks) && ks[i] < r; i++ {
		}
	}
	if i < len(ks) && ks[i] == r {
		return m.transVals[nd.base+int32(i)]
	}
	return 0
}

// FindAll 返回 text 中所有命中（非重叠贪心：先命中优先；同一起始位置前缀关系取最长），
// 按出现先后（Start 升序）排序；无命中或 text 为空返回 nil。
func (m *Matcher) FindAll(text string) []Match {
	var out []Match
	m.scan(text, 0, func(hit Match) bool {
		out = append(out, hit)
		return true
	})
	return out
}

// FindNext 从 offset（字节偏移）开始查找第一个命中，找到即终止扫描、不遍历剩余文本，
// 适合超长文本按需查找。无状态，可并发调用；调用方用返回的 End 作下次 offset 迭代，
// 得到的序列与 FindAll 完全一致。无命中返回 (Match{}, false)。
// offset<0 按 0 处理；offset>=len(text) 返回 false；offset 落在多字节字符中间时向后对齐到 rune 边界。
func (m *Matcher) FindNext(text string, offset int) (Match, bool) {
	if offset < 0 {
		offset = 0
	}
	n := len(text)
	if offset >= n {
		return Match{}, false
	}
	for offset < n && !utf8.RuneStart(text[offset]) {
		offset++ // 对齐 rune 边界
	}
	if offset >= n {
		return Match{}, false
	}
	var found Match
	ok := false
	m.scan(text, offset, func(hit Match) bool {
		found, ok = hit, true
		return false // 找到第一个即停止扫描
	})
	return found, ok
}

// scan 从 from 开始单遍扫描（自动机从 root 起步）；每确定一个最终命中调用 emit，
// emit 返回 false 时立即停止。文本指针单调前进、绝不回退。
// 贪心语义用 pending 机制实现：候选按结束位置先后到达（每个结束位置的候选按长度降序）：
//   - 新候选与 pending 同一起始位置（前缀关系）→ 替换（取最长）
//   - 新候选起始 >= pending 结束（不重叠）→ 提交 pending，新候选成为 pending
//   - 其余（重叠且起始更晚，或提交后更晚结束的超长串）→ 丢弃新候选（先命中优先）
func (m *Matcher) scan(text string, from int, emit func(Match) bool) {
	n := len(text)
	pos := from
	var state int32
	var pendStart, pendLen int32 // pendLen==0 表示无未提交候选
	flush := func() bool {
		if pendLen == 0 {
			return true
		}
		return emit(Match{
			Start:   int(pendStart),
			End:     int(pendStart + pendLen),
			Keyword: text[pendStart : pendStart+pendLen],
		})
	}
	for pos < n {
		if state == 0 {
			pos = m.skipForward(text, pos)
			if pos >= n {
				break
			}
		}
		r, size := utf8.DecodeRuneInString(text[pos:])
		state = m.step(state, r)
		pos += size
		// 该结束位置的全部候选按长度降序（outLens）；选第一个与 pending 兼容的：
		// 贪心语义——优先最长；若最长者与 pending 重叠则尝试更短者，
		// 使「命中后跳到结尾继续」的语义在长词遮蔽短词的场景下仍然成立
		// （如词库 {国,人,中国人} 的文本 "中国人"：先命中 "国"，随后 "中国人"
		// 与之重叠被丢弃，但同位置结束的 "人" 起始恰在其后，应当命中）。
		for _, l := range m.nodes[state].outLens {
			cs := int32(pos) - l
			switch {
			case pendLen == 0:
				pendStart, pendLen = cs, l
			case cs == pendStart:
				pendLen = l // 同一起始位置：前缀关系取最长
			case cs >= pendStart+pendLen:
				if !flush() {
					return
				}
				pendStart, pendLen = cs, l
			default:
				continue // 重叠且起始更晚：先命中优先，尝试更短候选
			}
			break
		}
	}
	flush()
}

// skipForward 在自动机处于 root 态时应用 Boyer-Moore 坏字符跳跃：
// 从 pos 起跳过不可能出现匹配起始的文本段，返回下一个可能的匹配起始位置（或 n）。
// 安全性：root 态下无任何进行中的部分匹配；任何命中的起始 rune 必为某关键词的
// 首字符，故一段全部 rune 均不在词库首字符集中的区域内不可能出现匹配起始。
// 未命中字节过滤器的字节可直接按字节跳过（无需解码）；命中过滤器的字节再解码
// rune 精确判断（词库首字符表即 root 转移表 rootNext，双重身份零额外内存）。
func (m *Matcher) skipForward(text string, pos int) int {
	n := len(text)
	for pos < n {
		b := text[pos]
		if m.byteFilter[b>>3]&(1<<(b&7)) == 0 {
			pos++ // 字节不在词库首字符字节集中 → 所属 rune 必不在首字符集中
			continue
		}
		r, size := utf8.DecodeRuneInString(text[pos:])
		if _, ok := m.rootNext[r]; ok {
			return pos
		}
		pos += size // 仅字节前缀撞上过滤器（或非法 UTF-8）：前进一个 rune 宽度，保持 root
	}
	return n
}
