# 通用协作规范（Go 语言）

供各项目 AGENTS.md 引用的 **Go 语言**通用工程规范。项目侧文件通过相对链接引用本文件后，以下条目生效；项目绑定约定（如 Go 版本、依赖策略、性能基线）以项目侧文件为准，两者冲突时**项目侧优先**。

## 1. 格式与静态检查

任何改动提交前必须通过：

```bash
go build ./... && go vet ./... && gofmt -l .   # gofmt -l 必须无输出
go fix ./...                                   # 有修复输出则应用后重复执行，直至无输出（注意它不含 b.Loop 升级）
golangci-lint run                              # 0 issues（配置见 .golangci.yml）
```

出现告警优先修代码而非改配置。凡涉及**源码改动**的提交，`go fix ./...` 与 `golangci-lint run` 必须在提交前先行执行并通过；`go fix` 一轮修复可能触发下一轮改写，须反复执行直至无输出为止。

## 2. 测试门槛

- 涉及源码改动的提交必须通过 `go test ./... -count=1` 与 `go test -race ./... -count=1`。
- 修复缺陷时必须先添加能复现该缺陷的测试（红→绿），并同步更新规格相关条目。
- 改动性能敏感路径后应跑基准（`go test -bench . -run '^$'`）确认无明显回退。

## 3. 现代 Go 写法

新代码不得再引入旧写法（lint 不全覆盖、靠本条自律）：

- 已知次数循环用 `for i := range n`；遍历切片/映射用 `for i, v := range xs` / `for _, v := range xs`，不用 C 式三段循环；
- 基准循环用 `for b.Loop() { ... }`（自动扣除迭代管理开销、保持编译器优化），不用 `b.N`；
- `min` / `max` / `clear` 内建与 `slices` / `maps` 标准库包优先于手写；
- `strings.Builder` 链式写入逐次写入，不写 `b.WriteString(prefix + string(s) + ",")` 式整体拼接（中间分配）；
- `go fix` 现代化改写的典型写法（提交前须收敛到目标形式）：
  - `interface{}` 写作 `any`；
  - 手写循环判成员用 `slices.Contains` / `slices.Index`；`sort.Slice` / `sort.Sort` 用 `slices.Sort` / `slices.SortFunc` 替代；
  - `[]byte(fmt.Sprintf(...))` 用 `fmt.Appendf`；收集 map 键/值切片优先 `slices.Collect(maps.Keys(m))` 等迭代器组合；
  - `strings.HasPrefix` + `strings.TrimPrefix` 组合用 `strings.CutPrefix`；逐段遍历分隔串优先 `strings.SplitSeq`（零分配迭代）；
  - `sync.WaitGroup` 的 Add/go/Done 三段式用 `wg.Go(...)`；测试内取 context 用 `t.Context()` 替代 `context.Background()`。
