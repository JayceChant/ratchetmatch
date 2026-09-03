// 本文件为构建选项：Option 形态（变参、零值安全）与唯一选项 WithCaseFold。
// 模式在 New 时一次性定型（见 matcher.go），Find 系列不再接受模式选项；
// 扫描引擎见 engine.go，构建管线见 build.go。
package ratchetmatch

// Option 定制 New 的构建行为。零值不改变默认行为。
type Option func(*queryOpts)

// queryOpts 聚合一次构建调用的全部选项（变参展开结果）。
type queryOpts struct {
	caseFold bool
}

// WithCaseFold 使 New 构建大小写折叠匹配自动机（strings.EqualFold 语义：
// 逐 rune 的 SimpleFold 轨道等价），"Hello" 可命中关键词 "hello"。大小写
// 变体关键词在折叠自动机中合一，不漏报；无展开式折叠（ß 不匹配 ss）。
//
// 折叠模式由构建期一次性决定且不可更改：折叠自动机的命中区间 Keyword 为
// 文本原样切片（如关键词 "hello" 命中文本 "Hello" 时 Keyword 为 "Hello"），
// Start/End 仍可直接切文本。需要同一词库的两种模式时，分别调用 New 构建
// 两个实例。
func WithCaseFold() Option {
	return func(o *queryOpts) { o.caseFold = true }
}

// wantsFold 判定一次构建调用是否要求折叠模式。
func wantsFold(opts []Option) bool {
	var o queryOpts
	for _, opt := range opts {
		opt(&o)
	}
	return o.caseFold
}
