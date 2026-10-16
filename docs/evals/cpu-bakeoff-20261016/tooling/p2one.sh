#!/bin/bash
# p2one.sh <model> <base|fewshot>: one phase-2 arm on v2-datalake (ee harness, num_ctx 8192)
cd "$(dirname "$0")/.."
name=$1 arm=$2 L=${1//[:\/]/_}
[ -f "runs/v2-$L-$arm/report.json" ] && { echo "already done"; exit 0; }
rm -rf "runs/v2-$L-$arm"
if ! docker exec ollama ollama show "$name" >/dev/null 2>&1; then
  for m in $(docker exec ollama ollama list | awk 'NR>1{print $1}'); do docker exec ollama ollama rm "$m" >/dev/null; done
  IFS='|' read -r _ repo tag <<<"$(grep "^$name|" scripts/models.txt)"; f=models/$L.gguf
  scripts/fetch.sh "$repo" "$tag" "$f" && printf 'FROM /models/%s\n' "${f#models/}" > "$f.Modelfile" \
    && docker exec ollama ollama create "$name" -f "/models/${f#models/}.Modelfile" >/dev/null 2>&1; rm -f "$f"
fi
extra=(--env OLLAMA_NUM_CTX=8192); [ "$arm" = fewshot ] && extra+=(--env AI_FEW_SHOT=true)
python3 scripts/run_model2.py "$name" --label "v2-$L-$arm" --cases /home/user/cohestra-dataflow/tests/ai-evals/cases/v2-datalake.json \
  --harness "$PWD/scripts/h2/ai-harness-ee.test" --port 14001 --db cohestra_eval2 "${extra[@]}" </dev/null 2>&1 | grep -E "Error|Traceback"
python3 -c "import json;s=json.load(open('runs/v2-$L-$arm/report.json'))['summary'];print('$name $arm passed',s['passed'],'/',s['cases'])"
