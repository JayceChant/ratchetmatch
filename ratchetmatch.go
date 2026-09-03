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
// 词（真包含关系一律输出最长）。Matcher 构建完成后为只读数据结构（除 root
// 首字符表外无 map，查询期零分配），可无锁并发使用。
//
// Find 系列均带变参 opts ...Option：不传即精确匹配（默认行为）；传
// WithCaseFold 时按 Unicode SimpleFold 轨道折叠匹配，折叠自动机在首次
// fold 查询时惰性构建（一次性，之后只读，并发安全），详见 WithCaseFold。
package ratchetmatch

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"
)

// Match 表示一次关键词命中。Start/End 为 text 中的字节偏移，
// [Start,End) 半开区间，且 text[Start:End] == Keyword。
type Match struct {
	Start   int
	End     int
	Keyword string
}

// Matcher 是构建完成的多模式匹配自动机（node 定义在 build.go）。
// 转移表为全局 CSR 数组（transKeys/transVals 按节点 base/count 区间分段），
// 构建后只读，可无锁并发使用。
type Matcher struct {
	nodes      []node
	transKeys  []rune         // 全局转移表：仅自有 trie 边，段内 rune 升序，段与节点一一对应
	transVals  []int32        // 全局转移表：与 transKeys 平行的转移目标（恒非 0）
	rootNext   map[rune]int32 // root 的转移表 = 词库首字符表，兼任跳跃判断集（见 skipForward）
	byteFilter [32]byte

	// fold 惰性子结构（仅 WithCaseFold 查询使用）：首次 fold 查询时构建
	// 一次，之后只读。froot 是独立完整自动机（节点表/CSR/首字符表/过滤器），
	// 查询热路径与精确模式共用同一套代码。
	once   sync.Once
	froot  *Matcher
	folded bool // 本实例为折叠自动机（经 buildFold 构建，命中区间按 rune 数回退）
}

// Option 定制一次 Find 系列查询的行为。零值不改变默认行为。
type Option func(*queryOpts)

// queryOpts 聚合一次查询的全部选项（变参展开结果）。
type queryOpts struct {
	caseFold bool
}

// WithCaseFold 使本次查询按 Unicode SimpleFold 轨道折叠匹配（strings.EqualFold
// 语义）：文本 rune 与关键词 rune fold 相等即视为同一字符，"Hello" 可命中
// 关键词 "hello"。大小写变体关键词在折叠自动机中合一，不漏报。
//
// 折叠自动机在首次 fold 查询时在同一 Matcher 内惰性构建（一次性，此后只读，
// 并发 fold 查询经 sync.Once 串行等待）；精确查询不受任何影响。命中区间的
// Keyword 为文本原样切片（如关键词 "hello" 命中文本 "Hello" 时 Keyword 为
// "Hello"），Start/End 仍可直接切文本。
func WithCaseFold() Option {
	return func(o *queryOpts) { o.caseFold = true }
}

// resolve 展开变参选项；fold 查询返回惰性构建（仅首次）的折叠自动机。
func (m *Matcher) resolve(opts []Option) *Matcher {
	var o queryOpts
	for _, opt := range opts {
		opt(&o)
	}
	if !o.caseFold {
		return m
	}
	m.once.Do(func() { m.froot = buildFold(m) })
	return m.froot
}

// New 根据关键词列表构建 Matcher。
//
// 关键词列表不能为空；关键词本身不能为空字符串；重复关键词会被自动去重。
// 关键词必须是合法 UTF-8 且不得包含 U+FFFD（替换字符）：非法字节在 rune 层
// 坍缩为同一个 RuneError（身份歧义），U+FFFD 的 3 字节编码与查询端逐字节
// 前进不一致（长度歧义），二者均会使命中区间或关键词身份失去健全语义，
// 故显式拒绝（详见 spec 词库校验需求）。文本侧的非法字节不受影响，
// 扫描时按 RuneError 逐字节处理，不 panic、不漏扫。
func New(keywords []string) (*Matcher, error) {
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
	b := newBuilder(len(keywords))
	m := &Matcher{}
	for _, kw := range keywords {
		if _, dup := seen[kw]; dup {
			continue
		}
		seen[kw] = struct{}{}
		cur := int32(0)
		for _, r := range kw {
			cur = b.insert(cur, r)
		}
		b.nodes[cur].termLen = int32(len(kw)) // 终止标记：关键词字节长度
		// 收集词库首字符字节集（256 位位图），跳跃用（见 skipForward）。
		m.byteFilter[kw[0]>>3] |= 1 << (kw[0] & 7)
	}
	b.buildAutomaton()
	m.nodes, m.transKeys, m.transVals = b.flatten()
	m.rootNext = b.nodes[0].children
	return m, nil
}

// FindAll 返回 text 中所有命中（非重叠最左最长：起点最小优先；同一起点前缀关系取最长），
// 按出现先后（Start 升序）排序；无命中或 text 为空返回 nil。
// opts 可传 WithCaseFold 启用大小写折叠匹配（见 WithCaseFold）。
func (m *Matcher) FindAll(text string, opts ...Option) []Match {
	var out []Match
	m.resolve(opts).scan(text, 0, func(hit Match) bool {
		out = append(out, hit)
		return true
	})
	return out
}
