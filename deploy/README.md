# Homelab deployment runbook

Deploys the karakeep-random-rss service to `percival` (Tailscale IP
`100.119.145.113`), managed there by dockhand from
[`deploy/percival/docker-compose.yml`](percival/docker-compose.yml). The service
joins the `karakeep_default` network (to reach Karakeep's container
`karakeep-web-1:3000`) and a dedicated `feeds` network, whose only purpose is
letting Miniflux resolve and fetch this feed by container name — Miniflux
itself sits on the default `bridge` network, which has no automatic
container-name DNS, so a user-defined network is required. This service has no
database and needs no other shared network. No port is published to the host.

## One-time setup

1. **Create the `feeds` network on percival** (it does not exist yet):

   ```bash
   ssh -t percival 'sudo docker network create feeds'
   ```

   `sudo` on percival prompts for a password, so run this from a real
   terminal (the `-t` flag above allocates one over SSH).

2. **Attach Miniflux to `feeds`.** Both of the following are needed:
   - **Persistent:** add `feeds` as an external network to the Miniflux
     stack's compose file in dockhand and redeploy Miniflux. Without this,
     the attachment below is lost the next time Miniflux's container is
     recreated and the feed silently starts failing to refresh.
   - **Immediate, no-restart stopgap** if you don't want to redeploy Miniflux
     right away:

     ```bash
     ssh -t percival 'sudo docker network connect feeds miniflux'
     ```

     This takes effect immediately but does **not** survive container
     recreation — it buys time, it does not replace the compose change above.

3. **Verify both containers are on `feeds`** before deploying this stack:

   ```bash
   ssh -t percival 'sudo docker network inspect feeds --format "{{range .Containers}}{{.Name}} {{end}}"'
   ```

   Expect to see `miniflux` now, and `karakeep-random-rss` once this stack is
   deployed.

4. **Create a Karakeep API key and store it as a dockhand stack variable.**
   In Karakeep, go to Settings → API Keys and create one. Then set
   `KARAKEEP_API_KEY` as a dockhand stack variable for this stack (it will be
   encrypted in dockhand's database, never committed to the repo).

5. **Ensure percival can pull from GHCR.** The image must be public, or
   percival must be authenticated to GHCR with `docker login ghcr.io` using a
   `read:packages` PAT.

6. **Create the git stack in dockhand.** Dockhand itself runs on percival in the
   `dockhand` container (port 3000). Register the stack with:
   - Repository: this repo. percival also runs a soft-serve git server (ports
     9418, 23231–23233; ssh alias `soft` → `percival:23231`) as a possible git
     remote alongside GitHub — either works as the dockhand repository source.
   - Stack name: `karakeep-random-rss` (exactly — must match the compose file's
     `name:` pin so the state volume lines up across redeploys)
   - Compose path: `deploy/percival/docker-compose.yml`
   - Env file path: `deploy/percival/karakeep-random-rss.env`
   - Auto-update: on
   - Auto-update cron: `*/15 * * * *`

## Subscribing in Miniflux

Add the feed `http://karakeep-random-rss:8080/feed.xml` to Miniflux at
`rss.c18l.com`. Both containers sit on the `feeds` network (see one-time setup
above), which resolves the hostname and makes the feed reachable.

Note that Miniflux's feed refresh interval setting is independent of this
service's `INTERVAL` env var. This service publishes exactly one new item per
`INTERVAL`, regardless of how often Miniflux polls it. Set Miniflux's refresh
interval to something comfortably shorter than `INTERVAL` (an hour is fine for a
daily feed) so it doesn't miss the window.

## Releasing

```bash
git tag vX.Y.Z
git push --tags
```

That's the whole release: it triggers `.github/workflows/release.yml`. CI runs
`go test ./...` as a gate, builds and pushes the image to GHCR, then commits
the `KARAKEEP_RANDOM_RSS_VERSION` bump to `deploy/percival/karakeep-random-rss.env`
on `main` as `github-actions[bot]`. dockhand polls the git stack on its cron
(`*/15 * * * *`) and redeploys within ~15 minutes once it sees the bump commit
— no manual dockhand step needed.

Manual bump (if needed):

```bash
./scripts/bump-deployed-version.sh vX.Y.Z
git commit -am "chore(deploy): release vX.Y.Z"
git push
```

**Deferred improvement — push webhook.** The stack currently syncs on the
`*/15 * * * *` cron only, so a release lands up to 15 minutes after CI
commits the version bump. Dockhand's "Enable webhook" option (off today)
takes push events from GitHub and syncs immediately, which would make
releases near-instant. Turning it on means enabling it in the stack's
settings and adding the resulting URL as a webhook in the GitHub repo's
settings — note dockhand must be reachable from GitHub for that, which the
cron flow does not require, so this is a real tradeoff and not a pure win.

**Gotcha:** dockhand's variable precedence is repo `.env` file → dockhand stack
variables → deploy-time env (later wins). Never set `KARAKEEP_RANDOM_RSS_VERSION`
as a dockhand stack variable, or it will silently override every future version
bump committed to the repo.

## Verifying

Since no port is published, checks must run from inside the shared network (via
SSH to percival):

Check the logs:

```bash
ssh -t percival 'sudo docker logs karakeep-random-rss'
```

Check the healthz and feed endpoints from inside a container that shares the
`feeds` network (e.g. the Miniflux container):

```bash
ssh -t percival 'sudo docker exec miniflux wget -qO- http://karakeep-random-rss:8080/healthz'
ssh -t percival 'sudo docker exec miniflux wget -qO- http://karakeep-random-rss:8080/feed.xml'
```

The Miniflux image ships `/usr/bin/wget` and a shell, but **no** `curl` —
verified against `miniflux/miniflux:latest`, so use `wget` as above.

Note: `sudo` on percival requires an interactive password (passwordless sudo is
not configured), so these commands must run interactively. The `-t` flag forces
pseudo-terminal allocation even when running over SSH, which enables password
prompts. These cannot be automated in non-interactive scripts.

**Troubleshooting:** if Miniflux reports a connection or DNS error fetching
the feed URL, the first thing to check is whether Miniflux is still attached
to `feeds` (see the verification command in one-time setup step 3 above). A
Miniflux redeploy that dropped the network — because the persistent compose
change from step 2 wasn't made, so only the stopgap `docker network connect`
was ever applied — is the most likely cause.

## First-boot behavior

The first link publishes immediately on a cold start, so there is something in
the feed right after the stack comes up. No need to wait a full `INTERVAL` to
confirm the service is working.
