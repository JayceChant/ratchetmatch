// 示例 2：匹配语义演示——同一文本分别用两种模式扫描，直观理解语义差异。
//
// 运行：go run ./semantics
//
// 三种查询模式各服务不同需求：
//   - FindAll            非重叠最左最长：内容过滤、高亮标注（每个位置至多属于一个命中）
//   - FindAllOverlapping 全量出现（含重叠）：词频统计、倒排索引构建（见 wordcount 示例）
//   - FindNext           按需单条：超长文本流式处理（见 iterate 示例）
//
// FindAll 的语义规则：从左到右，起点最小者优先；同一起点取完整出现的
// 最长关键词——真包含关系一律输出最长。命中互不重叠、不留空档。
package main

import (
	"fmt"
	"log"

	"github.com/JayceChant/ratchetmatch"
)

// show 打印一组命中：关键词、字节区间、还原出的文本切片。
func show(name string, hits []ratchetmatch.Match) {
	fmt.Printf("%s：\n", name)
	if len(hits) == 0 {
		fmt.Println("  （无命中）")
		return
	}
	for _, m := range hits {
		fmt.Printf("  %q  [%d,%d)\n", m.Keyword, m.Start, m.End)
	}
	fmt.Println()
}

func main() {
	// 词库故意设计成三层前缀链：国 ⊂ 中国 ⊂ 中国人，再加一个单字词 人，
	// 用于展示「真包含关系一律取最长」的遮蔽效果。
	matcher, err := ratchetmatch.New([]string{"国", "中国", "中国人", "人"})
	if err != nil {
		log.Fatal(err)
	}

	text := "中国人热爱中国人民"
	fmt.Printf("文本：%q（词库：国 中国 中国人 人）\n\n", text)

	// FindAll：非重叠最左最长。两处 "中国人" 均整体胜出，
	// 单字词 国/人 在同一位置被最长命中遮蔽，不重复输出。
	show("FindAll（非重叠最左最长）", matcher.FindAll(text))

	// FindAllOverlapping：全部出现，含互相重叠者。
	// 输出按 End 升序、同一 End 按关键词长度降序（注意与 FindAll 的序不同）。
	show("FindAllOverlapping（全量出现，含重叠）", matcher.FindAllOverlapping(text))

	// 语义边界：长词「中国人」未完整出现时，其最长完整前缀「中国」胜出
	// （而非更短的单字「国」）——每一步都取当前可得的最长匹配。
	fmt.Println("边界：文本「中国梦」中「中国人」未完整出现，取最长完整前缀「中国」")
	show("  FindAll", matcher.FindAll("中国梦"))
}
