# AGENTS.md — ratchetsearch 项目协作规范

本文件约束在本仓库中工作的所有 AI 助手 / 自动化任务的每一次循环。开始任何任务前必须完整阅读本文件。

## 1. 规格文档（spec）的位置与权威性

- 项目规格文档位于仓库根目录的 **`spec/`** 目录（不是 `.trae/`，也不建子目录）：
  - `spec/spec.md` — 需求规格：算法原理、匹配语义、API 契约、验收场景
  - `spec/tasks.md` — 任务清单：实现进度（含勾选状态与实现期修正记录）
  - `spec/checklist.md` — 验收清单：逐项验证点
- **所有后续任务必须先读 `spec/spec.md` 并遵循其规定**。实现与 spec 冲突时：
  1. 若 spec 本身有误或需求变化，先修改 spec（并在 tasks.md 记录修正缘由），再改代码；
  2. 严禁绕过 spec 直接实现（例如为让测试通过而"避开"某些用例组合——此类做法已被证明会掩盖真实缺陷）。
- 每完成一项任务，勾选 `spec/tasks.md` 对应条目；每验证通过一项验收点，勾选 `spec/checklist.md` 对应条目。

## 2. Git 提交规范（强制）

- **每次被接受的修改（用户确认或任务验收通过）都必须做一次 git commit**，不积攒多个任务的改动。
- 提交信息使用**中文 Conventional Commits** 格式：

  ```
  <类型>: <中文摘要，50 字符内，不加句号>

  <正文：动机与要点，中文，每行 ≤ 72 字符，可省略>
  ```

- 类型取值：`feat`（新功能）、`fix`（缺陷修复）、`refactor`（重构）、`test`（测试）、`docs`（文档）、`perf`（性能）、`chore`（构建/工具）。
- 示例：
  - `feat: 实现中文优化 ACBM 多模式匹配库`
  - `fix: 修复 maxOut 单值遮蔽导致的漏命中`
  - `docs: 补充 AGENTS.md 协作规范`
- 提交前必须确认 `git status` 干净合理、`git diff` 只含本次任务的改动；**不提交**密钥、临时文件（`*.out`、`test_out.txt`、`bench_out.txt`、`.env.*` 等已由 `.gitignore` 排除）。
- 禁止 `--force` 推送、禁止改写历史；仅在被明确要求时推送远端。
- 多行提交信息：POSIX shell（WSL）下直接用 heredoc：`git commit -m "$(cat <<'EOF' ... EOF)"`。
- **提交信息环境中立**：commit message 中不得出现本地绝对路径，仅可使用相对项目根目录的路径。

## 2.1 路径与环境的文档中立性（强制）

- 凡**进入 git 提交的任何文档**（AGENTS.md、`spec/` 下三份文档、README、代码注释、commit message 等）：
  - **禁止**出现本地绝对路径（盘符路径、`/home/...`、`/mnt/...`、`C:\Users\...` 等）与用户名、机器名等环境特定信息；
  - **只允许**使用相对项目根目录的路径（如 `spec/spec.md`、`build.go`）；
  - 仓库根目录本身在文档中一律表述为「项目根目录」或 `.`，不写实际盘符。
- 本地绝对路径**唯一合法的存放处**是 `.env.*`（不提交）。

## 3. 代码与工程规范

- **Go 版本**：`go 1.27`（见 `go.mod`），不引入第三方依赖（保持零依赖库，标准库优先）。
- **格式与静态检查**：任何改动提交前必须通过：
  ```bash
  go build ./... && go vet ./... && gofmt -l .   # gofmt -l 必须无输出
  golangci-lint run                              # 0 issues（配置见 .golangci.yml）
  ```
  golangci-lint 配置原则：官方 standard 默认集 + 少量零误报增补，formatter 仅 gofmt；出现告警优先修代码而非改配置。
- **测试门槛**：涉及源码改动的提交必须通过：
  ```bash
  go test ./... -count=1
  go test -race ./... -count=1
  ```
  修复缺陷时必须先添加能复现该缺陷的测试（红→绿），并同步更新 spec 相关条目。
