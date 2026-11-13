# 答案导入（Vocabulaire Progressif A2/B1 — Corrigés）— 最终状态

源：`Vocabulaire progressif du français — Intermédiaire — Corrigés, 3e édition`
（Claire Miquel / Anne Goliot-Lété, CLE International）。PDF 32 页，**纯扫描无文本层**，
每页一张整页 JPG，且正文页被横放了 90°，需先转正才能阅读。

**结论：词汇 1611 / 1865 道可判分题已有答案（86.4%）；全库 ready = 3486 / 4305。**
`GET /api/answers/status` 实测返回 `{"pending":819,"ready":3486,"total":4305}`。

## 管线（可重跑，幂等）

```
PDF (32p) --pypdf--> %TEMP%/vocab_corriges/vNNN.jpg
                     --PIL rotate(90)--> %TEMP%/vocab_rot/vNNN.jpg   (竖版可读)
        │
data/answers_vocab/raw/vNNN.txt   26 页逐字转录（v004–v029）
        │  tools/parse_vocab.py
        ├─ 按 (章, 主书页, 大题, 印刷题号) 对齐，DP 求保序最优匹配
        ├─ 按题型把答案册文字「转码」成前端提交值
        └─ 校验每个答案键都落在题干占位符范围内
data/answers_vocab/vNN.json       25 个最终答案文件
data/answers_vocab/over/vNN.json 人工补录（填字游戏等，导入时覆盖同名题）
        │  tools/import_vocab.py
french.db                        answer_json + answer_status='ready'
```

重跑全部：
```
python tools/parse_vocab.py && python tools/import_vocab.py
```
（只跑单章：`python tools/import_vocab.py v15.json`）

转录格式见 `raw/FORMAT_VOCAB.md`。有效答案页是 v004–v029（印刷 p.5–p.30）；
v030/v031 是「Activités communicatives」（主书 p.174+，题库只到 p.173），v032 封底。

## 与语法册的三点根本差异

语法册的答案可以直接抄进数据库；**词汇册不行**——练习页提交的是控件编码值：

| 占位符 | 前端提交的值 | 答案册给的是 | 本管线做的转换 |
| --- | --- | --- | --- |
| `{{o:A\|B\|C}}` | 选项**序号**（`"2"`，多选 `"2,3"`） | 选项文字 `madame`，或字母 `a, c` | 逐项映射到序号；`/` 与 `,` 视为可接受变体 |
| `{{c:g}}` | 勾选后 `"1"`，同组互斥，未勾为 `""` | `V` / `F` / `Amour` / `Amitié` / `a` | 从题干提取每个 tick 前的标签，匹配后写对应键 `"1"` |
| `{{b}} {{L}}` | 用户输入的文字 | 文字本身 | 清理断字连字符；`/` 拆成多变体 |

**稀疏键是必须的**：`matchAnswer()` 会把 `answer_json` 的每个键都算进总分，而空的
用户输入会被跳过（永不得分）。若把「未勾选的 Faux 框」也写进答案，这题最多只能拿
50%。所以每题只写**有值的空**，未勾的框直接省略。

其它处理：
- **答案册用 `–` 分隔小题**（语法册用 `…` 分隔同一题的多个空），`/` 表示多个可接受
  答案，`→` 后面是判断题的改正内容。转录时这些符号一个都不能漏。
- **对齐不是按位置硬套**：部分页题库拆得比答案册细。对每页，用「该答案格能成功编码
  该大题多少道题」打分，再做 DP 求保序最优匹配。并用形状奖惩消除平局（纯 V/F 答案
  只能配 tick 题；带 `→` 的改正只能配有文本空的题）。
- **整题 N 空 vs 答案册 N 个编号**：如 ch11 p71 是 1 道题 14 个空，答案册按 1..14
  编号，需合并回一题的空序列。
- **断字**：印刷跨行断词（`jour- nal`）在清理时拼回 `journal`，不误删真正的连字符词
  （`belle-sœur`、`Tchin-tchin`）。

## 口径

- 只导**可客观判分**的题。`{{T}}` 长文框（自由表达 / 看图写话 / 范文）一律跳过：
  答案册给的是示范文，硬判必然误伤。这类共 239 道。
- 图表 / 家谱 / 填字网格无法机械解析，标注 `[FIGURE]` 并跳过；ch15 p105 的 13 个
  填字词由人工读图后写入 `over/v15.json`（横向 1–8 + 纵向 a–e，已用交叉字母验证）。
