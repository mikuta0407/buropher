# Performance

This document describes how buropher's performance is measured at a realistic scale, the
results before and after the tuning work of M3-08 / M7-06, and the remaining hot spots.
All optimizations keep the rendered output identical (verified with `go test ./...` and the
compatibility harness, see [Verification](#verification)).

## Data set

`tools/gendata` fills an empty database (it runs the migrations and the equivalent of
`buropher init` itself) with deterministic pseudo-random data:

| Object | Rows |
|---|---:|
| Projects (40 roots, nested up to depth 3; 80 % public) | 200 |
| Users / groups / memberships (incl. group-inherited roles) | 500 / 50 / 10,700 |
| Issues (25 % in parent/child trees, Zipf-like distribution over projects) | 100,000 |
| Issue relations / watchers | 20,000 / 200,000 |
| Issue custom fields / custom values | 10 / 600,000 |
| Journals / journal details | 1,003,000 / 1,404,000 |
| Time entries | 200,000 |
| Wiki pages / page versions | 20,000 / 60,000 |
| Attachments (rows only, no files) | 50,000 |
| News / comments / forum messages | 2,000 / 4,000 / 15,800 |

`proj-001` is the largest project (about 13,000 issues, 20,000 including subprojects). Issue
`#50001` has 300 journals. The administrator is `admin` / `perfadmin1`; `user0001` /
`perfpass1` is a non-admin "member" with the Developer role in about 70 projects. Both have
API keys (see `tools/gendata`), the REST API is enabled and `issues_export_limit` is 10,000.

```sh
go run ./tools/gendata -driver sqlite -dsn path/to/perf.db              # ~4 min, 830 MB
go run ./tools/gendata -driver postgres -dsn 'postgres://...'          # ~3.5 min
```

`-scale 0.02` makes a small data set for quick checks; `-seed` changes the random data.

## Benchmark

`tools/perf` logs in as each user (form login for HTML, API key for `.json`), then requests
each endpoint sequentially (`-n` measured requests after `-warmup` requests, stopping after
`-budget` per endpoint) and prints p50 / p95 latencies of the complete response:

```sh
buropher migrate -config perf.toml && buropher serve -config perf.toml &
go run ./tools/perf -base http://127.0.0.1:3000 -n 10 -warmup 1 -json result.json
```

Every endpoint is measured with a fresh session, because issue queries are stored in the
session as in Redmine (otherwise e.g. the Gantt chart would inherit the filters and columns of
the previously measured issue list). `-only gantt,roadmap` restricts the endpoints, `-c`
sends requests concurrently.

## Profiling

Set `server.pprof = true` (and optionally `server.pprof_addr`, default `127.0.0.1:6060`; only
loopback addresses are accepted) to start a second listener with:

- `/debug/pprof/...` — Go's `net/http/pprof` (`go tool pprof http://127.0.0.1:6060/debug/pprof/profile?seconds=10`).
- `/debug/sqlstats` — SQL statistics: every statement is normalized (numbers and `IN` lists
  collapsed) and counted with total / max duration, sorted by total time. Recording is off by
  default: `?enable=1` starts it, `?reset=1` clears it, `?limit=20` limits the output,
  `?full=1` prints statements without truncation, `?enable=0` stops it. Many calls of the
  same statement point at an N+1 query; a single slow statement at a missing index or a bad
  plan.

## Results

Hardware: Intel N100 (4 cores, 4 threads), 16 GB RAM, NVMe SSD, Linux 7.0, Go 1.26.1,
PostgreSQL 17 in Docker on the same machine (`shared_buffers=512MB`,
`synchronous_commit=off`). The machine was shared with other build and test jobs, so
individual numbers vary by ±20 %; differences of 2× and more are significant.

Times are milliseconds; each cell is `p50 / p95` of 10 requests (slow endpoints stop after
60 s with at least 3 samples). "member" is `user0001`. "before" is the commit before this
work (`fb09673`), "after" is this work with migration `00002` applied. Each run starts the
server on a fresh copy of the generated SQLite database (the "after" server analyzes it in
the background within the first seconds, see [Changes](#changes)); the PostgreSQL database
was analyzed after loading. The runs in the tables were made back to back at a load average
of 1–3.

### SQLite

| Endpoint | admin before | admin after | member before | member after | member speed-up (p50) |
|---|---:|---:|---:|---:|---:|
| projects | 28 / 38 | 28 / 35 | 30 / 38 | 29 / 37 | 1.0× |
| project overview | 343 / 378 | 115 / 135 | 286 / 314 | 132 / 146 | 2.2× |
| issues (default) | 117 / 127 | 21 / 28 | 142 / 163 | 35 / 40 | 4.0× |
| issues (all projects) | 553 / 591 | 232 / 246 | 658 / 713 | 297 / 319 | 2.2× |
| issues filter | 108 / 114 | 44 / 49 | 126 / 141 | 59 / 61 | 2.2× |
| issues sort priority | 124 / 130 | 96 / 111 | 146 / 173 | 139 / 141 | 1.1× |
| issues sort assignee | 129 / 150 | 100 / 104 | 152 / 183 | 141 / 154 | 1.1× |
| issues sort cf | 233 / 251 | 142 / 145 | 259 / 285 | 160 / 175 | 1.6× |
| issues group status | 153 / 161 | 60 / 65 | 198 / 211 | 79 / 86 | 2.5× |
| issues group assignee+totals | 555 / 584 | 379 / 397 | 614 / 653 | 618 / 651 | 1.0× |
| issues many columns | 4728 / 4773 | 53 / 58 | 4874 / 4925 | 71 / 82 | 68.9× |
| issue show (many journals) | 235 / 598 | 186 / 203 | 565 / 1203 | 159 / 165 | 3.6× |
| gantt | 1305 / 1987 | 319 / 358 | 3524 / 5044 | 332 / 346 | 10.6× |
| calendar | 248 / 285 | 90 / 92 | 192 / 313 | 95 / 113 | 2.0× |
| activity | 1822 / 2242 | 599 / 611 | 1174 / 2047 | 577 / 596 | 2.0× |
| activity (all) | 5177 / 5909 | 2049 / 2077 | 4909 / 4963 | 1819 / 1876 | 2.7× |
| search | 338 / 377 | 167 / 171 | 400 / 450 | 74 / 85 | 5.4× |
| search (project) | 511 / 532 | 489 / 513 | 531 / 551 | 501 / 514 | 1.1× |
| time entries | 3354 / 5299 | 1120 / 1143 | 3338 / 4584 | 1005 / 1033 | 3.3× |
| time entries (project) | 663 / 806 | 248 / 262 | 810 / 871 | 246 / 267 | 3.3× |
| time report | 1815 / 2060 | 418 / 443 | 1811 / 1885 | 424 / 447 | 4.3× |
| my page | 8 / 12 | 8 / 9 | 41 / 45 | 29 / 43 | 1.4× |
| issues.json limit=100 | 688 / 711 | 278 / 283 | 764 / 851 | 351 / 365 | 2.2× |
| project issues.json limit=100 | 183 / 189 | 76 / 85 | 230 / 237 | 98 / 103 | 2.4× |
| wiki page | 11 / 13 | 10 / 12 | 15 / 21 | 15 / 16 | 1.0× |
| wiki index | 55 / 58 | 43 / 63 | 60 / 74 | 48 / 59 | 1.2× |
| roadmap | 2064 / 2299 | 561 / 603 | 3867 / 4201 | 566 / 587 | 6.8× |
| issues CSV (10k) | 271 / 278 | 229 / 246 | 283 / 310 | 238 / 247 | 1.2× |

### PostgreSQL

| Endpoint | admin before | admin after | member before | member after | member speed-up (p50) |
|---|---:|---:|---:|---:|---:|
| projects | 29 / 38 | 32 / 39 | 39 / 48 | 38 / 51 | 1.0× |
| project overview | 87 / 92 | 70 / 82 | 94 / 122 | 71 / 73 | 1.3× |
| issues (default) | 54 / 75 | 45 / 50 | 64 / 72 | 50 / 56 | 1.3× |
| issues (all projects) | 82 / 87 | 90 / 94 | 109 / 121 | 119 / 131 | 0.9× |
| issues filter | 128 / 141 | 127 / 158 | 139 / 153 | 140 / 162 | 1.0× |
| issues sort priority | 61 / 66 | 51 / 55 | 67 / 79 | 53 / 64 | 1.3× |
| issues sort assignee | 58 / 62 | 49 / 58 | 68 / 76 | 54 / 66 | 1.2× |
| issues sort cf | 199 / 208 | 197 / 221 | 219 / 224 | 208 / 227 | 1.0× |
| issues group status | 71 / 91 | 52 / 55 | 82 / 103 | 61 / 68 | 1.4× |
| issues group assignee+totals | 220 / 226 | 204 / 208 | 233 / 243 | 210 / 228 | 1.1× |
| issues many columns | 101 / 111 | 92 / 98 | 117 / 125 | 105 / 130 | 1.1× |
| issue show (many journals) | 392 / 412 | 321 / 352 | 595 / 655 | 283 / 288 | 2.1× |
| gantt | 1082 / 1122 | 433 / 449 | 1027 / 1057 | 412 / 423 | 2.5× |
| calendar | 74 / 79 | 71 / 78 | 76 / 109 | 74 / 87 | 1.0× |
| activity | 606 / 637 | 526 / 818 | 562 / 602 | 534 / 909 | 1.1× |
| activity (all) | 2553 / 2600 | 1809 / 2028 | 2301 / 2372 | 1645 / 2021 | 1.4× |
| search | 127 / 139 | 132 / 138 | 136 / 140 | 141 / 151 | 1.0× |
| search (project) | 1538 / 1569 | 1535 / 1584 | 1409 / 1444 | 1408 / 1463 | 1.0× |
| time entries | 177 / 190 | 177 / 190 | 191 / 204 | 188 / 202 | 1.0× |
| time entries (project) | 118 / 125 | 115 / 120 | 113 / 121 | 112 / 122 | 1.0× |
| time report | 1363 / 1409 | 224 / 257 | 1290 / 1344 | 220 / 240 | 5.9× |
| my page | 12 / 16 | 11 / 14 | 43 / 52 | 45 / 52 | 1.0× |
| issues.json limit=100 | 219 / 263 | 200 / 230 | 263 / 298 | 241 / 266 | 1.1× |
| project issues.json limit=100 | 186 / 219 | 149 / 203 | 196 / 305 | 165 / 196 | 1.2× |
| wiki page | 18 / 31 | 16 / 19 | 24 / 30 | 24 / 41 | 1.0× |
| wiki index | 51 / 59 | 50 / 58 | 56 / 61 | 56 / 65 | 1.0× |
| roadmap | 3671 / 3722 | 490 / 516 | 4126 / 4244 | 506 / 548 | 8.2× |
| issues CSV (10k) | 240 / 278 | 239 / 257 | 258 / 305 | 239 / 295 | 1.1× |

The very first baseline run, made while the machine was heavily loaded (load average ~17),
shows the pathological cases more drastically: on SQLite "issues many columns" took 15 s
(p50; one query for the relations column took 13 s because SQLite, without statistics,
scanned every issue), "time entries" 4–6 s and "roadmap" 2.6 s (admin) / 8.2 s (member, 9,600
queries for the subtree estimated hours of parent issues).

## Changes

Database / SQL:

- **Planner statistics for SQLite.** SQLite has no statistics unless `ANALYZE` is run, and
  without them it picked catastrophic plans on large tables (e.g. the relations of 100
  listed issues were found by scanning every issue: 13 s for one query). The server now
  runs `ANALYZE` (with `analysis_limit = 1000`, table by table so that writers are not
  blocked for long) in the background at startup for tables that have no statistics or an
  index without statistics, and for all tables every 6 hours. Afterwards it bumps the schema
  version (creating and dropping a scratch table in one transaction), because `ANALYZE` only
  reloads the statistics of its own connection and pooled connections would otherwise keep
  planning without them.
- **Connection settings for SQLite:** `temp_store = MEMORY`, `cache_size` 32 MiB and
  `mmap_size` 256 MiB per connection; WAL mode with `BEGIN IMMEDIATE` and a busy timeout as
  before (readers run in parallel, writers are serialized by SQLite's lock).
- **Connection pool:** up to `max(4, 2 × CPUs)` idle connections are kept (Go's default of
  2 re-opened connections under concurrent load, and each new SQLite connection has to parse
  the schema). The number of open connections stays unlimited by default because a request
  may read on a second connection while holding a transaction; `database.max_open_conns`
  sets a limit (e.g. below PostgreSQL's `max_connections`).
- **Index** (migration `00002_perf_indexes`): `issues (project_id, status_id, tracker_id,
  is_private, author_id, assigned_to_id)` replaces `issues (project_id)`. Issue counts,
  lists and per-tracker counts of a project tree with the default "open" filter, including
  the `Issue.visible_condition` of non-admin users, are answered from the index alone.
- **Gantt:** sorting by project tree order and the `gantt_items_limit` are applied in SQL
  instead of loading every matching issue (with all its preloads) and truncating in Go.
- **Time entry counts and sums** omit the `LEFT OUTER JOIN`s to activities and issues when
  neither the filters nor the aggregate refers to them (joins on a primary key never change
  the number of rows).
- **Activity:** journals are selected with `EXISTS (status change detail) OR notes <> ''`
  instead of `LEFT JOIN journal_details` + `DISTINCT` over the note texts.

N+1 queries and algorithmic fixes:

- Issue page: journal details and reactions are loaded for all journals at once; the
  principal visibility condition (`Principal.visible`) is computed once per request
  (it was two `COUNT` queries per journal author link).
- Roadmap: fixed issues and visible fixed issues of all versions are read in one query, and
  the subtree estimated hours of parent issues in one grouped query per 500 issues
  (previously one query per version and per parent issue: ~10,000 queries).
- Issue lists for the API, context menus and bulk edit load the issues and their custom
  values in batches instead of two queries per row.
- Time report: rows are grouped per criterion value and summed per period in one pass
  instead of re-scanning all rows for every (value, period) cell.
- Activity: `format_activity_description` skips its regular expressions when the text cannot
  match (no quoted lines, no `<pre>`/`<code>`, no line breaks).

## Remaining hot spots

- **Activity over all projects** (~2 s; 30 days ≈ 67,000 journal events, a 25–30 MB page):
  the page itself is the problem; Redmine renders the same amount. Template execution
  (reflection in `text/template`) and the row lookups by `created_at` dominate.
- **Time entries over all projects on SQLite** (~1 s; 200,000 entries): `COUNT` and `SUM`
  over all visible entries and the ordered id query (`spent_on DESC, created_at DESC`) each
  read every row; SQLite does not use an index for this order because of the join
  conditions. PostgreSQL needs ~0.2 s.
- **Issue lists over all projects and grouped totals on SQLite** (0.2–0.6 s): counting
  60,000 open issues and summing `estimated_hours` / spent time per group still read every
  matching row.
- **Search in a large project** (0.5 s SQLite, 1.4 s PostgreSQL): `LIKE '%…%'` over subjects,
  descriptions, journals and custom values cannot use an index (same as Redmine without a
  full text plugin).
- **Roadmap with many shared versions** (~0.5 s): loading ~10,000 fixed issues of
  system-wide shared versions in one query and rendering them; a few per-version queries
  (assignees, custom fields, overview counts) remain.
- SQLite's pure-Go driver (modernc.org/sqlite) is roughly 2–4× slower than C SQLite for
  scan-heavy queries; PostgreSQL is faster for large aggregates.

## Verification

- `go test ./...`.
- The compatibility harness was run with two buropher candidates — the commit before this
  work and this work — over all scenarios including the write scenarios (`go run
  ./tools/compat diff -ref <before> -cand <after> -reset-ref … -reset-cand …`, in both
  directions): 2,162 cases identical, 0 differences. The same 12 cases report scenario
  expectation errors (status codes expected from Redmine) on both builds.

## Upgrading

Run `buropher migrate` after replacing the binary (as for every release): migration
`00002_perf_indexes` creates the new index (about a second for 100,000 issues on SQLite).
