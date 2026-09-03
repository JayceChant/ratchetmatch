// 本文件为 fold 与 exact 模式的对照基准：同一词库、同一文本分别用
// 默认（精确）与 WithCaseFold 构建，量化折叠模式的构建与查询开销。
//
// 对照口径：基准词库为纯中文（无大小写轨道），折叠自动机的命中结果与
// 精确完全一致（TestFoldBenchEquiv 守卫），因此查询耗时差即折叠引擎的
// 纯结构开销——轨道展开使 rootNext/CSR 键冗余、命中提取走 runeStartBack
// 的 rune 级回退而非精确路径的字节长度差；构建耗时差为逐 rune 轨道解析
// （SimpleFold 环上走圈）与展开键写入的额外成本。大小写混排文本的折叠
// 语义收益（漏报→命中）不在此量化，见 casefold_test.go 的语义测试。
package ratchetmatch

import (
	"strings"
	"testing"
)

var (
	// 两套基准词库的折叠自动机（与 bench_test.go 的精确自动机同词库）
	benchFoldMatcherSparse  Matcher
	benchFoldMatcherOverlap Matcher
	// ASCII 大小写词库对照（轨道展开的最坏情况）
	benchMatcherASCII     Matcher
	benchFoldMatcherASCII Matcher
	benchTextEnglish      string
)

func init() {
	var err error
	if benchFoldMatcherSparse, err = New(benchKeywordsSparse, WithCaseFold()); err != nil {
		panic(err)
	}
	if benchFoldMatcherOverlap, err = New(benchKeywordsOverlap, WithCaseFold()); err != nil {
		panic(err)
	}
	// ASCII 词库：每个字母 rune 轨道展开为大小写两键，rootNext/CSR 键数
	// 约为精确版两倍，代表折叠构建/查询开销的最坏情况。
	for _, kws := range [][]string{benchKeywordsASCII} {
		if len(kws) != 100 {
			panic("ASCII 基准词库须为 100 个关键词")
		}
	}
	// 防呆：噪声句不得含任何词库词（否则 fold/exact 命中数不等，对照失真）
	for _, kw := range benchKeywordsASCII {
		if strings.Contains(benchEnglishNoise, kw) {
			panic("英文噪声句含有词库关键词：" + kw)
		}
	}
	if benchMatcherASCII, err = New(benchKeywordsASCII); err != nil {
		panic(err)
	}
	if benchFoldMatcherASCII, err = New(benchKeywordsASCII, WithCaseFold()); err != nil {
		panic(err)
	}
	benchTextEnglish = buildEnglishText(100_000, benchKeywordsASCII)
}

// benchKeywordsASCII 为 ASCII 大小写词库：100 个英文关键词（全部小写形态），
// 每个字母 rune 轨道展开为大小写两键（rootNext/CSR 键数约为精确版两倍），
// 代表折叠构建/查询开销的最坏情况。
var benchKeywordsASCII = []string{
	"time", "year", "people", "way", "day", "man", "thing", "woman", "life", "child",
	"world", "school", "state", "family", "student", "group", "country", "problem", "hand", "part",
	"place", "case", "week", "company", "system", "program", "question", "work", "government", "number",
	"night", "point", "home", "water", "room", "mother", "area", "money", "story", "fact",
	"month", "lot", "right", "study", "book", "eye", "job", "word", "business", "issue",
	"side", "kind", "head", "house", "service", "friend", "father", "power", "hour", "game",
	"line", "end", "member", "law", "car", "city", "name", "team", "minute", "idea",
	"body", "back", "parent", "face", "others", "level", "office", "door", "health", "person",
	"art", "war", "history", "party", "result", "change", "morning", "reason", "research", "girl",
	"guy", "moment", "air", "teacher", "force", "education", "foot", "boy", "age", "policy",
}

// benchEnglishNoise 为不含词库词的英文噪声句（人工核验：全为高频虚词短语，
// 与词库实义词零交集，防呆断言兜底）。
const benchEnglishNoise = "the of and to in a is that it for was on are as with his they at be this "

// buildEnglishText 构造约 n rune 的英文文本：噪声句循环为主体，每隔一段
// 插入一个词库关键词（首字母大写形态，同经折叠轨道），保证命中存在。
func buildEnglishText(n int, keywords []string) string {
	var b strings.Builder
	b.Grow(n)
	runes := 0
	i := 0
	for runes < n {
		for range 8 {
			if runes >= n {
				break
			}
			b.WriteString(benchEnglishNoise)
			runes += len(benchEnglishNoise)
		}
		if runes >= n {
			break
		}
		kw := keywords[i%len(keywords)]
		b.WriteString(strings.ToUpper(kw[:1]) + kw[1:]) // 首字母大写：仅 fold 可命中，量化折叠收益
		runes += len(kw)
		i++
	}
	return b.String()
}

