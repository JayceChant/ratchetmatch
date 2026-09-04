// Package ratchetmatch 实现针对中文优化的 ACBM（Aho-Corasick + Boyer-Moore）
// 多模式匹配。名字中的 ratchet（棘轮）指文本指针单向只进不退的单遍扫描。
//
// 构建期建立 rune 级 Trie 与失败指针，节点转移表仅存自有 trie 边并展平进
// 全局 CSR 有序数组：查询期每处理一个 rune 先在当前状态段内查找，未命中
// 沿失败指针回退重试（平均 1–2 步，摊还 O(1)），不做 DFA 全量解析——后者
// 在中文大字符集下会使每节点表膨胀至 root 扇出，内存不可接受。自动机处于
// root 态时应用 Boyer-Moore 坏字符跳跃，按词库首字符集跳过不可能出现匹配
// 起始的文本段。
//
// 匹配语义为非重叠最左最长：起点最小者优先，同一起点取完整出现的最长关键
// 词（真包含关系一律输出最长）。
//
// 匹配模式由构建期一次性决定、不可更改：New(keywords) 返回大小写敏感的
// 精确匹配实现；New(keywords, WithCaseFold()) 返回 SimpleFold 轨道折叠的
// 匹配实现。两种模式是两套独立实现（exactMatcher / foldMatcher），需要
// 同时使用时分别 New 两个实例即可（与主流多模式引擎的 caseless 构建模式
// 一致，见 option.go 的 WithCaseFold）。文件布局：matcher.go 公共 API
// （本文件）/ option.go 选项 / engine.go 扫描引擎 / build.go 构建。
package ratchetmatch

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Match 表示一次关键词命中。Start/End 为 text 中的字节偏移，
// [Start,End) 半开区间，且 text[Start:End] == Keyword（折叠模式下 Keyword
// 为文本原样切片，即实际命中的大小写形态）。
//
// Group 是命中词的同义词组号（分区语义，恒有效）：WithSynonyms 声明组按
// 声明顺序编号，未声明的词各自成单元素组（去重词库序）。分组不改变匹配
// 语义，调用方可直接按组聚合。折叠模式下 Group 亦是命中词的规范词身份
// （Keyword 随文本大小写形态变化，Group 跨形态稳定），组员经
// Matcher.WordGroup / WordGroups 查询。
type Match struct {
	Start   int
	End     int
	Keyword string
	Group   int
}

// Matcher 是构建完成的多模式匹配自动机。模式由 New 的选项一次性决定：
// 默认为精确匹配；WithCaseFold 为大小写折叠匹配（strings.EqualFold 语义）。
// 实现构建后只读，可无锁并发使用；经未导出方法 isInternal 密封，仅本包
// 提供实现（精确 exactMatcher / 折叠 foldMatcher，见 engine.go），接口可
// 自由演进。
type Matcher interface {
	// FindAll 返回 text 中所有命中（非重叠最左最长：起点最小优先；同一起点
	// 前缀关系取最长），按出现先后（Start 升序）排序；无命中或 text 为空
	// 返回 nil。
	FindAll(text string) []Match
	// FindAllOverlapping 返回 text 中全部关键词出现（含互相重叠者），服务
	// 词频统计、关键词提取、索引构建等场景：每个（关键词，出现位置）输出
	// 一次，不做非重叠筛选。输出按 End 升序、同一 End 按关键词长度降序。
	// 无命中或 text 为空返回 nil。
	//
	// 开销输出敏感：时间 O(n + K)、空间 O(K)，K 为总出现数——病态词库
	// 配高频文本时 K 可达 O(n·m)，调用方自行评估规模。不提供对应的
	// FindNext 版本：重叠语义与「从 offset 重扫的无状态迭代」不合（见
	// spec Non-Goals）。
	FindAllOverlapping(text string) []Match
	// FindNext 从 offset（字节偏移）开始查找第一个命中，找到即终止扫描、
	// 不遍历剩余文本，适合超长文本按需查找。无状态，可并发调用；调用方用
	// 返回的 End 作下次 offset 迭代，得到的序列与 FindAll 完全一致。无命中
	// 返回 (Match{}, false)。offset<0 按 0 处理；offset>=len(text) 返回
	// false；offset 落在多字节字符中间时向后对齐到 rune 边界。
	FindNext(text string, offset int) (Match, bool)
	// CaseFold 报告该 Matcher 是否为大小写折叠模式（即 New 时是否传入了
	// WithCaseFold）。
	CaseFold() bool
	// WordGroup 返回同义词组 g 的全部成员词（去重后的词库序）：WithSynonyms
	// 声明组返回声明成员（折叠模式为折叠归一形），未声明分组的词自成单元素
	// 组。返回内部只读切片，调用方不得修改；越界组号返回 nil。组号恒有效：
	// 任何命中的 Match.Group 均可直接传入。
	WordGroup(g int) []string
	// WordGroups 返回全部同义词组，按组号升序（下标即 Match.Group 组号）。
	// 外层切片为新建、可自由重排；元素与 WordGroup 返回的内部只读切片相同，
	// 不得修改元素内容。
	WordGroups() [][]string

	// isInternal 密封接口：包外类型无法实现 Matcher，新增方法不构成破坏性
	// 变更。
	isInternal()
}

