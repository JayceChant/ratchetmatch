// 本文件为构建选项：Option 形态（变参、零值安全）与 WithCaseFold /
// WithSynonyms。模式与分组在 New 时一次性定型（见 matcher.go），Find 系列
// 不再接受任何选项；扫描引擎见 engine.go，构建管线见 build.go。
package ratchetmatch

// Option 定制 New 的构建行为。零值不改变默认行为。
type Option func(*queryOpts)

// queryOpts 聚合一次构建调用的全部选项（变参展开结果）。
type queryOpts struct {
	caseFold  bool
	synGroups [][]string
}

// WithCaseFold 使 New 构建大小写折叠匹配自动机（strings.EqualFold 语义：
// 逐 rune 的 SimpleFold 轨道等价），"Hello" 可命中关键词 "hello"。大小写
// 变体关键词在折叠自动机中合一，不漏报；无展开式折叠（ß 不匹配 ss）。
//
// 折叠模式由构建期一次性决定且不可更改：折叠自动机的命中区间 Keyword 为
// 文本原样切片（如关键词 "hello" 命中文本 "Hello" 时 Keyword 为 "Hello"），
// Start/End 仍可直接切文本。需要同一词库的两种模式时，分别调用 New 构建
// 两个实例。与 WithSynonyms 正交：分组词身份按折叠归一形判定（见下）。
func WithCaseFold() Option {
	return func(o *queryOpts) { o.caseFold = true }
}

// WithSynonyms 声明同义词组：groups 每个内层切片为一组同义词，组员自动
// 并入词库（与显式关键词合并去重、同套合法性校验），命中的 Match.Group
// 携带命中词组号，GroupWords(g) 返回组内成员词。分组是纯输出元数据，
// 不改变任何匹配语义——非重叠最左最长、重叠全量、首停照旧裁决，胜者的
// 组随 Match 带出（如词库含 "电脑城" 时，文本 "电脑城" 命中的是
// "电脑城" 的组，而非 "电脑" 的组）。
//
// 分组为分区语义、恒填充：每个词库词恰属一组——声明组按声明顺序编号
// 0..k-1；未声明分组的词获得单元素组（去重词库序：显式关键词在前、组员
// 按组序在后）。同一词出现在两个声明组报错；空组、空串或非法词报错。
// 与 WithCaseFold 组合时，词身份按折叠归一形判定（"PC" 与 "pc" 归一形
// 相同，分属两组即冲突，同组则合一），GroupWords 返回归一形成员词。
func WithSynonyms(groups [][]string) Option {
	return func(o *queryOpts) { o.synGroups = groups }
}
