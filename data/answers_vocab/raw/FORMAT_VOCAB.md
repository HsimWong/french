# 词汇答案册（Vocabulaire Progressif A2/B1 — Corrigés）转录格式

源图：`%TEMP%\vocab_rot\vNNN.jpg`（已从 PDF 抽出并转正，竖版可读）。
输出：`data/answers_vocab/raw/vNNN.txt`（NNN 与图片编号完全一致，例如 v004.jpg → v004.txt）。

## 只允许这 4 种行

```
@CH n        章号 1..25，取自印刷的 "Chapitre N …"；无章号（Activités communicatives）写 @CH 0
@PAGE p      主书页码，取自印刷的 "Exercices p. P" 里的 P（不是答案册自己的页码）
@EX k        黑椭圆圈号里的数字（①②③…），按印刷数字写
- <文本>     该圈号下全部答案正文，压成一行
```

没有任何其它行；不要写解释、标题、Markdown、空行、文件名。整页无答案文字时只写一行 `@EMPTY`。

## 逐条规则

1. 见 `Chapitre 1 Présentations et usages` → `@CH 1`。同页出现下一章章头就再写一行 `@CH N`。
2. 见 `Exercices p. 7` → `@PAGE 7`。同一章会有多个页码（p. 7 / p. 9 / p. 11…），每次都要写。
3. 见黑椭圆里的圈号 `1` `2` `3` → `@EX 1` / `@EX 2` / `@EX 3`。写**印刷的数字**。
4. 每个 `@EX` 下面写**一条** `- ` 开头的行：把该圈号下的所有印刷文字按阅读顺序接成一行，
   印刷换行处用一个空格连接。不要拆成多行，不要重排。
5. **逐字保留，一个字符都不能改**：
   - 小题编号 `1.` `2.` … `10.`
   - 小题之间的分隔短横 `–`（U+2013），以及它两侧的空格
   - 斜杠 `/`（表示多个可接受答案）与箭头 `→`（判断题改正）
   - 括号内容、感叹号、句号、`n°`、`V` / `F` / `Vrai` / `Faux`
   - 法语重音、`'` 与 `’` 原样、`œ`
   - **不要**把 `/` 写成 `|`，不要把 `–` 写成 `-` 或 `...`，不要补全或删除任何词
6. 页首若是上一页某个圈号的续排（本页没有新章头/页头），先补写承接的 `@CH n` 和 `@PAGE p`，
   再写 `@EX <印刷圈号>`，然后转录续排内容。
7. 图表 / 家谱 / 插画类答案：写 `- [FIGURE] <能读出的文字，逐字>`，不要描述连线或位置。
8. `N° 1 – Sam et Fanny : la rupture !` 这类 Activités communicatives 标题：写 `@CH 0`，
   其后的 `Exercices p. 174` 照常写 `@PAGE 174`。标题本身不用写。
9. 看不清的字用 `[?]` 占位，不要猜。不要因为"看起来像错字"而纠正原书。

## 示例（对应 v004 前半，仅供参考格式，不是让你照抄）

```
@CH 1
@PAGE 7
@EX 1
- 1. V → vous – 2. V → tu – 3. V → tu – 4. F → tu (s'ils sont amis) / vous (s'ils ne sont pas amis) – 5. F → vous – 6. F → tu.
@EX 2
- 1. Mathilde / madame – 2. Quentin / mon chéri – 3. Deux – 4. Bisous / Je vous embrasse / Grosses bises – 5. On peut se serrer la main – 6. vous.
@EX 3
- 1. f – 2. V – 3. V – 4. V – 5. V – 6. V.
@PAGE 9
@EX 1
- 1. Je vous en prie, madame. – 2. Je t'en prie. – 3. Asseyez-vous, je vous en prie. – 4. Après vous, je vous en prie.
```
