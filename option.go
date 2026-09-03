// 本文件为查询与构建选项：Option 形态（变参、零值安全）、WithCaseFold 的
// 语义与两种用法、以及折叠引擎的惰性选择（Matcher.foldEngine，被 Find 系列与
// New 的引擎分支调用）。公共 API 签名见 matcher.go，扫描引擎见 engine.go，
// 双自动机构建见 build.go。
package ratchetmatch

// Option 定制 New 与 Find 系列调用的行为。零值不改变默认行为。
type Option func(*queryOpts)

// queryOpts 聚合一次调用的全部选项（变参展开结果）。
type queryOpts struct {
	caseFold bool
}

// WithCaseFold 启用大小写折叠匹配（strings.EqualFold 语义：逐 rune 的
// SimpleFold 轨道等价），"Hello" 可命中关键词 "hello"。大小写变体关键词
// 在折叠自动机中合一，不漏报；无展开式折叠（ß 不匹配 ss）。
//
// 用法一（惰性）：m, _ := New(kws); m.FindAll(text, WithCaseFold())——首次
// fold 查询在同一 Matcher 内构建折叠自动机（一次性，之后只读，并发 fold
// 查询经 sync.Once 串行等待），精确查询不受任何影响。
// 用法二（fold-only）：m, _ := New(kws, WithCaseFold())——仅构建折叠自动机，
// 后续所有查询（含不传选项的）固定按折叠语义执行且无法关闭；精确查询需求
// 另建 Matcher。选项对象建议提升为包级变量复用（如 var fold =
// ratchetmatch.WithCaseFold()），避免每次调用分配闭包。
// 命中区间的 Keyword 为文本原样切片（如关键词 "hello" 命中文本 "Hello" 时
// Keyword 为 "Hello"），Start/End 仍可直接切文本。
func WithCaseFold() Option {
	return func(o *queryOpts) { o.caseFold = true }
}

// wantsFold 判定一次变参调用是否要求折叠语义。
func wantsFold(opts []Option) bool {
	var o queryOpts
	for _, opt := range opts {
		opt(&o)
	}
	return o.caseFold
}

// foldEngine 返回折叠自动机；惰性模式下首次调用构建一次（并发安全，其它
// goroutine 等待构建完成后共同使用只读结果）。构建必须无条件经 once.Do：
// Once 的快速路径自身原子且提供 happens-before，绕过它读 m.fold 会引入
// 数据竞争。词库由精确 trie 无损还原（trieKeywords），不在 Matcher 中驻留。
// 仅在「foldOnly 或本次调用要求 fold」时可达（见 matcher.go 的引擎分支）。
func (m *Matcher) foldEngine() *foldMatcher {
	m.once.Do(func() {
		if m.fold == nil { // fold-only 构建的 Matcher 不会走到这里，防御兜底
			m.fold = buildFold(trieKeywords(m.exact))
		}
	})
	return m.fold
}
