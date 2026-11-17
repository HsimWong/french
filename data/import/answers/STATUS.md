# 答案导入（Corrigés）— 最终状态

源：`Grammaire progressive du français A2B1 — Corrigés`（81 页，**纯扫描无文本层**，本机无 OCR）。
页面图片已用 pypdf 抽出：`%TEMP%\corriges\p001.jpg … p081.jpg`（每页一张整页 JPG）。

**结论：语法 1875 / 2213 道可判分题已入库（84.7%），全库 1875 / 2369 题。**
数据落盘在 `data/answers/gNN.json`，DB 里已写入（`answer_status='ready'`），重启/重播种不丢。

## 管线（可重跑，幂等）

```
data/answers/raw/pNNN.txt     逐页逐字转录（@CH / @PAGE / @EX / "k. …答案…"）
        │  tools/parse_corriges.py
        ├─ 按 (章, 主书页, 该页第几个大题, 印刷题号) 对齐到 question
        ├─ 空数校验：答案段数必须等于题干 {{b/L/T/c}} 占位符数
data/answers/over/gNN.json    agent 人工补录（同样受空数校验，不过则拒收）
        │
data/answers/gNN.json         最终答案（导入器输入）
        │  tools/import_answers.py
french.db                     answer_json + answer_status='ready'
```

重跑全部：
```
python tools/parse_corriges.py && python tools/import_answers.py
```

## 对齐规则（已用 p006/p030 逐条对标核验）

- 章头 `N. TITRE. Exercices p. P` → 语法章 `seq=N`，主书页码 `P` = 数据库 `exercise.page_no`。
- 同页圈号 ①②③… 按 **该页内的 `exercise.seq` 顺序**对应（不是全局 seq）。
- 大题内 `k.` = **书里的印刷题号**。库里 `question.seq` 是位置序号，26 个大题两者差 1
  （书印 2..5、库存 seq 1..4），因此按题干开头的数字对齐，而不是按 seq 硬套。
- 多空：答案册用 `…` 分隔各空；转录保留全部 `…`，解析时取「非空段」作为各空答案。
  答案在省略号之前/之后的两种排版、末尾多一个省略号的排版都覆盖到了。
- 同一空给两个可接受答案（`le/la`、`Chaque … Chacun`）→ 存成 `le|la`，判分时任一命中即算对。

## 口径

- 只导**可客观判分**的题（题干含占位符）。
- 跳过：`Réponses possibles` / `Exemples` / `Questions possibles` 这类自由范文（无唯一正确答案）、
  `open` 写作题、以及答案册与题库粒度不对应的表格题。
- 判分容错 `normal`：大小写不敏感、去首尾标点、统一撇号。

## 剩余 338 题的构成（未导入）

| 类别 | 题数 | 说明 |
| --- | ---: | --- |
| 自由范文 | 224 | 答案册只给例句范文（"Réponses possibles"），没有唯一答案，判分必然误伤 |
| 列表块未对齐 | 64 | 答案册把整题写成一行逗号列表，项数与题库空数对不上 |
| 编号项空数不符 | 47 | 一个印刷题号里塞了多个小例（如 `… une … J'ai mis la couverture sur le lit.`） |
| 无对应格 | 3 | 页码/大题在转录中未落到该章 |

## 各章覆盖

见下方表格（可判分 = 题干含占位符的题）。

