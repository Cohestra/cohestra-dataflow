#!/bin/bash
BAKE=${BAKE:?set BAKE to the bake-off work dir (scripts/, runs/, models/)}
# Golden preflight: fake Ollama returns golden outputs; real ee handlers validate them.
python3 $BAKE/scripts/golden_v2.py 11500 & F=$!
rm -f $BAKE/golden-ollama.jsonl
(cd /home/user/cohestra-dataflow/apps/workflow-go/internal/api && EVAL_PORT=14001 EVAL_DB=cohestra_eval2 OLLAMA_URL=http://127.0.0.1:11500 OLLAMA_MODEL=golden EVAL_OLLAMA_LOG=$BAKE/golden-ollama.jsonl exec $BAKE/scripts/h2/ai-harness-ee.test -test.run='^TestEvalHTTPHarness$' -test.timeout=0 > $BAKE/golden-server.log 2>&1) & H=$!
until curl -s 127.0.0.1:14001/health >/dev/null; do sleep 0.3; done
cd /home/user/cohestra-dataflow && AI_EVAL_MODEL=golden python3 tests/ai-evals/run.py --base-url http://127.0.0.1:14001 --cases tests/ai-evals/cases/v2-datalake.json --output $BAKE/golden-v2.json --strict >/dev/null 2>&1; echo "strict exit $?"
kill $H $F
