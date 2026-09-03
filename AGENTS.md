# AGENTS.md — ratchetmatch 项目协作规范

本文件约束在本仓库中工作的所有 AI 助手 / 自动化任务的每一次循环。开始任何任务前必须完整阅读本文件及其引用的通用规范：

- [common.md](agents/common.md) — 语言无关通用规范（规格与进度文档、Git 提交、文档中立性、注释语言、工作循环、shell 环境）；
- [go.md](agents/go.md) — Go 语言通用工程规范（静态检查、测试门槛、现代 Go 写法）。

两者冲突时以本文件为准。

## 1. 规格文档（spec）的位置与权威性

- 项目规格文档位于仓库根目录的 **`spec/`** 目录（不是 `.trae/`，也不建子目录）：
  - `spec/spec.md` — 需求规格：算法原理、匹配语义、API 契约、验收场景
  - `spec/tasks.md` — 任务清单与实现期修正记录（防回归）
  - `spec/checklist.md` — 验收结论摘要
- 沿用 [common.md](agents/common.md) 第 1 节的规格权威性规则；本项目对应文档即上述三份。

## 2. Git 提交规范（强制）

- 沿用 [common.md](agents/common.md) 第 2 节全部约定。
- 本项目额外约定：**不提交**临时文件（`*.out`、`test_out.txt`、`bench_out.txt`、`.env.*` 等已由 `.gitignore` 排除）。

## 3. 路径与环境的文档中立性（强制）

- 沿用 [common.md](agents/common.md) 第 3 节全部约定。

## 4. 代码与工程规范

- **Go 版本**：`go 1.27`（见 `go.mod`），不引入第三方依赖（保持零依赖库，标准库优先）。
- **格式与静态检查 / 测试门槛 / 现代 Go 写法**：沿用 [go.md](agents/go.md) 全部约定。
- **公共 API 契约**：`New` / `FindAll` / `FindAllOverlapping` / `FindNext` 的签名与语义以 `spec/spec.md` 为准，不得随意变更。
- **并发安全**：`Matcher` 构建后必须保持只读（无查询期可变状态），`FindAll` / `FindNext` 必须可无锁并发调用。
- **性能基线**：BM 坏字符跳跃与 FindNext 首命中即停是本库核心卖点，改动扫描逻辑后应跑 `go test -bench . -run '^$'` 确认无明显回退（FindNext ~7.6–10x 与三参照基线——纯 Trie 重启 / 纯 Boyer-Moore / strings.Index——为参考；跳跃隔离对照 NoSkip 已于 2026-09-01 移除，历史混合跳跃 ~1.4–2x 见 spec/tasks.md）。

## 5. 工作循环（每次任务必须遵守）

沿用 [common.md](agents/common.md) 第 5 节工作循环，其中各步在本项目对应：

| 步骤 | 本项目对应 |
| --- | --- |
| 读环境 | 根目录 `.env.wsl`（Remote-WSL 侧）/ `.env.win`（Windows 侧），机制见 `agents/common.md` 第 6 节 |
| 读规格 | `spec/spec.md` |
| 查进度 | `spec/tasks.md` |
| 最小实现 | 同 `agents/common.md` 第 5 节第 4 步 |
| 自验证 | 第 4 节全部命令 + 对照 `spec/checklist.md` |
| 更新文档 | 勾选/追加 `spec/tasks.md` 与 `spec/checklist.md`；行为变化同步更新 `spec/spec.md` |
| 提交 | `agents/common.md` 第 2 节 + 本文件第 2 节额外约定 |

## 6. 仓库结构

```
matcher.go          公共 API（Package doc / New / Match / Matcher / Find 系列）
option.go           查询与构建选项（Option / WithCaseFold / 折叠引擎惰性选择）
engine.go           自动机查询期结构（nodeAPI / exactNode / foldNode）与泛型扫描引擎（machine）
build.go            双自动机构建（精确：trie/失败指针/CSR 展平/BM 过滤器；折叠：轨道合一与展开；关键词还原）
*_test.go           单元测试、示例、基准、fuzz（testdata/fuzz/ 存回归样本）
spec/               规格文档（spec.md / tasks.md / checklist.md）
agents/             通用协作规范目录（供其它项目复用）
  common.md           语言无关通用规范
  go.md               Go 语言通用工程规范
LICENSE             MIT 许可证（宽松许可，不含专利条款）
AGENTS.md           本文件
.golangci.yml       golangci-lint 配置
.env.win/.env.wsl   本地环境 dotfile（按环境取用，不提交，gitignore 排除）
.gitignore          排除临时产物与 .env.*
```

## 7. Shell 环境规范（WSL）

- 开发环境为 **Remote-WSL**（Ubuntu，POSIX 登录 shell）：`go`、`git`、`golangci-lint` 均在 PATH 中，直接以命令名执行。
- 环境探测结论的持久化与记录机制沿用 [common.md](agents/common.md) 第 6 节。
