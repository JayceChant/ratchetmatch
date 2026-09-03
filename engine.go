// 本文件为两套自动机实现的查询期核心：nodeAPI 约束接口、exactNode/foldNode
// 节点、machine[N] 泛型扫描引擎与最左最长待提交链。两套自动机对外形态
// （实现导出的 Matcher 接口）见本文件末段；构建管线见 build.go，公共 API
// 见 matcher.go，选项见 option.go。
package ratchetmatch

import "unicode/utf8"

// nodeAPI 约束两套自动机的节点行为：转移布局 seg 一致（CSR 段查找与 fail
// 回退共用同一引擎逻辑），命中起点 start 各异——精确节点按关键词字节长回退，
// 折叠节点按关键词 rune 数回退（折叠轨道成员的 UTF-8 字节宽可不同，见
// runeStartBack）。节点以值存储、值接收者实现方法：exactNode/foldNode 布局
// 不同，泛型按 gcshape 各自单态化，节点方法在热路径编译为直接调用，零接口
// 开销。
type nodeAPI interface {
	// seg 返回节点的转移布局：自有边区间在 transKeys/transVals 中的起始下标
	// base 与条数 count（段内键升序，供 find 线性/二分查找），以及失败指针
	// fail（未命中时沿其回退重试，root 为 0）。
	seg() (base, count, fail int32)
	// outs 返回以该状态结束的输出条数；0 表示无输出（叶子或仅继承失败链的
	// 节点），此时 start 不会被调用。
	outs() int
	// start 返回以 pos 结尾的第 i 个输出的命中起点（字节下标，rune 边界）：
	// 精确节点按关键词字节长回退，折叠节点按关键词 rune 数回退（轨道成员
	// 字节宽可不同，见 runeStartBack）。text 为被扫描文本。
	start(text string, pos, i int) int
	// group 返回第 i 个输出的同义词组号（与 outLens/outRunes 平行的 outGroups
	// 下标，见 buildPartition 的分区语义）。
	group(i int) int32
}

// exactNode 是精确自动机的查询期状态：outLens 为以当前状态结束的全部关键词
// 字节长度，严格降序（自身 + 失败链继承；自身必为真后缀关键词的最长者）；
// outGroups 与 outLens 平行的组号。nil 表示无输出。
// 命中起点 = pos − 关键词字节长（文本消耗恒等于关键词字节长）。
type exactNode struct {
	base      int32   // 自有边区间在 transKeys/transVals 中的起始下标
	count     int32   // 自有边条数
	fail      int32   // 失败指针：已匹配部分的最长真后缀（且是词库中某关键词前缀）对应的节点；无则指向 root
	outLens   []int32 // 全部输出关键词字节长，严格降序；nil 表示无
	outGroups []int32 // 与 outLens 平行的组号（分区语义见 resolveSynonyms / build.go）
}

func (n exactNode) seg() (base, count, fail int32) { return n.base, n.count, n.fail }
func (n exactNode) outs() int                      { return len(n.outLens) }
func (n exactNode) start(_ string, pos, i int) int { return pos - int(n.outLens[i]) }
func (n exactNode) group(i int) int32              { return n.outGroups[i] }

// foldNode 是折叠自动机的查询期状态：outRunes 为以当前状态结束的全部关键词
// rune 数，严格降序且无重复（自身 + 失败链继承；同节点多个折叠变体关键词
// 只保留一条）；outGroups 与 outRunes 平行的组号（折叠同形词组号恒等）。
// 命中起点 = 从 pos 按 rune 数向前回退（轨道成员字节宽可不同，
// 关键词字节长不可直接用作文本消耗，见 runeStartBack）。
type foldNode struct {
	base      int32   // 同 exactNode
	count     int32   // 同 exactNode
	fail      int32   // 同 exactNode
	outRunes  []int32 // 全部输出关键词 rune 数，严格降序无重复；nil 表示无
	outGroups []int32 // 与 outRunes 平行的组号；同 rune 数的折叠变体组号恒等
}

