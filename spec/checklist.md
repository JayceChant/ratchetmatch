# Checklist

验收全项通过（2026-08-31 全量复测；以下为已验证结论摘要，契约细节见 spec.md）：

- 构建：go.mod（module github.com/JayceChant/ratchetmatch，go 1.27）；空词库/空串/非法 UTF-8/U+FFFD 关键词报错可区分；重复去重
- 语义：非重叠最左最长（含前缀取最长、断词 fail 结算、逐级弹出、无空档——必死候选不弹链 {0,000} 用例）；FindNext 以 End 迭代 == FindAll（500 组随机 + fuzz）
- 正确性：白盒自动机语义重推导（fail/outLens/byteFilter 不变量）、CSR 布局与二分分支、黑盒极端场景（自重叠/Emoji/词即整文本等）；naiveSearch 为独立 oracle，500 组随机对照一致
- 跳跃：root 态坏字符跳跃不漏报（独立 oracle 随机对照与 fuzz 覆盖）；ASCII 段字节级跳过
- FindAllOverlapping：全量保留、End 升序同 End 长度降序，与逐词 strings.Index 枚举一致
- 并发：Matcher 构建后只读，8 goroutine × 100 次 FindAll/FindNext，`go test -race` 通过
- fuzz：FuzzMatch 任意字节文本 × 关键词组合不 panic、全部不变量与 oracle 成立（3 分钟 449 万 execs 零失败）；崩溃样本回归 testdata/fuzz/
- 工程：gofmt / go vet / golangci-lint（0 issues）/ go test ./...（-race 经 Windows 侧执行）全链路通过；基准无回退（New 持平或更优；跳跃隔离对照于 2026-09-01 并入三基线体系，历史混合 ~1.97x 见 tasks.md）；三参照基线（各 100 词 × 稀疏/重叠 × 10 万 rune）：中文 Trie ~1.1–1.2x / BM ~9.6–11.0x / Index ~9.9–11.1x，混合 Trie ~1.3–1.9x / BM ~11.5–13.3x / Index ~9.4–9.7x，FindNext 首停 ~12.5–19.7x（相对逐词全文扫），等价性由 TestBaselineEquiv（两套词库 × 两份文本）锁定
- CI（2026-09-01）：GitHub Actions 四工作流（ci/scorecard/codeql/sonarcloud）+ Codecov 免 token 上传 + pkg.go.dev 自动抓取；README 双语徽章保留（「质量与 CI」结果表与文末「第三方服务说明」已于 2026-09-02 移除，README 收敛为用户视角，开通遗留项见 tasks.md 当日记录）；仅 SonarCloud 需 SONAR_TOKEN secret、Scorecard 提分需 master 分支保护。goreportcard 已于同日移除（官方宣布停止服务，其检查项由 CI 的 golangci-lint/go vet/gofmt 覆盖）
