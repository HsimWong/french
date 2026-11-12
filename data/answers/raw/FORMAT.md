# 转录格式规范（Corrigés 答案页 → 机器可解析文本）

源图片：`C:/Users/WangXin/AppData/Local/Temp/corriges/pNNN.jpg`（NNN = 三位 PDF 页码，如 `p006.jpg`）。
每页输出一个文件：`data/answers/raw/pNNN.txt`（与本页图片同编号）。

## 必须遵守

1. **逐字转录**，保留所有法语重音（é è ê à ç ù î ï ô œ « »）、大小写、标点。不翻译、不改写、不纠错、不补答案。
2. **省略号必须原样保留**，使用单个字符 `…`（U+2026），不要写成 `...` 或 `......`。
   省略号在答案里标记「一个空」，数量必须与书中完全一致——这是后续自动对空的唯一依据。
3. **每条答案项压缩成一行**（印刷上跨行的一律接成一行，不要在中间断行）。
4. 不要输出 Markdown 标题、表格、项目符号以外的任何装饰。不要输出解释性文字。

## 行类型（只有这几种）

```
@CH 12                       ← 遇到章头「12. L'ADJECTIF (2). Exercices p. 55.」时先输出
@PAGE 55                     ← 紧接着的 "Exercices p. N" 里的 N（主书页码，不是 PDF 页码）
@EX 1                        ← 遇到圈号 ① 时；每个 @PAGE 组内从 1 重新计数，按印刷顺序递增
2. … sont réservés.          ← 答案项：印的题号 + 点 + 空格 + 原文（保留 …）
3. … sont petits et minces.
- … e … es … nes … e.         ← 无题号的块（词形表、整段示范），以 "- " 开头，整块一行
- Réponses possibles : …     ← "Réponses possibles" 之类自由答案，以 "- " 开头
```

## 关键细节

- `@CH` 的 N 取章头开头的数字；`@PAGE` 取同一行 `Exercices p. N` 的 N。
- 同一页可能连续出现多个章头（例如第 6 页同时有「2. … p. 15」和「2. … p. 17」），每个章头都要重新输出 `@CH` + `@PAGE`。
- 圈号每次换 `@PAGE` 后从 1 重新开始：①②③④… → `@EX 1` `@EX 2` `@EX 3` `@EX 4`…
- 圈号内没有题号、直接是省略号串的（词形表题），输出 `@EX k` 然后一行 `- ` 块。
- 题号可能不从 1 开始（示范题被省略，如从 `2.` 起），**照抄实际题号**，不要重编号。
- 遇到页眉页脚（书名、页码数字）不输出。
- 整页空白或不含答案时，只输出一行 `@EMPTY`。

## 示例（对应 p006 上半页的真实内容）

```
@CH 2
@PAGE 15
@EX 1
2. … sont réservés.
3. … sont petits et minces.
4. … sont roux.
5. … sont jaloux.
@EX 2
- … courts … serrés … classiques … ringards … démodés.
@EX 3
2. C'est vrai, ils sont beaux tous les deux.
3. C'est vrai, ils sont enrhumés tous les deux.
4. C'est vrai, ils sont originaires tous les deux.
@EX 6
- Réponses possibles : des manteaux noirs, des chemisiers blancs, …
```
