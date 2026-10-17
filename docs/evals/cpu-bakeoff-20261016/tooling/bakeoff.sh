#!/bin/bash
# name|hub-repo|hub-tag
cd "$(dirname "$0")/.."
while IFS='|' read -r name repo tag <&3; do
  f=models/${name//[:\/]/_}.gguf
  echo "=== $name ($repo:$tag) $(date -u +%H:%M)"
  if docker exec ollama ollama show "$name" >/dev/null 2>&1; then echo "already imported"; else
  [ -s "$f" ] || scripts/fetch.sh "$repo" "$tag" "$f" || { echo "FETCH FAILED $name"; continue; }
  printf 'FROM /models/%s\n' "${f#models/}" > "$f.Modelfile"
  docker exec ollama ollama create "$name" -f "/models/${f#models/}.Modelfile" >/dev/null 2>&1 || { echo "CREATE FAILED $name"; rm -f "$f"; continue; }
  rm -f "$f"
  fi
  [ -f "runs/${name//[:\/]/_}/report.json" ] && continue
  python3 scripts/run_model.py "$name" --label "${name//[:\/]/_}" --cases scripts/subset12.json 2>&1 | grep -E "^  |Error|Traceback" 
  docker exec ollama ollama rm "$name" >/dev/null
done 3< "${1:-scripts/models.txt}"
echo "ALL DONE $(date -u +%H:%M)"
