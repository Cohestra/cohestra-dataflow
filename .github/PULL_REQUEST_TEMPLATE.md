## Summary

<!-- What does this PR do? One or two sentences. Link related issues: Closes #123 -->

## Changes

<!-- Bullet list of what changed and why -->

## Test plan

<!-- How did you verify this works? -->

- [ ] Unit tests pass (`npm test` / `go test ./...`)
- [ ] TypeScript compiles (`npx tsc --noEmit` in `apps/web`)
- [ ] Docker Compose stack starts (`docker compose up`)
- [ ] Manually tested the changed flow

## Checklist

- [ ] Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`, `docs:`, etc.)
- [ ] Added/updated tests for non-trivial logic
- [ ] No secrets or credentials in the diff
- [ ] `CHANGELOG.md` updated if this is a user-visible change

## DCO sign-off

By submitting this PR I certify that my contribution is made under the terms
of the [Developer Certificate of Origin](https://developercertificate.org/)
and the [GNU AGPL-3.0](../LICENSE) (or the [Elastic License 2.0](../apps/workflow-go/ee/LICENSE)
for changes under `apps/workflow-go/ee/`).

<!-- Add `Signed-off-by: Your Name <you@example.com>` to each commit, or check the box: -->
- [ ] I have signed off all commits in this PR (`git commit -s`)
