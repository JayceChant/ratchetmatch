package ratchetmatch

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

// FindAllOverlapping 返回 text 中全部关键词出现（含互相重叠者），服务词频
// 统计、关键词提取、索引构建等场景：每个（关键词，出现位置）输出一次，
// 不做非重叠筛选。输出按 End 升序、同一 End 按关键词长度降序（单遍扫描的
// 天然产出序，与 FindAll 的 Start 升序不同序）。无命中或 text 为空返回 nil。
//
// 开销输出敏感：时间 O(n + K)、空间 O(K)，K 为总出现数——病态词库
// （大量互为后缀的词）配高频文本时 K 可达 O(n·m)，调用方自行评估规模。
// 不提供对应的 FindNext 版本：重叠语义与「从 offset 重扫的无状态迭代」
// 不合，返回一条命中后无法不重扫地枚举与其重叠的更早出现（见 spec Non-Goals）。
// opts 可传 WithCaseFold 启用大小写折叠匹配（见 WithCaseFold）。
func (m *Matcher) FindAllOverlapping(text string, opts ...Option) []Match {
	fm := m.resolve(opts)
	var out []Match
	n := len(text)
	pos := 0
	var state int32
	for pos < n {
		if state == 0 {
			pos = fm.skipForward(text, pos) // 跳跃安全性只依赖词首字符判据，与输出模式无关
			if pos >= n {
				break
			}
		}
		r, size := utf8.DecodeRuneInString(text[pos:])
		state = fm.step(state, r)
		pos += size
		// 以 pos 结束的全部关键词（outLens 降序 = 同 End 长度降序）逐条输出；
		// fail 链的输出继承恰好就是重叠全量信息，无需任何筛选
		out = fm.appendHits(out, text, state, pos)
	}
	return out
}

// FindNext 从 offset（字节偏移）开始查找第一个命中，找到即终止扫描、不遍历剩余文本，
// 适合超长文本按需查找。无状态，可并发调用；调用方用返回的 End 作下次 offset 迭代，
// 得到的序列与 FindAll 完全一致（首条命中即 FindAll 的第一条）。无命中返回 (Match{}, false)。
// offset<0 按 0 处理；offset>=len(text) 返回 false；offset 落在多字节字符中间时向后对齐到 rune 边界。
// opts 可传 WithCaseFold 启用大小写折叠匹配（见 WithCaseFold）。
func (m *Matcher) FindNext(text string, offset int, opts ...Option) (Match, bool) {
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
	var hits []Match // 与 FindAll 同源的待提交链，一次性整链产出
	m.resolve(opts).scan(text, offset, func(hit Match) bool {
		hits = append(hits, hit)
		return false // 停止扫描；整链已在本次 emit 中给出
	})
	if len(hits) == 0 {
		return Match{}, false
	}
	return hits[0], true
}

// pendHit 是待提交链上的一个已确定区间、尚未落袋的候选命中。
type pendHit struct {
	start, end int32
}

// scan 从 from 开始单遍扫描（自动机从 root 起步）；每确定一个最终命中调用 emit，
// emit 返回 false 时立即停止。文本指针单调前进、绝不回退。
//
// 最左最长（leftmost-longest）语义经「待提交链」实现：候选按结束位置升序到达
// （同一结束位置的候选按长度降序，来自 outLens），链内起点升序、互不重叠；
// 逐候选归并规则见 mergeCandidate。提交时机：自动机回到 root 或扫描结束时
// 提交整链。root 时刻的安全性：若存在「起点 ≤ 当前 pos 而结束于其后」的候选，
// 其前缀仍是词库前缀，与 state==0 矛盾——故此刻链不会再被任何更左候选覆盖。
// 如词库 {国,人,中国人} 的文本 "中国人"："国" 先入链，"中国人"(起点更左)
// 到达时弹出 "国" 与 "人"，最终仅输出 "中国人"；文本 "中国梦" 中 "国"
// 则在断词回到 root 时结算输出。
func (m *Matcher) scan(text string, from int, emit func(Match) bool) {
	n := len(text)
	pos := from
	var state int32
	var inline [4]pendHit // 待提交链常规 ≤4 条，栈上内联零分配
	chain := inline[:0]   // 溢出（连续不重叠命中且长期不回 root）时 append 转堆
	for pos < n {
		if state == 0 { // root 循环：先提交链（安全提交点，见上）再跳跃
			if !flushChain(chain, text, emit) {
				return
			}
			chain = chain[:0]
			pos = m.skipForward(text, pos)
			if pos >= n {
				break
			}
		}
		r, size := utf8.DecodeRuneInString(text[pos:])
		state = m.step(state, r)
		pos += size
		// 以 pos 结束的候选按长度降序（outLens 降序保证更左者先处理）
		chain = m.mergeHits(chain, text, state, pos)
	}
	flushChain(chain, text, emit)
}