- **注释与文档语言**：代码注释、提交信息、spec 文档一律使用**中文**；标识符用英文。
- **现代 Go 写法**（2026-08-31 `go fix` 审计 + b.Loop 补充结论，新代码不得再引入旧写法）：
  - 已知次数循环用 `for i := range n`，不再写 `for i := 0; i < n; i++`（Go 1.22+ range-over-int）；
  - 仅遍历切片/映射无需下标时用 `for _, v := range xs`，需要下标时 `for i, v := range xs`，不再用 C 式三段循环；
  - 基准循环用 `for b.Loop() { ... }`，不再写 `for i := 0; i < b.N; i++`（Go 1.24+）：自动扣除每次迭代的管理开销、保持编译器优化（防止过度内联失真）、无需手动 `b.ResetTimer`（准备代码天然排除）；
  - `min` / `max` / `clear` 内建（Go 1.21+）优先于手写；`slices` / `maps` 标准库包优先于手写循环（排序、查找、去重等）；
  - `strings.Builder` 链式写入时拆分拼接表达式：不写 `b.WriteString(prefix + string(s) + ",")`，改为 `b.WriteString(prefix); b.WriteRune(s); b.WriteByte(',')`（逐次写入零中间分配，`string(rune)` 转换会被拼接整体物化；go fix stringsbuilder 也会做此改写）；
  - 上述已由 golangci-lint（gofmt/standard 集）部分覆盖，但 lint 不报 range-over-int 与 b.N→b.Loop 升级，靠本条约定自律；提交前可跑 `go fix ./...` 自查（应无输出，注意 go fix 不含 b.Loop 升级）。
- **公共 API 契约**：`New` / `FindAll` / `FindAllOverlapping` / `FindNext` 的签名与语义以 `spec/spec.md` 为准，不得随意变更；变更属破坏性修改（**BREAKING**），需在 spec 中显式声明并说明迁移方式。
- **并发安全**：`Matcher` 构建后必须保持只读（无查询期可变状态），`FindAll` / `FindNext` 必须可无锁并发调用；新增字段/逻辑时不得违反此约束。
- **性能基线**：BM 坏字符跳跃与 FindNext 首命中即停是本库核心卖点，改动扫描逻辑后应跑 `go test -bench . -run '^$'` 确认无明显回退（混合文本跳跃收益 ~1.4x、FindNext ~10x 为参考基线）。

## 4. 工作循环（每次任务必须遵守）

1. **读环境**：读根目录当前环境对应的 `.env.*` 文件（`.env.wsl` / `.env.win`，存在则直接采用探测结论；缺失才重新探测并回写）。
2. **读 spec**：从 `spec/spec.md` 起手，明确需求边界与验收场景。
3. **查进度**：读 `spec/tasks.md`，确认已完成/未完成任务，避免重复劳动。
4. **最小实现**：只做被要求或显然必要的改动；不过度设计、不提前抽象。
5. **自验证**：跑第 3 节全部命令；对照 `spec/checklist.md` 逐项核对。
6. **更新文档**：勾选 tasks/checklist；行为变化同步更新 spec。
7. **提交**：按第 2 节规范 git commit。

## 5. 仓库结构

```
ratchetsearch.go   公共 API（New / Match / Matcher）
build.go           Trie 构建、失败指针、全量转移自动机、BM 跳跃表
search.go          FindAll / FindNext / scan / skipForward
*_test.go          单元测试、示例、基准
spec/              规格文档（spec.md / tasks.md / checklist.md）
AGENTS.md          本文件
.golangci.yml      golangci-lint 配置（standard 集 + 少量增补）
.env.win/.env.wsl  本地环境 dotfile（按环境取用，不提交，gitignore 排除）
.gitignore         排除临时产物与 .env.*
```

## 6. Shell 环境规范（WSL）

- 开发环境为 **Remote-WSL**（Ubuntu，POSIX 登录 shell）：`go`、`git`、`golangci-lint` 均在 PATH 中，直接以命令名执行，无需路径前缀。
- 环境探测结论持久化于根目录 `.env.*` 文件，**按环境独立成文件、互不覆盖**（`.env.win` — Windows 主机侧探测结论；`.env.wsl` — Remote-WSL 侧探测结论）：
  - 每次任务开始先读取当前环境对应文件并直接采用（见第 4 节工作循环第 1 步）；
  - 记录内容：OS / 发行版、工具链路径与版本、项目路径、验证结论与历史备注；
  - 环境变化时只更新对应环境的文件，**不直接改动其他环境的文件**；新环境（如 macOS、其他发行版）按同一命名规则新增 `.env.<环境名>`。
- shell 命令与提交信息一律按 POSIX 语义执行，heredoc 可直接使用（见第 2 节）。
