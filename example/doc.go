// 本目录是 ratchetmatch 的完整可运行示例集：每个子目录是一个独立、自包含的
// 示例程序（package main + 单个 main.go），可直接 go run，也可把 main.go
// 整个复制到自己的项目里改词库后拿来即用。
//
// 运行方式（任选其一，需 Go 1.27+，在 example 目录下执行）：
//
//	go run ./basic/       # 最小上手：一次构建，FindAll 一次拿全
//	go run ./semantics/   # 最左最长语义演示：同一文本三种模式对比
//	go run ./iterate/     # 超长流式文本：FindNext 首命中即停
//	go run ./wordcount/   # 词频统计：FindAllOverlapping 全量计数
//	go run ./synonyms/    # 同义词分组：WithSynonyms 声明组，命中自带组号
//
// 独立模块方案：example 目录自带 go.mod（replace 指向仓库根目录），与根
// module 互不干扰——示例无需被主模块编译/发布，也不影响根目录的测试门槛
// （go build ./... 等）。把 main.go 复制出去时，记得在自己的 go.mod 里
// 执行 go get github.com/JayceChant/ratchetmatch。
package documentation