- 同一空的多个可接受答案存成变体数组，判分时任一命中即算对。

## 剩余 325 题（1936 − 1611）

| 类别 | 题数 | 说明 |
| --- | ---: | --- |
| `{{T}}` 自由表达 / 范文 | 239 | 无唯一答案，**建议保持空缺** |
| 无占位符的纯自由题 | 71 | 题干本身没有可填控件 |
| open 题无对应答案 | 11 | 答案册只给示范段落 |
| 题库选项与答案册语义不对应 | 3 | ch21 p145 q7、ch22 p155 q2/q3：题库把书上填空句改成了选择题，选项里没有答案册给的词，硬填会写错 |
| 印刷题号在答案册中被跳过 | 1 | ch3 p17 e2 q2（书印 5，答案册只给 1/3/4/6/7/9/10） |

也就是说，**可客观判分的题基本已全部覆盖**；剩余几乎全是本来就该人工看的表达题。

## 已知限制

- 一道题若有多个 `{{o}}` 空，答案册每个编号项按逗号拆成各空，再逐个转序号；
  多选题若书上要求「全选」，当前按「任一命中」判分，偏宽松。
- 6 道 `{{L}}` 题的参考答案是 110+ 字符的整段范文（ch4 p27、ch6 p41、ch16 p111），
  逐字判分较严，交卷后建议对照参考答案人工看。
- `{{c}}` 空的参考答案显示为 `"1"`（勾选编码），含义是「勾这一项」。

## 质量核验

- 转录层：v004、v005、v018 与扫描页逐条对标（含 p.105 填字网格横向 1–8 / 纵向 a–e）。
- 结构：1611 条 ready 的答案键全部落在题干占位符范围内，`{{c}}` 值全是 `"1"`、
  `{{o}}` 值全是选项序号，**0 格式错误、0 空值、0 残留省略号、0 空框被写入**。
- `go test -count=1 -run "TestDraftResumeLifecycle|TestFrozenVerdicts|TestAnswerJSONStable|TestSeedAllChapters" .` 通过。
- 重跑解析 + 导入结果一致（1598 自动生成 + 13 人工覆盖 = 1611）。

## 各章覆盖

| 章 | 标题 | 可判分 | 已有答案 | 覆盖 |
| ---: | --- | ---: | ---: | ---: |
| 1 | PRÉSENTATIONS ET USAGES | 61 | 53 | 87% |
| 2 | LA FAMILLE | 37 | 33 | 89% |
| 3 | LES RELATIONS – LES SENTIMENTS | 52 | 49 | 94% |
| 4 | LE CARACTÈRE – LA PERSONNALITÉ | 45 | 38 | 84% |
| 5 | LE TEMPS QUI PASSE | 96 | 70 | 73% |
| 6 | LA MÉTÉO – LE CLIMAT | 65 | 63 | 97% |
| 7 | NATURE ET ENVIRONNEMENT | 54 | 40 | 74% |
| 8 | LE CORPS – LA SANTÉ | 100 | 95 | 95% |
| 9 | L'APPARENCE | 60 | 49 | 82% |
| 10 | LES VÊTEMENTS – LA MODE | 95 | 78 | 82% |
| 11 | LA MAISON – LE LOGEMENT | 78 | 64 | 82% |
| 12 | LES ACTIVITÉS QUOTIDIENNES | 68 | 45 | 66% |
| 13 | LE COMMERCE | 107 | 91 | 85% |
| 14 | CUISINE – RESTAURANT – CAFÉ | 96 | 81 | 84% |
| 15 | LOISIRS – JEUX – SPORTS | 111 | 106 | 95% |
| 16 | TRANSPORTS – CIRCULATION | 98 | 88 | 90% |
| 17 | LE TOURISME – LES VACANCES | 67 | 54 | 81% |
| 18 | L'ENSEIGNEMENT | 81 | 57 | 70% |
| 19 | LA VIE PROFESSIONNELLE | 68 | 45 | 66% |
| 20 | LA TECHNOLOGIE | 83 | 72 | 87% |
| 21 | L'ARGENT – LA BANQUE | 89 | 88 | 99% |
| 22 | DIVERSITÉ, POLITIQUE ET SOCIÉTÉ | 75 | 73 | 97% |
| 23 | LA COMMUNICATION | 58 | 58 | 100% |
| 24 | DÉBATS ET OPINIONS | 55 | 55 | 100% |
| 25 | L'ART ET LA CULTURE | 66 | 66 | 100% |
| | **合计** | **1865** | **1611** | **86%** |
