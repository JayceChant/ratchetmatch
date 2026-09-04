// 示例 5：同义词分组——WithSynonyms 声明同义词组，命中天然携带组号。
//
// 运行：go run ./synonyms
//
// 痛点：词库里 电脑 / 计算机 / PC 是同一个概念，扫描结果却分散在三个词上，
// 调用方得自己维护「词 → 组」映射、对每个命中查表归并。WithSynonyms 把
// 分组下沉为输出元数据：命中直接带 Group，按组聚合就是一次切片自增；
// 组员自动入库（无需在关键词列表里重复列出），WordGroup / WordGroups
// 可随时查回成员词。分组不改变任何匹配语义——最左最长照旧裁决。
package main

import (
	"fmt"
	"log"

	"github.com/JayceChant/ratchetmatch"
)

func main() {
	// 1. 构建：声明同义词组。组按声明顺序编号（组 0、组 1）；未声明分组的
	//    词（服务器、机房）各自成单元素组，组号接在声明组之后——因此
	//    Match.Group 恒有效，任何命中都能直接聚合。
	matcher, err := ratchetmatch.New([]string{"服务器", "机房"},
		ratchetmatch.WithSynonyms([][]string{
			{"电脑", "计算机", "PC"},   // 组 0：组员自动入库
			{"手机", "移动电话", "手机端"}, // 组 1
		}))
	if err != nil {
		log.Fatal(err)
	}

	text := "机房巡检：服务器正常，PC 与计算机数量一致，移动电话信号弱，电脑已锁屏。"

	// 2. 命中自带组号：同一概念的多种写法共享同一个 Group。
	fmt.Println("命中（关键词 [区间] 组号）：")
	for _, m := range matcher.FindAll(text) {
		fmt.Printf("  %-8q [%d,%d)  Group=%d\n", m.Keyword, m.Start, m.End, m.Group)
	}

	// 3. 按组聚合：取回全部组（下标即组号），计数就是一次自增。
	groups := matcher.WordGroups()
	counts := make([]int, len(groups))
	for _, m := range matcher.FindAllOverlapping(text) {
		counts[m.Group]++
	}
	fmt.Println("\n按组聚合（成员词 × 出现次数）：")
	for g, members := range groups {
		fmt.Printf("  组%d  %-24s × %d\n", g, fmt.Sprint(members), counts[g])
	}

	// 单组查询：WordGroup(g) 返回成员词（越界返回 nil）。
	fmt.Printf("\nWordGroup(0) = %v\n", matcher.WordGroup(0))

	// 4. 与 WithCaseFold 正交：折叠模式下词身份按归一形判定，"PC" 与 "pc"
	//    归一同形、必然同组——大小写混排文本也归到同一概念。
	folded, err := ratchetmatch.New(nil,
		ratchetmatch.WithCaseFold(),
		ratchetmatch.WithSynonyms([][]string{{"个人电脑", "PC", "pc"}}))
	if err != nil {
		log.Fatal(err)
	}
	for _, m := range folded.FindAll("pc 与个人电脑、PC 是一回事") {
		fmt.Printf("折叠命中：%-8q Group=%d\n", m.Keyword, m.Group)
	}

	// 注意：分组不参与匹配裁决。词库同时含「电脑」与「电脑城」时，文本
	// 「电脑城」命中的是「电脑城」自己的组，而非「电脑」的组。
}
