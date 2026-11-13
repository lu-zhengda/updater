# Updater

pstack is the development loop for this repo. Trust comes from the published
app installing and updating on a real Mac. A green release job is not that
proof. v0.24.1 got a tag and no GitHub Release and no DMG.

dyxhs-downloader is adopting the same pstack `AGENTS.md` shape. That copy has
not landed yet. Keep this file small so the two can be aligned later.

## Standing rules

- Every change is a pull request. Never push to `main`.
- No force-push, no history rewrite, and no moving or deleting a published tag.
- Never read, print, or export signing or notarization secrets or certificates.
  If a secret is needed, stop and report what is needed and where it lives.
- Do not sign, notarize, tag, or publish from a developer Mac. GitHub-hosted
  macOS runners do that.
- Release automation stays automatic. Do not add a human approval gate, a
  GitHub Environment reviewer, or a manual step to the release workflows.

## Flow

1. Design the change before editing across a boundary.
2. Build it.
3. Adversarial review with the pstack `interrogate` skill. Keep only findings a run proves.
4. Blast-radius check with the pstack `blast-radius` skill. Prove the one fact the change depends on by running code.
5. CI must pass. The required check is `.github/workflows/ci.yml`.
6. Ship in a pull request. Record what was run and the result in the PR body.
7. Publish only when that PR has exactly one of `semver:patch`, `semver:minor`, or `semver:major`. Zero semver labels publishes nothing. More than one fails the release workflow and does not guess.
8. After publish, on a real Mac, run `.cursor/skills/verify-updater/` against the version that just published. Paste the PASS/FAIL transcript into a comment on the merged PR.

Docs-only and workflow-only pull requests stay unlabeled. This verification skill does not publish a release.

## When verify-updater fails

The published tag stays where it is. Do not move it, delete it, or force-push it.

If the tag exists and the publish job failed before a Release existed, re-run that failed job. It checks out the existing tag and does not move it. v0.24.1 is the case where a tag was not enough.

If a Release exists and `verify-updater` fails, the artifact is wrong. Open a fix pull request and publish it as a new patch (`semver:patch`). Do not replace the bad tag. Run `verify-updater` on the new release and record that transcript too.

## Proof command

```sh
sh .cursor/skills/verify-updater/scripts/verify-updater.sh
sh .cursor/skills/verify-updater/scripts/verify-updater.sh 0.24.2
```

The script checks the real GitHub Release, the quarantined DMG, Homebrew, and in-app update. It exits non-zero on any FAIL. Do not weaken a check to make a broken release pass.

Past release failures and the check that now fails them are listed in [`docs/lessons.md`](docs/lessons.md).
