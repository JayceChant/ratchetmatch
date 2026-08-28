// 本文件为构建期基准：量化 New 的耗时与内存分配，供转移表结构优化对比。
package ratchetsearch

import "testing"

// benchKeywordPool 生成 n 个 2-4 字中文关键词（词形尽量无前缀关系，
// 保证节点表为 trie 原始扇出而非全量合并后的表）。
func benchKeywordPool(n int) []string {
	chars := []rune("的一是了我不人在他有这上中来到大地以时要就出会可也你对生能而子那得着自之过家学可她里后么去向")
	kws := make([]string, 0, n)
	for len(kws) < n {
		i := len(kws)
		nCh := 2 + i%3 // 2-4 字
		kw := make([]rune, nCh)
		// 用互素步长取字，避免词间前缀关系；i 递增保证词互不相同
		for j := 0; j < nCh; j++ {
			kw[j] = chars[(i*17+j*29)%len(chars)]
		}
		kws = append(kws, string(kw))
	}
	return kws
}

// benchNewN 量化 n 个关键词的构建成本（含 New 内部去重与自动机解析）。
func benchNewN(b *testing.B, n int) {
	kws := benchKeywordPool(n)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := New(kws); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNew100(b *testing.B) { benchNewN(b, 100) }
func BenchmarkNew1k(b *testing.B)  { benchNewN(b, 1_000) }
func BenchmarkNew10k(b *testing.B) { benchNewN(b, 10_000) }
