# Tasks

全部任务已完成（历史实现进度，供追溯；契约与算法见 spec.md）：

- [x] Task 1–2: module 与 API 骨架；rune 级 Trie 构建与词库校验（空词库/空串报错、去重）
- [x] Task 3–4: BFS 失败指针与输出链 outLens；BM 坏字符跳跃表（首字符集 + 256 位字节过滤器）
- [x] Task 5–6: 单遍扫描与结果收集（FindAll / FindNext）；单元测试、500 组随机对照、-race 并发
- [x] Task 7–8: 可运行示例与基准（混合文本跳跃 ~1.4x、FindNext ~10x）；gofmt/vet/lint/test 全链路通过
- [x] Task 9: 稀疏转移 + fail 回退优化（替代 DFA 全量表，决策见 spec「转移表内存布局」）：New 快 1.3–2.9x、allocs 减半，扫描不回退
- [x] Task 10: FindAllOverlapping 重叠全量返回（独立循环，默认路径零改动）
- [x] Task 11: 测试强化与 Go 原生 fuzz（白盒语义重推导、黑盒边界、fuzz 不变量 oracle；449 万 execs 零失败，两项 fuzz 发现转正为修复）
- [x] Task 12: 基准新增朴素多模式对照（`naiveMultiFindAll` / `naiveMultiFindNext`：逐关键词 strings.Index 枚举 + 最左最长归并 / 首命中即停一轮扫描；TestNaiveMultiEquiv 守卫参照与 FindAll/FindNext 在基准语料下等价）。结论：50 词 × 10 万 rune 文本，FindAll 快 ~5.2x（中文）/ ~5.2x（混合）、FindNext 快 ~7.6x；既有基准全部保留无回退
- [x] Task 13: 基准参照重构为三基线体系。naiveFindAll（同自动机无跳跃对照）改为 trieFindAll（纯 Trie 重启：复用 CSR 边与 rootNext、失配即回 root；词尾判定 outLens[0]==路径字节长——成品节点不存 termLen，fail 链继承的真后缀严格更短故等式精确）；新增 bmFindAll（教科书坏字符表逐词搜索）；naiveMulti* 更名 stringsIndex*（体现底层原理）；逐词归并抽为 collectLeftmostLongest 共用；等价守卫改为 TestBaselineEquiv（三参照 × 两语料 + 首命中）。实测（50 词 × 10 万 rune）：中文 FindAll 1.62ms vs Trie 1.52ms（~0.9x，持平属预期：该语料 fail 链收益小、跳跃无空间）/BM 9.13（~5.6x）/Index 9.25（~5.7x）；混合 0.94ms vs Trie 1.26（~1.3x）/BM 6.31（~6.7x）/Index 4.82（~5.1x）；首停 0.089ms vs 0.669ms（~7.6x）。同自动机无跳跃隔离对照随之移除（改以三基线呈现；历史跳跃收益 ~1.4–2x 见 2026-08-29/31 记录）

- [x] Task 16: 接入在线质量服务（零账号即可用的全部落地）：.github/workflows/ci.yml（go 1.27.x/1.28.x/stable 测试矩阵、-race、gofmt/vet、golangci-lint v2.13 与本地配置同源、govulncheck 官方漏洞库扫描、覆盖率上传 Codecov——公仓免 token）；scorecard.yml（OpenSSF Scorecard，publish_results + SARIF 进 Code Scanning）；codeql.yml（GitHub 原生静态安全分析）；sonarcloud.yml + sonar-project.properties（SonarCloud，需 SONAR_TOKEN secret 后自动生效）。README 双语加 7 枚徽章与「质量与 CI」结果表 + 「第三方服务说明」（SonarCloud/Codecov/Scorecard 开通指引按优先级排列）。Snyk/Socket 未接：零第三方依赖，govulncheck 已覆盖标准库漏洞面，接入收益低。

新增任务按 `Task N: 描述` 追加于上表。

# 实现期修正记录（防回归：勿以「简化」或「优化」为由重蹈以下覆辙）

