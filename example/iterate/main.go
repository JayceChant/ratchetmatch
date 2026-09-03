// 示例 3：超长文本按需迭代——FindNext 首命中即停，逐条产出、随时可停。
//
// 运行：go run ./iterate
//
// 场景：文本远大于内存预算，或只需前 N 条命中（如风控命中即告警）。
// FindNext(text, offset) 从 offset 返回第一条命中后立即停止扫描，
// 其后的大段文本完全不会被读取；用返回的 Match.End 作为下一次的 offset
// 继续迭代，得到的序列与 FindAll 完全一致。
//
// FindNext 无状态、可并发调用；Matcher 不会保存任何查询进度，
// 迭代进度由调用方持有——这也是它能流式处理「不断追加的文本」的原因。
package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/JayceChant/ratchetmatch"
)

func main() {
	matcher, err := ratchetmatch.New([]string{"上海", "北京", "人工智能", "机器学习"})
	if err != nil {
		log.Fatal(err)
	}

	// 模拟超长文档：大段噪声文本与关键词交替。
	noise := strings.Repeat("的在了是和有就不人都一", 200) // 2000 字噪声
	text := noise + "上海" + noise + "人工智能" + noise + "机器学习" + noise + "北京" + noise

	// 场景 A：只要第一条（如「文本里是否出现敏感词」），首命中即停，
	// 后面 ~8000 字节的文本完全不扫。
	first, ok := matcher.FindNext(text, 0)
	fmt.Printf("第一条命中：ok=%v  %q  [%d,%d)\n", ok, first.Keyword, first.Start, first.End)

	// 场景 B：只要前 N 条，凑满即停（本例 N=3）。
	const maxHits = 3
	fmt.Printf("\n前 %d 条命中：\n", maxHits)
	offset := 0
	for range maxHits {
		m, ok := matcher.FindNext(text, offset)
		if !ok {
			break // 扫描完整个文本也没有更多命中
		}
		fmt.Printf("  %q  [%d,%d)\n", m.Keyword, m.Start, m.End)
		offset = m.End // 关键：以 End 推进，起点早于 End 的出现不再重复报告
	}

	// 边界行为：offset<0 按 0；offset>=len(text) 或无命中返回 (Match{}, false)；
	// offset 落在多字节字符中间时自动向后对齐到 rune 边界。
	_, ok = matcher.FindNext(text, len(text))
	fmt.Printf("\n从文本末尾查找：ok=%v（offset 越界返回 false）\n", ok)
}
