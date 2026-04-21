#!/usr/bin/env python3
"""
检查 claude-spy 日志中的原始 usage 字段，用于排查 token 解析问题。
用法: python3 check_usage.py [日志文件路径]
      不传路径则自动取 ~/.claude-spy/logs/ 下最新的文件
"""
import json
import sys
import os
import glob


def find_latest_log():
    log_dir = os.path.expanduser("~/.claude-spy/logs")
    files = glob.glob(os.path.join(log_dir, "*.jsonl"))
    if not files:
        print(f"未找到日志文件，目录: {log_dir}")
        sys.exit(1)
    return max(files, key=os.path.getmtime)


def main():
    path = sys.argv[1] if len(sys.argv) > 1 else find_latest_log()
    print(f"日志文件: {path}\n")

    with open(path) as f:
        for i, line in enumerate(f):
            line = line.strip()
            if not line:
                continue
            rec = json.loads(line)
            req_id = rec.get("id", f"req_{i+1}")
            model = ""
            try:
                model = json.loads(rec["request"]["body"]).get("model", "")
            except Exception:
                pass

            raw_usage = rec.get("response", {}).get("raw_usage")

            # 兼容旧日志（没有 raw_usage 字段时从 body 里提取）
            if raw_usage is None:
                try:
                    body = rec["response"]["body"]
                    if isinstance(body, str):
                        body = json.loads(body)
                    raw_usage = body.get("usage")
                except Exception:
                    pass

            print(f"[{req_id}] model={model}")
            print(f"  raw_usage = {json.dumps(raw_usage, ensure_ascii=False)}")

            # 模拟当前解析逻辑，显示最终 in/out
            if isinstance(raw_usage, dict):
                in_tokens = raw_usage.get("input_tokens") or raw_usage.get("prompt_tokens") or 0
                out_tokens = raw_usage.get("output_tokens") or raw_usage.get("completion_tokens") or 0
                print(f"  解析结果: in={in_tokens}  out={out_tokens}")
            print()


if __name__ == "__main__":
    main()
