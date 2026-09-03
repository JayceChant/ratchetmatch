// Package ratchetmatch 实现针对中文优化的 ACBM（Aho-Corasick + Boyer-Moore）
// 多模式匹配。名字中的 ratchet（棘轮）指文本指针单向只进不退的单遍扫描。
//
// 构建期建立 rune 级 Trie 与失败指针，节点转移表仅存自有 trie 边并展平进
// 全局 CSR 有序数组：查询期每处理一个 rune 先在当前状态段内查找，未命中
// 沿失败指针回退重试（平均 1–2 步，摊还 O(1)），不做 DFA 全量解析——后者
// 在中文大字符集下会使每节点表膨胀至 root 扇出，内存不可接受。自动机处于
// root 态时应用 Boyer-Moore 坏字符跳跃，按词库首字符集跳过不可能出现匹配
// 起始的文本段。
//
// 匹配语义为非重叠最左最长：起点最小者优先，同一起点取完整出现的最长关键
// 词（真包含关系一律输出最长）。
//
// 内部为两套独立自动机：精确（exactMatcher）与折叠（foldMatcher，SimpleFold
// 轨道语义），共用同一套泛型扫描引擎（machine，见 search.go），命中区间
// 提取的差异由节点类型分别实现。对外统一收敛在导出的 Matcher 上：
//   - New(keywords)：仅构建精确自动机；折叠自动机在首次 WithCaseFold 查询
//     时惰性构建（一次性，之后只读，并发安全）；
//   - New(keywords, WithCaseFold())：仅构建折叠自动机（fold-only），后续所有
//     查询固定按折叠语义执行且无法关闭。
package ratchetmatch

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"
)

// Match 表示一次关键词命中。Start/End 为 text 中的字节偏移，
// [Start,End) 半开区间，且 text[Start:End] == Keyword（fold 查询时 Keyword
// 为文本原样切片，即实际命中的大小写形态）。
type Match struct {
	Start   int
	End     int
	Keyword string
}

// Matcher 是构建完成的多模式匹配自动机，内部持有两套私有自动机实现：
// exact 精确自动机（New 默认构建）、fold 折叠自动机（fold-only 模式在
// New 时构建，否则首次 WithCaseFold 查询经 once 惰性构建一次）。两套
// 自动机构建完成后均只读，可无锁并发使用。
type Matcher struct {
	exact    *exactMatcher // 精确自动机；fold-only 构建时为 nil
	fold     *foldMatcher  // 折叠自动机；未构建时为 nil
	once     sync.Once     // 串行化惰性构建（仅精确模式使用）
	foldOnly bool          // New(WithCaseFold)：所有查询固定走 fold，无法关闭
}

// Option 定制 New 与 Find 系列调用的行为。零值不改变默认行为。
type Option func(*queryOpts)

// queryOpts 聚合一次调用的全部选项（变参展开结果）。
type queryOpts struct {
	caseFold bool
}

// WithCaseFold 启用大小写折叠匹配（strings.EqualFold 语义：逐 rune 的
// SimpleFold 轨道等价），"Hello" 可命中关键词 "hello"。大小写变体关键词
// 在折叠自动机中合一，不漏报；无展开式折叠（ß 不匹配 ss）。
//
// 用法一（惰性）：m, _ := New(kws); m.FindAll(text, WithCaseFold())——首次
// fold 查询在同一 Matcher 内构建折叠自动机（一次性，之后只读，并发 fold
// 查询经 sync.Once 串行等待），精确查询不受任何影响。
// 用法二（fold-only）：m, _ := New(kws, WithCaseFold())——仅构建折叠自动机，
// 后续所有查询（含不传选项的）固定按折叠语义执行且无法关闭；精确查询需求
// 另建 Matcher。选项对象建议提升为包级变量复用（如 var fold =
// ratchetmatch.WithCaseFold()），避免每次调用分配闭包。
// 命中区间的 Keyword 为文本原样切片（如关键词 "hello" 命中文本 "Hello" 时
// Keyword 为 "Hello"），Start/End 仍可直接切文本。
func WithCaseFold() Option {
	return func(o *queryOpts) { o.caseFold = true }
}

// wantsFold 判定一次变参调用是否要求折叠语义。
func wantsFold(opts []Option) bool {
	var o queryOpts
	for _, opt := range opts {
		opt(&o)
	}
	return o.caseFold
}

