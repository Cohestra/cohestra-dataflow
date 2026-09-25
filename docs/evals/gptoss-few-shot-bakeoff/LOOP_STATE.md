# GPT-OSS few-shot bake-off

- Goal: Compare base prompting, category-matched few-shot prompting, and the trained QLoRA adapter on the held-out 14-case suite.
- Done when: Each runnable arm has one retained warm report with the same API build, model settings, fixtures, and evaluator; any unrunnable arm has a reproduced incompatibility.
- Budget: One deployment and one retained warm run per arm; no sub-agents.
- Status: complete
- Current item: Three-arm bake-off completed.
- Evidence: Retained reports are `base.json`, `few-shot.json`, and `trained.json`. Pass rates: base 28.57%, runtime few-shot 50.00%, trained QLoRA 28.57%. The final adapter SHA-256 is `5b950cec51fc17bd1a266bd96c4271f910cf7f42dbc0f38220654fd779e2ff8d`.
- Attempts: One retained warm run per arm; two failed adapter-export approaches preceded the successful native MXFP4 export.
- Next action: Promote category-matched runtime few-shot prompting; expand and rebalance training data before another QLoRA attempt.
- Last run: 2026-08-12
