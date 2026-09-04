// 示例 4：词频统计——FindAllOverlapping 全量计数（关键词提取、标签云、索引构建）。
//
// 运行：go run ./wordcount
//
// 为什么不用 FindAll 做统计：FindAll 是非重叠最左最长语义，"中国人" 会把
// 同位置的 "国"、"人" 遮蔽掉，计数会偏少。FindAllOverlapping 返回每个
// （关键词，出现位置）对，一次都不漏——本例中 "机器学习" 一处就会给
// 机器学习与学习 各计一次。
//
// 开销提示：输出敏感 O(n+K)，K 为总出现数；病态词库（大量互为后缀的词）
// 配高频文本时 K 可达 O(n·m)，超大场景先评估规模。
package main

import (
	"fmt"
	"log"
	"slices"
	"strings"

	"github.com/JayceChant/ratchetmatch"
)

func main() {
	// 候选关键词：故意含包含关系（人工智能 ⊃ 智能；机器学习/深度学习 ⊃ 学习），
	// 以便对照两种计数模式的差异。
	matcher, err := ratchetmatch.New([]string{
		"人工智能", "智能", "机器学习", "深度学习", "学习",
		"大模型", "模型", "算法", "数据",
	})
	if err != nil {
		log.Fatal(err)
	}

	text := "人工智能与大模型：机器学习是人工智能的分支，深度学习又是机器学习的分支，" +
		"大模型训练需要算法与数据——算法决定模型上限，数据决定模型下限。"

	// 全量统计：每个（关键词，出现位置）计一次，含互相重叠者。
	counts := make(map[string]int)
	for _, m := range matcher.FindAllOverlapping(text) {
		counts[m.Keyword]++
	}

	// 按出现次数降序输出（同次数按词典序，保证输出稳定）。
	keywords := make([]string, 0, len(counts))
	for kw := range counts {
		keywords = append(keywords, kw)
	}
	slices.SortFunc(keywords, func(a, b string) int {
		if n := counts[b] - counts[a]; n != 0 {
			return n
		}
		return strings.Compare(a, b)
	})

	fmt.Println("词频统计（FindAllOverlapping，含重叠位置）：")
	for _, kw := range keywords {
		fmt.Printf("  %-10s × %d\n", kw, counts[kw])
	}

	// ---- 对照：FindAll 非重叠模式 ----
	// 同一位置的真包含短词（"人工智能" 里的 "智能"、"机器学习" 里的 "学习"）
	// 被最长命中遮蔽，不出现在结果里——适合「高亮/替换」而非「计数」。
	hits := matcher.FindAll(text)
	fmt.Printf("\n对照：FindAll 非重叠最左最长，共 %d 处：\n", len(hits))
	for _, m := range hits {
		fmt.Printf("  %q  [%d,%d)\n", m.Keyword, m.Start, m.End)
	}

	// ---- 进阶：按同义词组合并统计 ----
	// 统计视角下 "人工智能 / AI / 机器智能" 往往是同一个概念——与其拿到
	// 分散的词频再自己查表归并，不如在构建时用 WithSynonyms 声明分组：命中
	// 自带 Match.Group，按组聚合就是一次切片自增（分组不改变匹配语义）。
	syn, err := ratchetmatch.New([]string{"人工智能", "机器学习", "深度学习", "学习"},
		ratchetmatch.WithSynonyms([][]string{
			{"人工智能", "AI", "机器智能"}, // 组 0：中英写法并入同一概念
		}))
	if err != nil {
		log.Fatal(err)
	}
	groups := syn.WordGroups()
	groupCounts := make([]int, len(groups))
	for _, m := range syn.FindAllOverlapping(text) {
		groupCounts[m.Group]++
	}
	fmt.Println("\n按同义词组聚合（WordGroups 下标即组号）：")
	for g, members := range groups {
		fmt.Printf("  组%d  %v × %d\n", g, members, groupCounts[g])
	}
}