// foldEngine 返回折叠自动机；惰性模式下首次调用构建一次（并发安全，其它
// goroutine 等待构建完成后共同使用只读结果）。构建必须无条件经 once.Do：
// Once 的快速路径自身原子且提供 happens-before，绕过它读 m.fold 会引入
// 数据竞争。词库由精确 trie 无损还原（trieKeywords），不在 Matcher 中驻留。
func (m *Matcher) foldEngine() *foldMatcher {
	m.once.Do(func() {
		if m.fold == nil { // fold-only 构建的 Matcher 不会走到这里，防御兜底
			m.fold = buildFold(trieKeywords(m.exact))
		}
	})
	return m.fold
}

// New 根据关键词列表构建 Matcher。
//
// 关键词列表不能为空；关键词本身不能为空字符串；重复关键词会被自动去重。
// 关键词必须是合法 UTF-8 且不得包含 U+FFFD（替换字符）：非法字节在 rune 层
// 坍缩为同一个 RuneError（身份歧义），U+FFFD 的 3 字节编码与查询端逐字节
// 前进不一致（长度歧义），二者均会使命中区间或关键词身份失去健全语义，
// 故显式拒绝（详见 spec 词库校验需求）。文本侧的非法字节不受影响，
// 扫描时按 RuneError 逐字节处理，不 panic、不漏扫。
//
// opts 可传 WithCaseFold：此时仅构建折叠自动机（fold-only），后续所有查询
// 固定按折叠语义执行且无法关闭；不传则仅构建精确自动机，折叠自动机留待
// 首次 WithCaseFold 查询时惰性构建（见 WithCaseFold）。
func New(keywords []string, opts ...Option) (*Matcher, error) {
	if len(keywords) == 0 {
		return nil, errors.New("ratchetmatch: keyword list is empty")
	}
	for i, kw := range keywords {
		if kw == "" {
			return nil, fmt.Errorf("ratchetmatch: keyword at index %d is empty", i)
		}
		if !utf8.ValidString(kw) {
			return nil, fmt.Errorf("ratchetmatch: keyword at index %d is not valid UTF-8", i)
		}
		if strings.Contains(kw, "\uFFFD") {
			return nil, fmt.Errorf("ratchetmatch: keyword at index %d contains U+FFFD (replacement character)", i)
		}
	}
	seen := make(map[string]struct{}, len(keywords)) // 构建期临时去重表
	kws := make([]string, 0, len(keywords))
	for _, kw := range keywords {
		if _, dup := seen[kw]; dup {
			continue
		}
		seen[kw] = struct{}{}
		kws = append(kws, kw)
	}
	if wantsFold(opts) {
		return &Matcher{fold: buildFold(kws), foldOnly: true}, nil
	}
	return &Matcher{exact: buildExact(kws)}, nil
}

// FindAll 返回 text 中所有命中（非重叠最左最长：起点最小优先；同一起点前缀
// 关系取最长），按出现先后（Start 升序）排序；无命中或 text 为空返回 nil。
// opts 可传 WithCaseFold 启用大小写折叠匹配（见 WithCaseFold）。
func (m *Matcher) FindAll(text string, opts ...Option) []Match {
	if m.foldOnly || wantsFold(opts) {
		return m.foldEngine().findAll(text)
	}
	return m.exact.findAll(text)
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
	if m.foldOnly || wantsFold(opts) {
		return m.foldEngine().findAllOverlapping(text)
	}
	return m.exact.findAllOverlapping(text)
}

// FindNext 从 offset（字节偏移）开始查找第一个命中，找到即终止扫描、不遍历
// 剩余文本，适合超长文本按需查找。无状态，可并发调用；调用方用返回的 End
// 作下次 offset 迭代，得到的序列与 FindAll 完全一致（首条命中即 FindAll 的
// 第一条）。无命中返回 (Match{}, false)。offset<0 按 0 处理；
// offset>=len(text) 返回 false；offset 落在多字节字符中间时向后对齐到 rune
// 边界。opts 可传 WithCaseFold 启用大小写折叠匹配（见 WithCaseFold）。
func (m *Matcher) FindNext(text string, offset int, opts ...Option) (Match, bool) {
	if m.foldOnly || wantsFold(opts) {
		return m.foldEngine().findNext(text, offset)
	}
	return m.exact.findNext(text, offset)
}