| 章 | 标题 | 可判分 | 已有答案 | 覆盖 |
| ---: | --- | ---: | ---: | ---: |
| 1 | LE VERBE « ÊTRE » | 33 | 30 | 91% |
| 2 | L'ADJECTIF (1) | 62 | 56 | 90% |
| 3 | LA NÉGATION et L'INTERROGATION (1) | 51 | 36 | 71% |
| 4 | LE NOM et L'ARTICLE | 102 | 98 | 96% |
| 5 | « C'EST » et « IL EST » | 27 | 13 | 48% |
| 6 | LES POSSESSIFS | 23 | 22 | 96% |
| 7 | LES NOMS de PARENTÉ et de GROUPE | 9 | 8 | 89% |
| 8 | LES DÉMONSTRATIFS | 21 | 20 | 95% |
| 9 | « IL Y A » et « C'EST » | 14 | 10 | 71% |
| 10 | LA SITUATION dans l'ESPACE (1) | 27 | 21 | 78% |
| 11 | LE VERBE « AVOIR » | 76 | 59 | 78% |
| 12 | L'ADJECTIF (2) | 40 | 39 | 98% |
| 13 | LES NOMBRES | 62 | 59 | 95% |
| 14 | LE TEMPS (1) | 40 | 29 | 72% |
| 15 | LES INDÉFINIS | 53 | 47 | 89% |
| 16 | LES VERBES en « -ER » au PRÉSENT | 63 | 51 | 81% |
| 17 | LE TEMPS (2) | 16 | 11 | 69% |
| 18 | L'ADVERBE | 35 | 25 | 71% |
| 19 | L'EXPRESSION de la QUANTITÉ | 26 | 22 | 85% |
| 20 | LE PRONOM « EN » | 35 | 28 | 80% |
| 21 | LA SITUATION dans l'ESPACE (2) | 24 | 24 | 100% |
| 22 | LE COMPARATIF et LE SUPERLATIF | 70 | 65 | 93% |
| 23 | LE VERBE « ALLER » | 18 | 15 | 83% |
| 24 | LE PRONOM « Y » | 17 | 17 | 100% |
| 25 | LA SITUATION dans l'ESPACE (3) | 14 | 14 | 100% |
| 26 | LES VERBES en « -IR », « -OIR » et « -RE | 149 | 135 | 91% |
| 27 | LES VERBES PRONOMINAUX | 17 | 14 | 82% |
| 28 | LES PRONOMS COMPLÉMENTS | 81 | 76 | 94% |
| 29 | LES PRONOMS TONIQUES | 21 | 18 | 86% |
| 30 | L'IMPÉRATIF | 24 | 23 | 96% |
| 31 | LE CONDITIONNEL (1) | 21 | 17 | 81% |
| 32 | LES RELATIFS | 70 | 63 | 90% |
| 33 | L'INTERROGATION (2) | 44 | 39 | 89% |
| 34 | LA NÉGATION (2) | 31 | 26 | 84% |
| 35 | LE DISCOURS INDIRECT au PRÉSENT | 6 | 5 | 83% |
| 36 | LE GÉRONDIF | 18 | 17 | 94% |
| 37 | LES PRÉPOSITIONS et LES VERBES | 31 | 25 | 81% |
| 38 | LES VERBES de DÉPLACEMENT | 22 | 16 | 73% |
| 39 | LE FUTUR PROCHE | 73 | 59 | 81% |
| 40 | LE PASSÉ COMPOSÉ | 239 | 191 | 80% |
| 41 | LE TEMPS (3) | 36 | 30 | 83% |
| 42 | LE PASSIF | 18 | 18 | 100% |
| 43 | L'IMPARFAIT | 66 | 57 | 86% |
| 44 | LE PLUS-QUE-PARFAIT | 12 | 10 | 83% |
| 45 | LE DISCOURS INDIRECT au PASSÉ | 32 | 31 | 97% |
| 46 | « VENIR de », « ÊTRE EN TRAIN de », « ÊT | 18 | 15 | 83% |
| 47 | LE FUTUR SIMPLE | 16 | 16 | 100% |
| 48 | LE FUTUR ANTÉRIEUR | 11 | 9 | 82% |
| 49 | LE CONDITIONNEL (2) | 12 | 11 | 92% |
| 50 | LES HYPOTHÈSES | 57 | 55 | 96% |
| 51 | LE SUBJONCTIF | 75 | 53 | 71% |
| 52 | LES RELATIONS LOGIQUES | 55 | 27 | 49% |
| | **合计** | **2213** | **1875** | **85%** |