// mergeHits 把以 pos 结束的全部关键词候选归并进待提交链，返回新链。
// 精确自动机按 outLens 字节长直接回退起点；fold 自动机按关键词 rune 数
// 回退文本（fold 轨道成员字节宽可不同，见 runeStartBack）。
func (m *Matcher) mergeHits(chain []pendHit, text string, state int32, pos int) []pendHit {
	outs := &m.nodes[state]
	if !m.folded { // 精确自动机：字节长即文本消耗
		for _, l := range outs.outLens {
			chain = mergeCandidate(chain, int32(pos)-l, int32(pos))
		}
		return chain
	}
	for i := range outs.outLens {
		start := runeStartBack(text, pos, int(outs.outRunes[i]))
		chain = mergeCandidate(chain, int32(start), int32(pos))
	}
	return chain
}

// appendHits 把以 pos 结束的全部关键词命中追加进 out（FindAllOverlapping
// 全量输出路径，顺序 = outLens 降序）。fold 语义同 mergeHits。
func (m *Matcher) appendHits(out []Match, text string, state int32, pos int) []Match {
	outs := &m.nodes[state]
	if !m.folded {
		for _, l := range outs.outLens {
			out = append(out, Match{
				Start:   pos - int(l),
				End:     pos,
				Keyword: text[pos-int(l) : pos],
			})
		}
		return out
	}
	for i := range outs.outLens {
		start := runeStartBack(text, pos, int(outs.outRunes[i]))
		out = append(out, Match{
			Start:   start,
			End:     pos,
			Keyword: text[start:pos],
		})
	}
	return out
}

// runeStartBack 从 pos（rune 边界）向前回退 n 个 rune，返回起点字节下标。
// 前提：pos 之前至少存在 n 个完整 rune（自动机不变量：命中关键词的 rune 数
// 不超过扫描已消耗的 rune 数）。
func runeStartBack(text string, pos, n int) int {
	for range n {
		pos--
		for !utf8.RuneStart(text[pos]) {
			pos--
		}
	}
	return pos
}

// flushChain 在安全提交点把待提交链整链落袋（链内起点升序即输出序）：
// 逐条调用 emit，emit 返回 false 时中止并返回 false；全部提交完返回 true。
// 调用方负责随后清空链（中断时链作废，无需保留）。
func flushChain(chain []pendHit, text string, emit func(Match) bool) bool {
	for _, p := range chain {
		if !emit(Match{
			Start:   int(p.start),
			End:     int(p.end),
			Keyword: text[p.start:p.end],
		}) {
			return false
		}
	}
	return true
}

// mergeCandidate 把候选 [cs,ce) 归并进待提交链，返回新链。候选按结束位置升序
// 到达（同一结束位置按长度降序），链内起点升序、互不重叠。与链比较（k 为弹出
// 后基准下标，chain[:k] 为保留前缀）：
//   - cs < 链尾起点：候选更左，弹出链尾后继续向链左比较；链空则候选取代全部弹出者
//   - cs == 基准起点：取更长（真包含关系一律输出最长），取代被弹出者
//   - cs >= 基准结束（不重叠）：入链接续，取代被弹出者
//   - 其余（与基准重叠且起点更晚）：候选必被遮蔽——必死候选无权改变链，
//     被弹出者原样恢复。若允许其弹链，会出现「为必死候选让位而丢弃本可
//     提交的命中」的空档（如 {0,000}+"000000000001" 在 [9,10) 的空档），
//     破坏最左最长语义，且无状态 FindNext 永远无法复现该空档。
func mergeCandidate(chain []pendHit, cs, ce int32) []pendHit {
	k := len(chain)
	for k > 0 && cs < chain[k-1].start {
		k-- // 候选更左：弹出链尾，继续向链左比较
	}
	if k == 0 {
		return append(chain[:k], pendHit{cs, ce}) // 候选最左，取代全部弹出者
	}
	t := &chain[k-1]
	switch {
	case cs >= t.end: // 不重叠：入链，取代被弹出者
		return append(chain[:k], pendHit{cs, ce})
	case cs == t.start:
		if ce > t.end { // 同起点取更长（真包含取最长）
			t.end = ce
		}
		return chain[:k] // 取代被弹出者
	default: // 与基准重叠且起点更晚：必死候选，链原样保留（弹出者恢复）
		return chain
	}
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