- **匹配语义由「先命中优先」改为非重叠最左最长**：原贪心在真包含词库下输出碎片（{国,人,中国人}+"中国人" → 国、人），不符合设计意图——长词进行中优先延续，断词才 fail 结算短词。scan 由单值 pending 改为「待提交链」（规则与 root 时刻提交的安全性证明见 spec）。
- **maxOut 单值遮蔽缺陷**：每个结束位置只暴露最长关键词时，其被 pending 遮蔽会连带丢失同位置更短的兼容候选，导致 FindAll 与 FindNext 迭代不一致（随机测试 59/2000 组分歧）。修正：节点存全部输出长度 outLens（降序）。
- **必死候选弹链导致空档**（fuzz 发现）：词库 {0,000} 文本 "000000000001" 中，更左但被遮蔽的候选弹出链尾，为它让位的合法命中被丢弃，[9,10) 成空档，且无状态 FindNext 无法复现。修正：被遮蔽候选无权改变链（弹出可恢复）；flush 移至 root 循环头（消除单 rune 词的时序漏洞）；FindNext 改整链首条产出；naiveSearch 参照改写为独立的教科书式重启贪心，不与被测链规则同构。
- **New 词库校验加固**（fuzz 发现）：关键词非法 UTF-8 在 rune 层坍缩为同一 RuneError（身份歧义），规范 U+FFFD 的 3 字节编码与查询端逐字节 RuneError 前进不一致（长度歧义，极端情形切片 panic）。二者均拒绝入词；文本侧非法字节不受影响（仍逐字节处理）。
- 2026-08-31 文档精简：README 收敛为用户视角（选型依据与权衡），spec/tasks/checklist 压缩历史、保留设计决策与修正记录。
- 2026-08-31 README 双语化：英文版置 README.md，中文原版迁移至 README_CN.md，两文件开头互链；正文内容与迁移前一致，无行为变化。
- 2026-08-31 更名 ratchetsearch → ratchetmatch：原名「search」与本库模式匹配语义不符且与 API 家族（Find*/Match）不一致，"ratchet search" 又是系统发生学既有术语、Go 生态另有知名同名工具，检索混淆重。新名保留棘轮隐喻（文本指针单向只进不退），以 match 修正语义。仓库无下游引用，零迁移成本。
- 2026-08-31 确定许可证为 MIT：宽松许可（允许闭源商用，仅要求保留版权声明）+ 不含专利条款（本人决策，算法库专利风险自评可控）+ 企业采用友好（MIT 认知度最高、法务审查零成本）。署名 Jayce Chant（陈思杰）。
- 2026-09-01 README 性能段前移至「适用范围」之后、「设计与权衡」之前，朴素多模式对比由行文改为表格呈现（双语同步，无行为变化）。
- [x] Task 15: 基准词库扩为两套各 100 词：稀疏（互不重叠/包含，原 50 词扩至 100）与重叠（15 个词族、前缀链/包含/子串 + 单字词「网」「间」，init 断言每套恰 100 词且无重复）；每套配纯中文/混合两份语料与自动机、BM 表；基准全部翻倍（*Overlap 后缀），TestBaselineEquiv 扩为两套词库 × 两份文本，ratchetmatch_test.go 三处 benchKeywords 引用改指稀疏词库。实测（各 100 词 × 10 万 rune）：逐词基线随词库翻倍（Index 中文 9.25→19.1ms）；重叠词库自动机仅小幅变慢（FindAll 中文 1.71→1.94ms），逐词 BM 因原始出现暴涨显著变慢（16.4→21.4ms）；混合文本无跳跃 Trie 1.9x；重叠词库噪声字命中词首字符使首停回落（0.09→0.13ms，~12.5x）。
- [x] Task 14: BM 参照口径修正：坏字符表改为 init 期预构建（benchBMTables），与自动机/归并表的构建成本同置基准循环外（bmFindAll 改接表签名）。复测：BM 中文 9.09ms（~5.6x）、混合 5.87ms（~6.4x），混合较表内构建（6.31ms）省 ~7%；逐词基线仍慢于 strings.Index 的 SIMD 全文扫描。
- 2026-09-01 基准参照体系改版：NoSkip（同自动机去跳跃）对照被纯 Trie 重启基线取代，跳跃收益不再能从基准套件直接复现（历史值保留于本文件与 checklist）；三基线（Trie/BM/strings.Index）与正式 API 的等价由 TestBaselineEquiv 在基准语料上锁定。勿再以为基准中存在无跳跃对照。
- 2026-09-01 移除 goreportcard（双语 README 徽章与质量表行、第三方服务说明提及，checklist 同步）：官方宣布停止服务；其检查项（gofmt/go vet/lint）已由 CI 的 golangci-lint 覆盖，无功能损失。不引入新徽章替代——质量信号已由 CI/Codecov/CodeQL/Scorecard/SonarCloud 充分覆盖。
- 2026-09-01 朴素基线形态定论（分析结论，未改基准代码）：strings.Index 已是标准库 SIMD（internal/bytealg，AVX2/SSE2）最快单串搜索；「文本下标外循环 × 逐词逐字节比较」同为 O(K·n) 但常数差一个向量宽度（~1500 万标量迭代 vs ~50 万向量迭代），只会持平或更慢。更强的「首字节位图+同首字符桶」本质是深度 1 trie（与被测 byteFilter/rootNext 同构），不算朴素基线，不入基准；理由固化于 bench_test.go naiveMultiFindAll 注释与 README。
- 2026-09-02 README 移除「质量与 CI」结果表与文末「第三方服务说明」（双语同步）：README 首先面向用户，质量状态由顶部徽章呈现即可；面向 owner 的一次性开通指引不属于用户内容。遗留开通要点（均为仓库外一次性操作）：SonarCloud 网页导入仓库、配 `SONAR_TOKEN` secret、Analysis Method 切 CI-based（关闭 Automatic Analysis，可选装 GitHub App）；Codecov 配 `CODECOV_TOKEN`（工作流已就绪）；Scorecard 提分需 master 分支保护 + 依赖图。未新增独立文档承载——遗留项以本条与 checklist CI 条目为准。
- 2026-09-02 SonarCloud 覆盖率 0% 修复：sonar-project.properties 误用通用属性 `sonar.coverage.reportPaths`（只认 Generic/Cobertura XML），Go coverprofile 文本格式被静默丢弃，面板恒 0%；改用 Go 专用属性 `sonar.go.coverage.reportPaths`（CI 扫描本身一直成功，故无任何报错线索）。顺带把 CI 产物 coverage.txt 补进 .gitignore（coverage.txt 非本地任务产物，原 *.out 规则未覆盖）。勿再把 Go 覆盖率挂到通用 reportPaths 属性上。
- 2026-09-02 README 徽章补齐 SonarCloud 与 govulncheck（双语同步）：govulncheck 原是 ci.yml 的 job，GitHub Actions 徽章是 workflow 级而非 job 级、无法单独展示，遂拆为独立 vulncheck.yml（触发条件与 ci.yml 一致，内容不变），获得独立徽章；SonarCloud 加官方 Quality Gate 徽章（alert_status）。徽章总数 6→8，排序按静态检查/漏洞/覆盖率/评分归组。
- [x] Task 17: scan 可读性重构（纯等价变换，零行为变化）：候选归并规则（弹链比较 + 四分支）抽为 mergeCandidate、链提交抽为 flushChain（原闭包改包级函数），scan 主体从 ~55 行/5 层嵌套缩至 ~26 行/3 层；规则注释随逻辑下沉到对应子函数。逃逸分析确认 mergeCandidate 的 chain 参数「leaking param to result level=0」、scan 内联数组不逃逸，零分配语义保持；实测（100 词 × 10 万 rune，改动前后各一轮）：FindAll 中文 1.82→1.70ms / 混合 1.01→0.91ms、FindNextFirst 102→85µs，allocs 完全一致（10–12 / 2），无回退。勿再以「内联闭包更高效」为由回退包级函数形态。
- [x] Task 18: 新增 `example/` 完整示例集（独立 module，`replace` 指向仓库根，与主模块互不干扰——主模块 `go build ./...`/CI 门槛不感知示例目录）：`doc.go`（目录导览 + 复制说明）与四个自包含程序，每个单文件 `package main`，复制出去改词库即可用：`basic`（最小上手：New + FindAll + 字节偏移/rune 换算说明）、`semantics`（同一文本 FindAll vs FindAllOverlapping 对照 + 长词未完整出现取最长完整前缀的边界）、`iterate`（FindNext 首停 + End 推进迭代 + 前 N 条即停 + 越界/rune 对齐边界）、`wordcount`（词频统计：含包含关系词库下 FindAllOverlapping 全量计数 vs FindAll 非重叠对照，slices.SortFunc 稳定排序输出）。验证：example 模块 go build/vet/gofmt/go fix/golangci-lint（0 issues）通过，四个程序逐个 go run 输出核对无误；主模块全链路（test/-race）不受影响。
- [x] Task 19: WithCaseFold 大小写折叠查询（feat/case-fold 分支）。API 形态讨论结论（用户定夺）：不新增方法，Find 系列改变参 `opts ...Option`（源兼容，不传 = 精确行为不变）；可扩展性留给后续构建期/查询期特性。实现要点：
  - **查询期换 EqualFold 比较不可行**：同一精确自动机上折叠比较会漏报词库内折叠变体（{Stop,stop} 只能走其中一支）——折叠合一必须在构建期完成（fold 相等的分支共享节点，outLens 天然并集）。
  - **折叠自动机构建期生成、首次 fold 查询时惰性构建**（用户要求）：Matcher.once/froot/folded 三字段，sync.Once 串行化并发首建；词库不驻留内存，由精确 trie 无损还原（trieKeywords：词尾判据 outLens[0]==路径字节长，与基准 trieFindAll 同源）。
  - **首字符 map 筛选（rootNext/byteFilter/CSR 边键）方案 = 轨道展开**：SimpleFold 轨道互不相交、每轨道 ≤4 成员，构建期把每个折叠代表边展开为全部轨道成员（CSR 段内仍严格升序，二分/线性查找零改动；rootNext 含全轨道，skipForward 判据不变）。查询热路径与精确模式共用同一套代码，零分支零归一。
  - **命中区间宽度差陷阱**：轨道成员 UTF-8 宽度可不同（K U+212A 3B vs k 1B），outLens 字节长不可回退起点。fold 自动机输出改存（规范字节长, 关键词 rune 数）双数组 outLens/outRunes（同节点重复插入经 sort+compact 去重），命中按 rune 数从 End 前走 rune 边界提取（runeStartBack）；Match.Keyword 为文本原样切片。
  - fold 语义 = 逐 rune SimpleFold 轨道等价（strings.EqualFold），无展开式折叠（ß 不匹配 ss）；非法字节 RuneError 不在任何轨道，行为同精确模式。
  - 测试：casefold_test.go（轨道白盒、语义表、300 组随机 oracle——逐位置 strings.EqualFold 全量枚举 + 贪心最左最长、并发惰性构建 -race）；FuzzMatch 追加 fold oracle 段与种子；45s fuzz 13.6 万 execs 零失败。
  - 验证：build/vet/gofmt/go fix/golangci-lint（0 issues）/test/-race 全过；基准无回退（FindAll 中文 1.84ms / 混合 0.99ms / 首停 101µs，allocs 13/11/3 与历史一致——resolve 变参展开与 !folded 分支不损零分配语义）。