func (n foldNode) seg() (base, count, fail int32) { return n.base, n.count, n.fail }
func (n foldNode) outs() int                      { return len(n.outRunes) }
func (n foldNode) start(text string, pos, i int) int {
	return runeStartBack(text, pos, int(n.outRunes[i]))
}
func (n foldNode) group(i int) int32 { return n.outGroups[i] }

// machine 是两套自动机共用的泛型扫描引擎。转移表为全局 CSR 数组
// （transKeys/transVals 按节点 base/count 区间分段），节点仅存自有 trie 边
// （键升序）：查询期每处理一个 rune 先在当前状态段内查找，未命中沿失败指针
// 回退重试（摊还 O(1)），不做 DFA 全量解析；自动机处于 root 态时应用
// Boyer-Moore 坏字符跳跃，按词库首字符集跳过不可能出现匹配起始的文本段
// （rootNext 兼任首字符表与跳跃判断集，见 skipForward）。
type machine[N nodeAPI] struct {
	nodes      []N
	transKeys  []rune         // 全局转移表：仅自有 trie 边，段内 rune 升序，段与节点一一对应
	transVals  []int32        // 全局转移表：与 transKeys 平行的转移目标（恒非 0）
	rootNext   map[rune]int32 // root 的转移表 = 词库首字符表，兼任跳跃判断集（见 skipForward）
	byteFilter [32]byte       // 词首 rune 的 UTF-8 首字节 256 位位图（见 skipForward）
	groups     [][]string     // WithSynonyms 声明组成员表（nil = 未使用；GroupWords 前段）
	singletons []int32        // 未声明分组的词库下标表：组号 = len(groups)+序号（GroupWords 后段）
	words      []string       // 去重词库（折叠模式为归一形）；singletons 的下标依据，兼查原始词
}

// ---------------------------------------------------------------------------
// 导出接口的两套实现：类型即模式（exact / fold），构建即定型，无运行时分支。
// ---------------------------------------------------------------------------

// exactMatcher 是大小写敏感（精确）自动机，实现导出的 Matcher 接口；
// 由 New 不带选项时返回（见 buildExact）。
type exactMatcher struct {
	machine[exactNode]
}

// CaseFold 报告自动机是否为折叠模式；exactMatcher 恒为 false。
func (*exactMatcher) CaseFold() bool { return false }

// isInternal 实现 Matcher 接口的密封方法：仅本包可提供实现，保障接口演进自由。
func (*exactMatcher) isInternal() {}

// internal 实现导出接口的密封方法（见 Matcher）。

// foldMatcher 是大小写折叠自动机（SimpleFold 轨道语义，见 WithCaseFold），
// 实现导出的 Matcher 接口；由 New(WithCaseFold()) 返回（见 buildFold）。
type foldMatcher struct {
	machine[foldNode]
}

// CaseFold 报告自动机是否为折叠模式；foldMatcher 恒为 true。
func (*foldMatcher) CaseFold() bool { return true }

// isInternal 实现 Matcher 接口的密封方法：仅本包可提供实现，保障接口演进自由。
func (*foldMatcher) isInternal() {}

// step 返回状态 s 在 rune r 上的转移目标；未含则沿失败指针回退重试，
// 回退到 root 仍未含则留在 root（返回 0）。摊还 O(1)：中文词库的节点
// 自有边通常 1–几条，线性/二分段内查找极快；失败链平均 1–2 步。
func (m *machine[N]) step(s int32, r rune) int32 {
	for s != 0 {
		if t := m.find(s, r); t != 0 {
			return t
		}
		_, _, f := m.nodes[s].seg()
		s = f
	}
	return m.rootNext[r] // root 的表（map）：未含时 map 返回 0，恰好即「留在 root」
}

// find 在节点 s 的 CSR 段内查找 rune r，返回转移目标（未含返回 0）。
// 段宽 ≤16 线性扫描（缓存友好，绝大多数段 1–4 条），>16 二分。
func (m *machine[N]) find(s int32, r rune) int32 {
	base, count, _ := m.nodes[s].seg()
	if count == 0 {
		return 0
	}
	ks := m.transKeys[base : base+count]
	i := 0
	if count > 16 {
		lo, hi := 0, int(count)
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
		return m.transVals[base+int32(i)]
	}
	return 0
}

