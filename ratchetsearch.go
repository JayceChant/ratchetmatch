// Package ratchetsearch 实现针对中文优化的 ACBM（Aho-Corasick + Boyer-Moore）
// 多模式匹配。
//
// 构建期把 Aho-Corasick 的失败指针解析为全量转移自动机：查询期每处理一个
// rune 只需一次 O(1) map 查找即可完成状态转移，未命中的 rune 一律回到 root，
// 没有运行时的失败指针回退链。自动机处于 root 态时应用 Boyer-Moore 坏字符
// 跳跃，跳过不可能出现匹配起始的文本段。
//
// 匹配语义为非重叠贪心：先命中优先；同一起始位置存在前缀关系的关键词取最长。
// Matcher 构建完成后为只读数据结构，可无锁并发使用。
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
// 构建完成后只读，可无锁并发使用。
type Matcher struct {
	nodes      []node
	runeSet    map[rune]struct{}
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
	seen := make(map[string]struct{}, len(keywords))
	m := &Matcher{
		nodes:   make([]node, 1, len(keywords)+1),
		runeSet: make(map[rune]struct{}, len(keywords)),
	}
	// root 位于索引 0，next 为空表（构建后由 buildAutomaton 保证完备语义）。
	m.nodes[0].next = make(map[rune]int32)
	for _, kw := range keywords {
		if _, dup := seen[kw]; dup {
			continue
		}
		seen[kw] = struct{}{}
		cur := int32(0)
		for _, r := range kw {
			m.runeSet[r] = struct{}{}
			if t, ok := m.nodes[cur].next[r]; ok {
				cur = t
				continue
			}
			t := int32(len(m.nodes))
			m.nodes = append(m.nodes, node{next: make(map[rune]int32)})
			m.nodes[cur].next[r] = t
			cur = t
		}
		m.nodes[cur].termLen = int32(len(kw)) // 终止标记：关键词字节长度
		// 收集词库字节集（256 位位图）。
		for i := 0; i < len(kw); i++ {
			m.byteFilter[kw[i]>>3] |= 1 << (kw[i] & 7)
		}
	}
	m.buildAutomaton()
	return m, nil
}
