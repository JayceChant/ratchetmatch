// 本文件为 ratchetmatch 的外部测试包（package ratchetmatch_test），
// 提供可被 go test 校验输出的可运行示例（Example）。
package ratchetmatch_test

import (
	"fmt"
	"strings"

	"github.com/JayceChant/ratchetmatch"
)

// ExampleMatcher_FindAll 演示一次性找出中英混合文本中的全部关键词命中。
//
// Match.Start/End 为 text 中的字节偏移（[Start,End) 半开区间），中文每字占
// 3 字节，ASCII 每字符占 1 字节；匹配是精确的字符串匹配，"Beijing" 并不会
// 命中中文关键词 "北京"。
func ExampleMatcher_FindAll() {
	matcher, err := ratchetmatch.New([]string{"上海", "北京", "广州", "深圳", "人工智能", "机器学习"})
	if err != nil {
		panic(err)
	}
	text := "上海的人工智能产业发展迅速。Beijing is the capital. 广州与深圳同属粤港澳大湾区，机器学习应用广泛。"
	for _, m := range matcher.FindAll(text) {
		fmt.Printf("%d-%d %s\n", m.Start, m.End, m.Keyword)
	}
	// Output:
	// 0-6 上海
	// 9-21 人工智能
	// 66-72 广州
	// 75-81 深圳
	// 108-120 机器学习
}

// ExampleMatcher_FindNext 演示超长文本的按需查找：FindNext 找到第一个命中
// 即停止扫描、不遍历剩余文本；用返回的 Match.End 作为下一次调用的 offset
// 即可迭代命中序列，其结果与 FindAll 完全一致。这里只取前 3 条就 break，
// 其后的大段文本完全不会被扫描。
func ExampleMatcher_FindNext() {
	matcher, err := ratchetmatch.New([]string{"上海", "北京", "人工智能", "机器学习"})
	if err != nil {
		panic(err)
	}
	// 用拼接模拟长文档：2000 个噪声字（6000 字节）与关键词交替出现。
	noise := strings.Repeat("的在了是和有就不人都一", 200)
	text := noise + "上海" + noise + "人工智能" + noise + "机器学习" + noise + "北京" + noise

	// 先定位第一个命中。
	first, ok := matcher.FindNext(text, 0)
	if !ok {
		fmt.Println("无命中")
		return
	}
	// 以 first.End 为新起点继续按需迭代，收集满 3 条即停止。
	hits := []ratchetmatch.Match{first}
	off := first.End
	for len(hits) < 3 {
		m, ok := matcher.FindNext(text, off)
		if !ok {
			break
		}
		hits = append(hits, m)
		off = m.End
	}
	for _, m := range hits {
		fmt.Printf("%d-%d %s\n", m.Start, m.End, m.Keyword)
	}
	// Output:
	// 6600-6606 上海
	// 13206-13218 人工智能
	// 19818-19830 机器学习
}