// skipForward 在自动机处于 root 态时应用 Boyer-Moore 坏字符跳跃：
// 从 pos 起跳过不可能出现匹配起始的文本段，返回下一个可能的匹配起始位置（或 n）。
// 安全性：root 态下无任何进行中的部分匹配；任何命中的起始 rune 必为某关键词的
// 首字符，故一段全部 rune 均不在词库首字符集中的区域内不可能出现匹配起始。
// 未命中字节过滤器的字节可直接按字节跳过（无需解码）；命中过滤器的字节再解码
// rune 精确判断（折叠自动机的过滤器已展开全部轨道成员，判据不变，见 build.go）。
func (m *machine[N]) skipForward(text string, pos int) int {
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

// GroupWords 返回同义词组 g 的成员词：WithSynonyms 声明组（折叠模式为归一
// 形）或未声明分组的单元素组（resolveSynonyms 编号规则：声明组在前，其后
// 每个词库词一个组）。返回内部只读切片，调用方不得修改；越界组号返回 nil。
func (m *machine[N]) GroupWords(g int) []string {
	if g < 0 {
		return nil
	}
	if g < len(m.groups) {
		return m.groups[g]
	}
	if si := g - len(m.groups); si < len(m.singletons) {
		i := m.singletons[si]
		return m.words[i : i+1]
	}
	return nil
}

// pendHit 是待提交链上的一个已确定区间、尚未落袋的候选命中。
type pendHit struct {
	start, end int32
	group      int32 // 命中词组号：随区间一并暂存，提交时落入 Match.Group
}

// scan 从 from 开始单遍扫描（自动机从 root 起步）；每确定一个最终命中调用 emit，
// emit 返回 false 时立即停止。文本指针单调前进、绝不回退。
//
// 最左最长（leftmost-longest）语义经「待提交链」实现：候选按结束位置升序到达
// （同一结束位置的候选按长度降序，来自 outLens/outRunes 的降序），链内起点
// 升序、互不重叠；逐候选归并规则见 mergeCandidate。提交时机：自动机回到 root
// 或扫描结束时提交整链。root 时刻的安全性：若存在「起点 ≤ 当前 pos 而结束于
// 其后」的候选，其前缀仍是词库前缀，与 state==0 矛盾——故此刻链不会再被任何
// 更左候选覆盖。如词库 {国,人,中国人} 的文本 "中国人"："国" 先入链，
// "中国人"(起点更左) 到达时弹出 "国" 与 "人"，最终仅输出 "中国人"；
// 文本 "中国梦" 中 "国" 则在断词回到 root 时结算输出。
func (m *machine[N]) scan(text string, from int, emit func(Match) bool) {
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
		// 以 pos 结束的候选按长度降序（保证更左者先处理）
		chain = m.mergeCandidates(chain, text, state, pos)
	}
	flushChain(chain, text, emit)
}

// mergeCandidates 把以 pos 结束的全部关键词候选归并进待提交链，返回新链
// （起点计算由节点类型决定，见 nodeAPI.start；组号经 outGroups 平行取出）。
func (m *machine[N]) mergeCandidates(chain []pendHit, text string, s int32, pos int) []pendHit {
	nd := m.nodes[s]
	for i := range nd.outs() {
		chain = mergeCandidate(chain, int32(nd.start(text, pos, i)), int32(pos), nd.group(i))
	}
	return chain
}

// appendAll 把以 pos 结束的全部关键词命中追加进 out（FindAllOverlapping
// 全量输出路径，顺序 = 输出数组降序约定）。
func (m *machine[N]) appendAll(out []Match, text string, s int32, pos int) []Match {
	nd := m.nodes[s]
	for i := range nd.outs() {
		start := nd.start(text, pos, i)
		out = append(out, Match{
			Start:   start,
			End:     pos,
			Keyword: text[start:pos],
			Group:   int(nd.group(i)),
		})
	}
	return out
}

