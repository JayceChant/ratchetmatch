// 示例 1：最小上手——一次构建词库，对文本 FindAll 一次拿回全部命中。
//
// 运行：go run ./basic
//
// 复制到自己的项目后通常只需改两处：关键词列表与文本来源。
// 这个模式覆盖最常见的扫描场景：内容过滤、敏感词审计、实体抽取等——
// 词库固定、文本多变，构建一次 Matcher 反复查询（构建有成本，请勿每条
// 文本重建；Matcher 构建后只读，可无锁并发使用）。
package main

import (
	"fmt"
	"log"

	"github.com/JayceChant/ratchetmatch"
)

func main() {
	// 1. 构建自动机（一次）。词库不能为空、不能含空串；
	//    重复关键词自动去重。
	matcher, err := ratchetmatch.New([]string{
		"上海", "北京", "广州", "深圳",
		"人工智能", "机器学习",
	})
	if err != nil {
		log.Fatal(err)
	}

	// 2. 扫描文本（多次）。返回全部命中，按 Start 升序。
	text := "上海的人工智能产业发展迅速。广州与深圳同属粤港澳大湾区，机器学习应用广泛。"
	hits := matcher.FindAll(text)

	// 3. 使用命中。Start/End 是字节偏移（[Start,End) 半开区间），
	//    且 text[Start:End] == Keyword 恒成立，可直接切片。
	//    注意：中文每字 3 字节、ASCII 每字符 1 字节。
	fmt.Println("命中关键词：")
	for _, m := range hits {
		fmt.Printf("  字节区间 [%d,%d)  关键词 %q\n", m.Start, m.End, m.Keyword)
	}

	// 需要字符列号（而非字节偏移）时，用 utf8.RuneCountInString 换算：
	// runeCount := utf8.RuneCountInString(text[:m.Start])

	// 匹配是精确字符串匹配："Beijing" 不会命中 "北京"。
	fmt.Printf("共命中 %d 处\n", len(hits))
}
