# Checklist

验收全项通过（2026-08-31 全量复测；以下为已验证结论摘要，契约细节见 spec.md）：

- 构建：go.mod（module github.com/JayceChant/ratchetmatch，go 1.27）；空词库/空串/非法 UTF-8/U+FFFD 关键词报错可区分；重复去重
- 语义：非重叠最左最长（含前缀取最长、断词 fail 结算、逐级弹出、无空档——必死候选不弹链 {0,000} 用例）；FindNext 以 End 迭代 == FindAll（500 组随机 + fuzz）
- 正确性：白盒自动机语义重推导（fail/outLens/byteFilter 不变量）、CSR 布局与二分分支、黑盒极端场景（自重叠/Emoji/词即整文本等）；naiveSearch 为独立 oracle，500 组随机对照一致
- 跳跃：root 态坏字符跳跃与禁用跳跃的参照结果完全一致；ASCII 段字节级跳过
- FindAllOverlapping：全量保留、End 升序同 End 长度降序，与逐词 strings.Index 枚举一致
- 并发：Matcher 构建后只读，8 goroutine × 100 次 FindAll/FindNext，`go test -race` 通过
- fuzz：FuzzMatch 任意字节文本 × 关键词组合不 panic、全部不变量与 oracle 成立（3 分钟 449 万 execs 零失败）；崩溃样本回归 testdata/fuzz/
- 工程：gofmt / go vet / golangci-lint（0 issues）/ go test ./...（-race 经 Windows 侧执行）全链路通过；基准无回退（混合跳跃 ~1.97x、FindNext ~10x、New 持平或更优）；朴素多模式对照 ~5.2x、FindNext 对照 ~7.6x，参照与正式 API 等价由 TestNaiveMultiEquiv 锁定
