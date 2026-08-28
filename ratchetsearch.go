// Package ratchetsearch 实现针对中文优化的 ACBM（Aho-Corasick + Boyer-Moore）
// 多模式匹配。
//
// 构建期建立 rune 级 Trie 与失败指针，节点转移表仅存自有 trie 边并展平进
// 全局 CSR 有序数组：查询期每处理一个 rune 先在当前状态段内查找，未命中
// 沿失败指针回退重试（平均 1–2 步，摊还 O(1)），不做 DFA 全量解析——后者
// 在中文大字符集下会使每节点表膨胀至 root 扇出，内存不可接受。自动机处于
// root 态时应用 Boyer-Moore 坏字符跳跃，按词库首字符集跳过不可能出现匹配
// 起始的文本段。
//
// 匹配语义为非重叠贪心：先命中优先；同一起始位置存在前缀关系的关键词取最长。
// Matcher 构建完成后为只读数据结构（除 root 首字符表外无 map，查询期零分配），
// 可无锁并发使用。
package ratchetsearch

import (
	"errors"
	"fmt"
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
}

// New 根据关键词列表构建 Matcher。
//
// 关键词列表不能为空；关键词本身不能为空字符串；重复关键词会被自动去重。
// 非法 UTF-8 的关键词按 rune 迭代时会统一得到 utf8.RuneError，
// 与查询端 utf8.DecodeRuneInString 的行为一致。
func New(keywords []string) (*Matcher, error) {
	if len(keywords) == 0 {
		return nil, errors.New("ratchetsearch: keyword list is empty")
	}
	for i, kw := range keywords {
		if kw == "" {
			return nil, fmt.Errorf("ratchetsearch: keyword at index %d is empty", i)
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
	m.rootNext = b.root.children
	return m, nil
}