// New 根据关键词列表（与 WithSynonyms 的组员合并）构建 Matcher；模式与分组
// 由 opts 一次性决定且不可更改：
//   - New(keywords)：精确匹配（大小写敏感）；
//   - New(keywords, WithCaseFold())：大小写折叠匹配；
//   - New(keywords, WithSynonyms(groups))：附加同义词分组（与折叠正交组合），
//     见 option.go 与 Match.Group。
//
// 若需同一词库的两种模式，分别调用 New 构建两个实例，按需使用。
//
// 词库（关键词与组员合并去重后）不能为空；关键词/组员不能为空字符串；
// 重复词自动去重。词必须是合法 UTF-8 且不得包含 U+FFFD（替换字符）：
// 非法字节在 rune 层坍缩为同一个 RuneError（身份歧义），U+FFFD 的 3 字节
// 编码与查询端逐字节前进不一致（长度歧义），二者均会使命中区间或关键词
// 身份失去健全语义，故显式拒绝（详见 spec 词库校验需求）。同一词出现在
// 两个声明同义词组亦报错（折叠模式按折叠归一形判冲突）。文本侧的非法
// 字节不受影响，扫描时按 RuneError 逐字节处理，不 panic、不漏扫。
func New(keywords []string, opts ...Option) (Matcher, error) {
	var o queryOpts
	for _, opt := range opts {
		opt(&o)
	}
	// 折叠模式按归一形解析词身份与分组：组员/关键词的折叠变体归一同形
	//（"PC"/"pc" 同词），词库以归一形构建（与折叠 trie 边键同构，自动机
	// 等价）；其余模式 norm 为 nil，词身份即原词。
	norm := func(w string) string { return w }
	if o.caseFold {
		norm = foldNorm
	}
	// 分组解析（组员校验、冲突检测、词→组号表）先行：报错时不得构建。
	kws, part, err := resolveSynonyms(keywords, o.synGroups, norm)
	if err != nil {
		return nil, err
	}
	if len(kws) == 0 {
		return nil, errors.New("ratchetmatch: keyword list is empty")
	}
	for i, kw := range keywords {
		if kw == "" {
			return nil, fmt.Errorf("ratchetmatch: keyword at index %d is empty", i)
		}
		if !utf8.ValidString(kw) {
			return nil, fmt.Errorf("ratchetmatch: keyword at index %d is not valid UTF-8", i)
		}
		if strings.Contains(kw, "\uFFFD") {
			return nil, fmt.Errorf("ratchetmatch: keyword at index %d contains U+FFFD (replacement character)", i)
		}
	}
	if o.caseFold {
		return buildFold(kws, part), nil
	}
	return buildExact(kws, part), nil
}
