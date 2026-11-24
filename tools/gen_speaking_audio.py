#!/usr/bin/env python3
"""为镜头练习的终稿生成法语音频。

约定：
  data/speaking-audio/p{练习ID}-v{版本号}-{音色}.mp3
由 speaking.go 的 GET /api/speaking/audio/{id}/{version}/{voice} 提供。

只处理 kind='final' 的版本；已存在且大于 2KB 的文件会跳过，
所以反复运行是安全的（定时任务每 10 分钟跑一次也没问题）。
"""
import asyncio
import os
import sqlite3
import sys
from pathlib import Path

import edge_tts

VOICES = {
    "henri": "fr-FR-HenriNeural",    # 男声，跟读用
    "denise": "fr-FR-DeniseNeural",  # 女声，辨音用
}

ROOT = Path(os.environ.get("FRENCH_ROOT", "/home/admin/maintain/french"))
DB = os.environ.get("DB_PATH", str(ROOT / "french.db"))
OUT = Path(os.environ.get("SPEAKING_AUDIO_DIR", str(ROOT / "data" / "speaking-audio")))
MIN_BYTES = 2048


async def synth(text: str, voice: str, path: Path) -> None:
    tmp = path.with_suffix(".mp3.part")
    comm = edge_tts.Communicate(text, voice)
    await comm.save(str(tmp))
    if tmp.stat().st_size < MIN_BYTES:
        tmp.unlink(missing_ok=True)
        raise RuntimeError("生成的音频过小，可能是接口返回异常")
    tmp.replace(path)


async def main() -> int:
    if not Path(DB).exists():
        print("数据库不存在:", DB, file=sys.stderr)
        return 1
    OUT.mkdir(parents=True, exist_ok=True)

    conn = sqlite3.connect(DB, timeout=15)
    conn.row_factory = sqlite3.Row
    rows = conn.execute(
        """
        SELECT p.id AS practice_id, r.version, r.text
        FROM speaking_practice p
        JOIN speaking_revision r ON r.practice_id = p.id AND r.kind = 'final'
        WHERE TRIM(r.text) <> ''
        ORDER BY p.id, r.version DESC
        """
    ).fetchall()
    conn.close()

    made = skipped = failed = 0
    for row in rows:
        text = row["text"].strip()
        for voice in VOICES:
            path = OUT / f"p{row['practice_id']}-v{row['version']}-{voice}.mp3"
            if path.exists() and path.stat().st_size >= MIN_BYTES:
                skipped += 1
                continue
            try:
                await synth(text, VOICES[voice], path)
                made += 1
                print(f"生成 {path.name} ({path.stat().st_size / 1024:.0f} KB)")
            except Exception as exc:  # 单个失败不影响其它条目
                failed += 1
                print(f"失败 {path.name}: {exc}", file=sys.stderr)

    print(f"完成：新增 {made}，跳过 {skipped}，失败 {failed}")
    return 0 if failed == 0 else 2


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
