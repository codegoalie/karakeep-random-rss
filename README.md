# karakeep-random-rss

A single Go binary that resurfaces your Karakeep bookmarks. On a schedule you
choose it picks a random link you have saved, appends it to an RSS feed, and
serves that feed over HTTP. Point Miniflux at it and your backlog starts
arriving one link at a time.

No dependencies outside the standard library.

## How it behaves

- **One item per interval.** The scheduler wakes on `INTERVAL` (default `24h`)
  and publishes exactly one link. The next due time is stored in the state
  file, so restarting the service doesn't reset the clock or skip a day.
- **No repeats until the library is exhausted.** Every published bookmark ID
  goes into a seen-set. Only when every eligible link has had a turn does the
  cycle counter tick over and the set clear — and the most recently published
  links are held back briefly so the feed doesn't stutter at the boundary.
- **A resurfaced link reads as new.** GUIDs are `urn:karakeep:<id>:cycle:<n>`,
  so when a link comes back around in a later cycle Miniflux treats it as a
  fresh unread item rather than deduplicating it against the old one.
- **Pool:** non-archived bookmarks whose content type is `link`. Optionally
  narrowed to a single list (`LIST`) or tag (`TAG`).
- **Failure is not a skipped day.** The next-publish timestamp only advances on
  a successful publish, so a Karakeep outage means a retry, not a gap.

## Build and run

```sh
go build -o karakeep-random-rss .

export KARAKEEP_URL=https://karakeep.example.com
export KARAKEEP_API_KEY=ak1_...        # Karakeep → Settings → API Keys
export INTERVAL=24h
./karakeep-random-rss
```

The first link publishes immediately on a cold start, so you have something to
subscribe to right away.

## Endpoints

| Path | What it does |
| --- | --- |
| `GET /feed.xml` | The RSS 2.0 feed. `/rss` is an alias. Supports `If-None-Match`. |
| `GET /healthz` | Plain-text status: item count, cycle, seen-this-cycle, next publish time. Returns 503 if the last Karakeep sync failed. |
| `POST /publish` | Publishes a link immediately, outside the schedule. Disabled unless `ADMIN_TOKEN` is set; requires `Authorization: Bearer $ADMIN_TOKEN`. |

## Configuration

Every flag has a matching environment variable; flags win.

| Env | Flag | Default | Notes |
| --- | --- | --- | --- |
| `KARAKEEP_URL` | `-karakeep-url` | — | **Required.** Base URL, no trailing path. |
| `KARAKEEP_API_KEY` | `-api-key` | — | **Required.** Or `KARAKEEP_API_KEY_FILE` for a secrets file. |
| `INTERVAL` | `-interval` | `24h` | Any Go duration ≥ 1m. `8h` for three a day, `168h` for weekly. |
| `LISTEN` | `-listen` | `:8080` | |
| `PUBLIC_URL` | `-public-url` | — | The URL Miniflux fetches; emitted as `<atom:link rel="self">`. |
| `STATE_FILE` | `-state-file` | `state.json` | Must persist. Written atomically. |
| `FEED_ITEMS` | `-feed-items` | `50` | How many past items stay in the feed. |
| `FEED_TITLE` | `-feed-title` | `Karakeep: a random link` | |
| `ITEM_TITLE_PREFIX` | `-item-title-prefix` | `🔖 From the stacks: ` | Prepended to each item's `<title>` at render time (stored titles stay unprefixed). Pass `-item-title-prefix=""` to disable. |
| `FEED_DESCRIPTION` | `-feed-description` | … | |
| `LIST` | `-list` | — | Restrict to one Karakeep list, by name. |
| `TAG` | `-tag` | — | Restrict to bookmarks carrying this tag. |
| `ADMIN_TOKEN` | `-admin-token` | — | Enables `POST /publish`. |
| `HTTP_TIMEOUT` | `-http-timeout` | `30s` | Per-request timeout to Karakeep. |
| `MAX_PAGES` | `-max-pages` | `200` | Pagination safety valve (100 bookmarks per page). |

## Docker

```sh
cp .env.example .env   # fill in KARAKEEP_API_KEY
docker compose up -d --build
```

State lives on the `karakeep-random-rss-state` volume — keep it, or the
seen-set resets and you'll start seeing repeats.

If Miniflux runs in another compose project, join its network instead of
publishing a port and subscribe to
`http://karakeep-random-rss:8080/feed.xml`.

### Dockhand (percival)

This service can also run as a git-backed dockhand stack on the homelab host
`percival`, alongside existing Karakeep and Miniflux containers. It joins their
docker networks instead of publishing a port, so Miniflux reaches it directly.
See [`deploy/README.md`](deploy/README.md) for the full deployment runbook.

## systemd

```sh
sudo install -m0755 karakeep-random-rss /usr/local/bin/
sudo install -m0600 /dev/stdin /etc/karakeep-random-rss.env <<'EOF'
KARAKEEP_URL=https://karakeep.example.com
KARAKEEP_API_KEY=ak1_...
INTERVAL=24h
PUBLIC_URL=https://random.example.com/feed.xml
EOF
sudo install -m0644 karakeep-random-rss.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now karakeep-random-rss
```

The unit uses `DynamicUser` and `StateDirectory`, so state lands in
`/var/lib/karakeep-random-rss/` with no user to create.

## Subscribing in Miniflux

Add `http://<host>:8080/feed.xml` as a feed. Two settings worth adjusting:

- **Refresh interval:** the feed advertises `ttl` equal to your `INTERVAL`, but
  Miniflux ignores `ttl`. Set the feed's own refresh interval to something
  comfortably shorter than `INTERVAL` (an hour is fine for a daily feed).
- **Fetch original content** is worth enabling if you want the full article
  rather than the description Karakeep captured.

If the feed is only reachable on your LAN, remember Miniflux has to reach it
from wherever *it* runs — container network, not your laptop.

## Tests

```sh
go test -race ./...
```

The suite runs the publisher end to end against a fake Karakeep server:
pagination, archived/non-link filtering, the no-repeat guarantee across a full
cycle, cycle rollover, feed well-formedness and conditional GETs.
