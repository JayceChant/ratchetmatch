# Tasks

- [x] Task 1: 初始化 Go module 与包骨架
  - [x] 1.1 创建 `go.mod`（module ratchetsearch，go 1.27）
  - [x] 1.2 创建 `ratchetsearch.go`：定义 `Match` 结构体、`New(keywords []string) (*Matcher, error)`、`(*Matcher) FindAll(text string) []Match`、`(*Matcher) FindNext(text string, offset int) (Match, bool)` 的 API 声明与包注释

- [x] Task 2: 实现 Trie 构建与校验（build.go）
  - [x] 2.1 按 rune 插入关键词构建 Trie，节点含转移表、终止标记、输出信息；末节点带终止符表示完整匹配
  - [x] 2.2 词库校验：空词库 / 空字符串关键词返回可区分的错误；重复关键词去重

- [x] Task 3: 实现失败指针与自动机解析（build.go）
  - [x] 3.1 BFS 计算失败指针：指向「已匹配部分的最长真后缀（且是某关键词前缀）」的 Trie 节点，无则为 root
  - [x] 3.2 构建终止节点的输出信息（该状态结束的全部关键词长度 outLens，降序；含沿失败指针可达者）
  - [x] 3.3 将失败指针解析为全量转移（goto 表）：查询期每 rune O(1) 转移、无运行时回退链（叶子节点共享 fail 表省内存）

- [x] Task 4: 实现 BM 坏字符跳跃表（build.go）
  - [x] 4.1 构建词库 rune 集与 256 位字节过滤器（关键词 UTF-8 编码字节集合）
  - [x] 4.2 root 态安全跳跃逻辑：字节过滤器批量跳过 → rune 解码 + 词库 rune 集判断 → 跳到首个词库 rune，状态保持 root；仅前缀撞过滤器时前进一个 rune 宽度；非法 UTF-8 按 RuneError 处理前进 1 字节

- [x] Task 5: 实现单遍扫描与贪心结果收集（search.go）
  - [x] 5.1 rune 单调前进扫描：可下行则下行，否则走失败转移；T 指针绝不回退
  - [x] 5.2 终止状态经输出链取得以当前位置结尾的候选（outLens 降序遍历，选第一个与 pending 兼容者）
  - [x] 5.3 pending 机制实现非重叠贪心：同起始替换取更长；不重叠提交旧 pending；重叠跳过并尝试更短候选；扫描结束提交剩余 pending
  - [x] 5.4 实现 `FindAll(text)`：单遍扫描收集全部命中，按 Start 升序，无命中返回 nil
  - [x] 5.5 实现 `FindNext(text, offset)`：offset < 0 按 0、offset >= len(text) 返回 (Match{}, false)、offset 落在 UTF-8 字符中间时向后对齐 rune 边界；从 offset 以 root 状态扫描，返回首个贪心命中后立即终止；comma-ok 形式（命中返回 Match 与 true，无命中返回零值与 false）

- [x] Task 6: 单元测试与对照测试（ratchetsearch_test.go）
  - [x] 6.1 基础用例：中文构建、非法词库报错、空文本/无命中返回 nil、非法 UTF-8 不 panic 不漏扫
  - [x] 6.2 贪心语义用例：前缀取最长、前缀未完整出现取短词、重叠取先命中、同结尾取更长、不重叠按序全部输出、嵌套前缀链、连续同词、长词遮蔽下接续命中（{国,人,中国人}+"中国人"→{国,人}）
  - [x] 6.3 FindNext 用例：首个命中、以 End 推进的按需迭代、无命中返回 (Match{}, false)、offset 边界（负数/超出/rune 中间）、FindNext 迭代序列与 FindAll 完全一致（500 组随机）
  - [x] 6.4 随机对照测试：随机词库/文本（500 组），与朴素参照实现（strings.Index 枚举 + 同一套 pending 贪心规则）结果一致
  - [x] 6.5 并发安全测试：8 goroutine 并发 FindAll 与 FindNext 各 100 次，`go test -race` 通过

- [x] Task 7: 示例与基准（example_test.go、bench_test.go）
  - [x] 7.1 可运行示例：中文词库 + 中英混合长文本的 FindAll（含精确 Output），以及 FindNext 按需迭代
  - [x] 7.2 基准：中文长文本、中英混合文本 × 跳跃/无跳跃对照、FindNext 首命中即停（混合文本跳跃版快 ~1.4x；FindNext 快 ~10x）

- [x] Task 8: 质量验证
  - [x] 8.1 `gofmt`、`go vet` 通过；`go test ./...` 与 `go test -race ./...` 全部通过（12 测试 + 2 示例）

- [x] Task 9: 稀疏转移 + fail 回退优化（perf/optimize-automaton 分支）
  - [x] 9.1 新增 New 构建基准（BenchmarkNew100/1k/10k，ReportAllocs），采集 map 版基线（10k 词：878µs/1.08MB/1177 allocs；FindAllChinese 1.87ms、Mixed 1.00ms、FindNextFirst 91.6µs）
  - [x] 9.2 spec 增补「转移表内存布局」需求（DFA 全量 CSR 方案实测构建内存反升、扫描回退 22–48%，废弃；改为稀疏转移 + fail 回退 + 词首字符集跳跃）
  - [x] 9.3 build.go：节点仅存自有 trie 边（CSR 展平）+ fail；BFS 用带回退查找计算失败指针；root 首字符表 map 兼任跳跃集
  - [x] 9.4 search.go：step 改段内查找 + fail 回退；skipForward 改用首字符集（root map）
  - [x] 9.5 验证：全量测试（含随机对照/race）通过；New100 快 2.9x、B/op 省 2.7x、allocs 减半；New10k 快 1.3x、allocs 减半；扫描基准同机交错对比持平或略优（Chinese/Mixed -5% 左右，FindNext 持平）
  - [x] 9.6 白盒测试更新：CSR 区间/升序/目标合法性不变量 + root 首字符表与词库首字符集一致性断言

# Task Dependencies
- Task 2, 4 依赖 Task 1（先有 module 与 API 声明）
- Task 3 依赖 Task 2（先有 Trie）
- Task 5 依赖 Task 3、4（扫描需要自动机与跳跃表）
- Task 6 依赖 Task 5
- Task 7 依赖 Task 5（可与 Task 6 并行）
- Task 8 依赖 Task 6、7

# 实现期修正记录
- 匹配语义由「先命中优先」改为非重叠最左最长（leftmost-longest）：原贪心在真包含词库下输出碎片（{国,人,中国人}+"中国人" → 国、人），不符合设计意图——长词进行中应优先延续，断词才 fail 结算短词。scan 由单值 pending 改为「待提交链」：更左候选弹出链尾、同起点取更长、不重叠入链、其余遮蔽；自动机回 root 或扫描结束时提交整链（root 时刻安全性证明见 spec）。naiveSearch 参照与全部用例同步改写，500 组随机对照在新语义下一致；基准同机交错对比无回退（Chinese/Mixed/FindNext 均持平）。
- 修复 maxOut 遮蔽缺陷：原设计每个结束位置只暴露最长关键词（maxOut 单值），当其与 pending 重叠被跳过时，同位置本可兼容的更短候选（如词库 {国,人,中国人} 的文本 "中国人" 中 "国" 先命中后的 "人"）被一并遮蔽，导致 FindAll 与 FindNext 迭代不一致（随机测试 59/2000 组分歧）。改为节点存全部输出长度 outLens（降序），scan 从最长候选开始选第一个与 pending 兼容者。spec.md 的贪心规则描述已同步更新并补充对应场景。