- 2026-09-03 结构重构（用户要求，Task 20）：fold 与非 fold 逻辑彻底分离——两套私有自动机 exactMatcher（exactNode：outLens 字节长）/ foldMatcher（foldNode：outRunes rune 数）共用泛型扫描引擎 machine[N]（nodeAPI 约束接口承载 seg/outs/start 三方法差异；两节点类型布局不同 → gcshape 各自单态化，热路径零接口开销），对外统一收敛在导出的 Matcher（exact/fold/once/foldOnly）；New 加变参：`New(kws, WithCaseFold())` = fold-only 模式（仅构建折叠自动机，所有查询固定折叠语义且无法关闭），默认仍为惰性构建。**陷阱记录**：filter 置位曾误用 `rune(kw[0])`（首字节 → U+00E5 而非首 rune 的编码首字节，多字节关键词全 skipForward 跳过、扫描零命中）；修复为按首 rune setFilterBit。**并发陷阱记录**：foldEngine 的 `if m.fold != nil` 快路径在 once.Do 外读 m.fold，-race 报真数据竞争；修复为无条件 once.Do（Once 快速路径自身原子且提供 happens-before）。fuzz 增加 fold-only == 惰性 fold 一致性不变量；-race/30s fuzz 26 万 execs 全过，基准 allocs 与时延无回退（泛型引擎 1.85/1.01ms、首停 93µs、allocs 13/11/3）。
- 2026-09-03 API 定型（用户要求，Task 21）：**惰性构建与 Find 查询期选项整体取消**。业界调研结论（Hyperscan 逐模式 HS_FLAG_CASELESS / aho-corasick builder 的 ascii_case_insensitive / RE2 编译期 (?i)）一致表明：折叠是构建期属性，主流引擎均不支持查询期切换；「双模式单实例」并无生产先例，不该替用户决定。定型形态：
  - **导出 Matcher 变为密封接口**（未导出方法 isInternal 保障演进自由），`exactMatcher` / `foldMatcher` 由类型别名升级为真实类型（嵌入 machine[N] 获得方法集），分别实现接口；`CaseFold() bool` 供调用方判别模式。类型即模式，无「字段二选一」的非法状态，无运行时分支。
  - **Find 系列签名回归无变参**：FindAll(text) / FindAllOverlapping(text) / FindNext(text, offset)——与 v0.1.0 签名一致；模式选项仅存在于 New。
  - 需同词库双模式 = 分别 New 两个实例（aho-corasick 惯例）。
  - 删除：惰性构建（once/sync）、foldEngine、trieKeywords（从精确 trie 还原词库的整套机制——fold 现直接从关键词构建）、matcher 外壳双字段。
  - 文件重排同步：matcher.go（接口+New）/ option.go（Option/WithCaseFold）/ engine.go（节点+引擎）/ build.go（双构建管线）。
  - 验证：全链路门槛 + example 模块通过；30s fuzz 全过；基准无回退（接口分发的 itab 开销相对扫描本身可忽略，FindAllMixed 1.02ms / 11 allocs 持平）。