// TestFoldBenchEquiv 守卫 fold/exact 基准的对照口径：基准词库为纯中文、
// 无大小写轨道，折叠 FindAll 必须与精确结果完全一致，否则两组基准的
// 差值不再是「同负载的纯开销」对比。
func TestFoldBenchEquiv(t *testing.T) {
	for _, d := range []struct {
		name  string
		fold  Matcher
		exact Matcher
		texts [2]string
	}{
		{"稀疏词库", benchFoldMatcherSparse, benchMatcherSparse,
			[2]string{benchTextZhSparse, benchTextMixSparse}},
		{"重叠词库", benchFoldMatcherOverlap, benchMatcherOverlap,
			[2]string{benchTextZhOverlap, benchTextMixOverlap}},
	} {
		for _, tx := range []struct{ name, body string }{
			{"Zh", d.texts[0]},
			{"Mix", d.texts[1]},
		} {
			want := d.exact.FindAll(tx.body)
			got := d.fold.FindAll(tx.body)
			if len(got) != len(want) {
				t.Fatalf("%s/%s: fold %d 条 vs exact %d 条", d.name, tx.name, len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("%s/%s: 第 %d 条不一致 fold=%+v exact=%+v",
						d.name, tx.name, i, got[i], want[i])
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 基准：fold 构建（对照 bench_new_test.go 的精确构建 BenchmarkNew{100,1k,10k}）
// ---------------------------------------------------------------------------

// benchNewFoldN 量化 n 个关键词的折叠构建成本（对照 benchNewN 同词库精确构建）。
func benchNewFoldN(b *testing.B, n int) {
	kws := benchKeywordPool(n)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := New(kws, WithCaseFold()); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNewFold100(b *testing.B) { benchNewFoldN(b, 100) }
func BenchmarkNewFold1k(b *testing.B)  { benchNewFoldN(b, 1_000) }
func BenchmarkNewFold10k(b *testing.B) { benchNewFoldN(b, 10_000) }

// ---------------------------------------------------------------------------
// 基准：fold 查询（对照 bench_test.go 同名去 Fold 后缀的精确查询）
// ---------------------------------------------------------------------------

// BenchmarkFindAllChineseFold 纯中文长文本 FindAll（稀疏词库，fold）。
func BenchmarkFindAllChineseFold(b *testing.B) {
	for b.Loop() {
		benchSink = benchFoldMatcherSparse.FindAll(benchTextZhSparse)
	}
}

// BenchmarkFindAllMixedFold 中英混合长文本 FindAll（稀疏词库，fold）。
func BenchmarkFindAllMixedFold(b *testing.B) {
	for b.Loop() {
		benchSink = benchFoldMatcherSparse.FindAll(benchTextMixSparse)
	}
}

// BenchmarkFindNextFirstFold 中英混合文本只找第一个命中（稀疏词库，fold）。
func BenchmarkFindNextFirstFold(b *testing.B) {
	for b.Loop() {
		m, ok := benchFoldMatcherSparse.FindNext(benchTextMixSparse, 0)
		if !ok {
			b.Fatal("应有命中")
		}
		benchSink = []Match{m}
	}
}

// BenchmarkFindAllChineseFoldOverlap 纯中文长文本 FindAll（重叠词库，fold）。
func BenchmarkFindAllChineseFoldOverlap(b *testing.B) {
	for b.Loop() {
		benchSink = benchFoldMatcherOverlap.FindAll(benchTextZhOverlap)
	}
}

// BenchmarkFindAllMixedFoldOverlap 中英混合长文本 FindAll（重叠词库，fold）。
func BenchmarkFindAllMixedFoldOverlap(b *testing.B) {
	for b.Loop() {
		benchSink = benchFoldMatcherOverlap.FindAll(benchTextMixOverlap)
	}
}

// BenchmarkFindNextFirstFoldOverlap 中英混合文本首个命中（重叠词库，fold）。
func BenchmarkFindNextFirstFoldOverlap(b *testing.B) {
	for b.Loop() {
		m, ok := benchFoldMatcherOverlap.FindNext(benchTextMixOverlap, 0)
		if !ok {
			b.Fatal("应有命中")
		}
		benchSink = []Match{m}
	}
}

// ---------------------------------------------------------------------------
// 基准：ASCII 词库 fold vs exact（轨道展开最坏情况 + 折叠语义收益）
// ---------------------------------------------------------------------------

// BenchmarkFindAllEnglishFold 英文长文本 FindAll（fold）：噪声大写化时仅
// fold 可命中，量化折叠语义收益。
func BenchmarkFindAllEnglishFold(b *testing.B) {
	for b.Loop() {
		benchSink = benchFoldMatcherASCII.FindAll(benchTextEnglish)
	}
}

// BenchmarkFindAllEnglishExact 同文本精确扫描（fold 的语义收益 = 两者之差）。
func BenchmarkFindAllEnglishExact(b *testing.B) {
	for b.Loop() {
		benchSink = benchMatcherASCII.FindAll(benchTextEnglish)
	}
}
