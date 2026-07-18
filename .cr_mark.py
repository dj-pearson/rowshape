#!/usr/bin/env python3
"""Mark a phase-cr story done in prd.json, preserving the file's formatting.

Usage: python .cr_mark.py CR-T6 note.txt

Scoped by task id (not a global text anchor) because several stories share the
same passes/status/priority tail, which silently matched the wrong task twice.
Validates the JSON BEFORE writing, after a hand-escaped note once corrupted the
file with a raw newline inside a string.
"""
import io
import json
import re
import sys

TASK_ID, NOTE_PATH = sys.argv[1], sys.argv[2]
note = io.open(NOTE_PATH, encoding="utf-8").read().strip()

path = "prd.json"
lines = io.open(path, encoding="utf-8").read().split("\n")

# Locate this task's line range: from its "id" line to the next task's "id" line.
start = next(i for i, l in enumerate(lines) if l.strip() == '"id": "%s",' % TASK_ID)
end = len(lines)
for i in range(start + 1, len(lines)):
    if re.match(r'^\s*"id": "', lines[i]):
        end = i
        break

# Within that range only, flip the work signal.
flipped_passes = flipped_status = False
for i in range(start, end):
    if lines[i].strip() == '"passes": false,':
        lines[i] = lines[i].replace("false", "true")
        flipped_passes = True
    elif lines[i].strip() == '"status": "todo",':
        lines[i] = lines[i].replace('"todo"', '"done"')
        flipped_status = True
assert flipped_passes and flipped_status, (TASK_ID, flipped_passes, flipped_status)

# Insert the note as the last key of this task object: find the task's closing
# brace (the last line before `end` that closes at task indent) and append.
close = max(i for i in range(start, end) if lines[i].rstrip() in ("        },", "        }"))
last = close - 1
if not lines[last].rstrip().endswith(","):
    lines[last] = lines[last].rstrip() + ","
lines.insert(close, '          "note": ' + json.dumps(note, ensure_ascii=False))

out = "\n".join(lines)
json.loads(out)  # validate before writing
io.open(path, "w", encoding="utf-8", newline="\n").write(out)

d = json.loads(out)
t = [x for p in d["phases"] for x in p["tasks"] if x["id"] == TASK_ID][0]
assert t["passes"] is True and t["status"] == "done" and t["note"] == note
print("%s -> passes:true, note %d chars, JSON valid" % (TASK_ID, len(t["note"])))