- 2026-09-03 SonarCloud 覆盖率排除 example/：example 目录并入仓库（Task 18）后，`sonar.sources=.` 会把示例代码计入覆盖率分母，而 coverage.txt 只含根模块（example 是独立 module，`go test ./...` 不编译它），SonarCloud 面板覆盖率因此被拉低；Codecov 不受影响（只消费 coverage.txt）。修正：sonar-project.properties 的 `sonar.exclusions` 追加 `**/example/**`。示例程序的验证靠运行核对而非单测覆盖，不参与覆盖率统计是预期行为。
- [x] Task 22: case fold 测试强度审计 + exact 零影响验证 + fold 性能基准（用户要求）：
  - **测试强度**：语句覆盖率 100%（-race + coverprofile，仅 isInternal 空方法 0%），但语句覆盖 ≠ 分支覆盖——审计发现 fold 从未专项测试 CSR 二分分支（段宽 >16）与 runeStartBack 跨混合宽度 rune 回退；新增 TestCaseFoldBinaryBranch（17 个 P? 词：轨道展开后 root 为 P/p 两键同目标、P 节点段宽 35=17×2+1（K 为 p 轨道第三成员））与 TestCaseFoldRuneStartBackMixed（k/K/U+212A 轨道 1B↔3B + ÿ/Ÿ 轨道 1B↔2B，文本窄宽交错、同 rune 数不同字节）。两测试首版均失败，根因均为测试构造错误（未考虑轨道展开；误以为全角Ａ与 ASCII a 同轨道——SimpleFold 不跨全半角），实现无缺陷。
  - **exact 零影响**：全量测试 + -race 通过、覆盖率 100%；基准 FindAllMixed 0.98ms/10 allocs（历史 1.02ms/10-11）、FindNextFirst 100µs/2 allocs——密封接口定型后 exact 路径无行为/性能变化。
  - **fold 性能**（新增 bench_fold_test.go，TestFoldBenchEquiv 锁定「同词库同文本命中一致」的对照口径）：构建 fold/exact = 1.42x（100 词）/1.19x（1k）/1.06x（10k），内存 +24%→+2%（轨道解析与展开键写入为一次性成本，随词库增大摊薄）；查询：纯中文词库（无轨道展开）与 exact 持平（±5% 噪声内）；ASCII 词库（轨道展开键数 ~2x，最坏情况）同文本 fold 3.05ms vs exact 3.04ms 持平，且 fold 多命中约 1000 条 exact 漏报（首字母大写词）——查询侧开销可忽略，构建侧小幅可测。
  - README 双语「大小写折叠」小节增补性能提示（查询持平 / 构建 1.4x→1.1x 随词库摊薄 / 语义收益；建议需要大小写不敏感即开 WithCaseFold）。
