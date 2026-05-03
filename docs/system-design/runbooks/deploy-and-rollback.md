# Deploy and rollback

## Normal deploy

`git push origin main` → `deploy.yml` runs CI, then `flyctl deploy --remote-only --strategy rolling`, then smoke-tests `/healthz`.

## Manual deploy

```sh
flyctl deploy --remote-only --strategy rolling
```

## Rollback

```sh
flyctl releases -a golang-build              # find the prior version
flyctl deploy -a golang-build --image <ref>  # redeploy that image
```

Rollbacks do NOT revert database migrations. If a migration is the problem, write a corrective migration forward.

## Preview environments

Comment `preview` (or `preview 1h`) on a PR. One preview at a time across the repo; comment again to refresh. Auto-tears-down after the duration.