// FindAll 返回 text 中所有命中（非重叠最左最长），按 Start 升序排序；
// 无命中或 text 为空返回 nil。
func (m *machine[N]) FindAll(text string) []Match {
	var out []Match
	m.scan(text, 0, func(hit Match) bool {
		out = append(out, hit)
		return true
	})
	return out
}

// FindAllOverlapping 返回 text 中全部关键词出现（含互相重叠者）：每个
// （关键词，出现位置）输出一次，不做非重叠筛选。输出按 End 升序、同一 End
// 按长度降序（单遍扫描的天然产出序）。无命中或 text 为空返回 nil。
func (m *machine[N]) FindAllOverlapping(text string) []Match {
	var out []Match
	n := len(text)
	pos := 0
	var state int32
	for pos < n {
		if state == 0 {
			pos = m.skipForward(text, pos) // 跳跃安全性只依赖词首字符判据，与输出模式无关
			if pos >= n {
				break
			}
		}
		r, size := utf8.DecodeRuneInString(text[pos:])
		state = m.step(state, r)
		pos += size
		// 以 pos 结束的全部关键词逐条输出；fail 链的输出继承恰好就是
		// 重叠全量信息，无需任何筛选
		out = m.appendAll(out, text, state, pos)
	}
	return out
}

// FindNext 从 offset（字节偏移）开始查找第一个命中，找到即终止扫描；
// 无命中返回 (Match{}, false)。offset<0 按 0 处理；offset>=len(text) 返回
// false；offset 落在多字节字符中间时向后对齐到 rune 边界。
func (m *machine[N]) FindNext(text string, offset int) (Match, bool) {
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
	m.scan(text, offset, func(hit Match) bool {
		hits = append(hits, hit)
		return false // 停止扫描；整链已在本次 emit 中给出
	})
	if len(hits) == 0 {
		return Match{}, false
	}
	return hits[0], true
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
			Group:   int(p.group),
		}) {
			return false
		}
	}
	return true
}

// mergeCandidate 把候选 [cs,ce)（组号 g）归并进待提交链，返回新链。候选按
// 结束位置升序到达（同一结束位置按长度降序），链内起点升序、互不重叠。与链
// 比较（k 为弹出后基准下标，chain[:k] 为保留前缀）：
//   - cs < 链尾起点：候选更左，弹出链尾后继续向链左比较；链空则候选取代全部弹出者
//   - cs == 基准起点：取更长（真包含关系一律输出最长），取代被弹出者
//   - cs >= 基准结束（不重叠）：入链接续，取代被弹出者
//   - 其余（与基准重叠且起点更晚）：候选必被遮蔽——必死候选无权改变链，
//     被弹出者原样恢复。若允许其弹链，会出现「为必死候选让位而丢弃本可
//     提交的命中」的空档（如 {0,000}+"000000000001" 在 [9,10) 的空档），
//     破坏最左最长语义，且无状态 FindNext 永远无法复现该空档。
//
// 组号随候选一并进出链：取代场景取候选的 g（同起点取更长时跨度对应更长
// 关键词，组号随跨度身份转移）；链保留场景自动恢复被弹出者的组号。
func mergeCandidate(chain []pendHit, cs, ce, g int32) []pendHit {
	k := len(chain)
	for k > 0 && cs < chain[k-1].start {
		k-- // 候选更左：弹出链尾，继续向链左比较
	}
	if k == 0 {
		return append(chain[:k], pendHit{cs, ce, g}) // 候选最左，取代全部弹出者
	}
	t := &chain[k-1]
	switch {
	case cs >= t.end: // 不重叠：入链，取代被弹出者
		return append(chain[:k], pendHit{cs, ce, g})
	case cs == t.start:
		if ce > t.end { // 同起点取更长（真包含取最长）：组号随更长关键词转移
			t.end = ce
			t.group = g
		}
		return chain[:k] // 取代被弹出者
	default: // 与基准重叠且起点更晚：必死候选，链原样保留（弹出者恢复）
		return chain
	}
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
