# CI fixes — 2026-09-14

## Confirmed causes

- The current secret-scan failure is a false positive in historical architecture prose: `token counts; monetary estimates that may be unavailable`. Exemptions are limited to exact commit/path/rule/line fingerprints: the original line and an identical quote inadvertently introduced in the first fix commit’s explanatory comment. The comment is now paraphrased. The two pre-existing exemptions are preserved.
- Re-scanning reachable history with Gitleaks 8.30.1 passes. A throwaway repository with the same documentation in a new commit still fails the detector, confirming the exemption does not apply to future commits.
- Once secret scanning passed, the subsequent Go vulnerability scan revealed reachable Go standard-library and SSH dependency findings. Go 1.26.8 and x/crypto 0.56.0 pass govulncheck with zero reachable vulnerabilities. The patched SSH module requires Go 1.26, so Go 1.25.13 alone cannot satisfy the update. The Docker builder uses the same pinned version.
- The scheduled Integration workflow starts its own Compose stack but calls `https://jsonplaceholder.typicode.com/posts`. It is disabled in GitHub (`disabled_manually`) under the user's instruction. The branch change removes its schedule and retains a manual trigger; leave disabled until that change is merged, then enable only for deliberate manual runs.
- The browser regression supplies synthetic API responses; admission/control CI starts its own PostgreSQL; Temporal sandbox starts its own PostgreSQL and Temporal. These are self-contained and remain enabled.

## Checks already completed

- Edited workflows pass actionlint 1.7.7.
- npm production audit passes the existing high-severity threshold (two moderate React Router advisories remain below that gate).
- Full community and enterprise race suites, vet, and service builds pass. Logs are included here.
- `git diff --check` passes.

Nothing is merged or deployed by this task.

## Final GitHub result

All 17 latest workflows across PR #49 and PRs #38–40/#42–48 succeeded on the exact heads in `branch-heads.json`. See `latest-workflows.json` and `github-checks.json`. Superseded or duplicate runs were cancelled to release runner capacity; the latest runs all succeeded. Push-only image signing is skipped on PRs as expected. Integration remains disabled in GitHub; `integration-workflow-state.json` records that state.
