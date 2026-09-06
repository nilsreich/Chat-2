# Noten

Private, single-teacher, tablet-first gradebook for German 0–15 point grades. It provides class, student and assessment CRUD, bulk student entry, weighted written/oral averages, fast class-wide grade entry, student details, absence handling, and CSV export.

Important mutations are written atomically with a durable audit entry. Recent changes offer conflict-safe undo, grade history can restore an older value without removing later history, and classes, students, and assessments are soft-deleted into a small recovery view. Restoring students or assessments preserves all existing grades.

## Architecture and development

One Go 1.25.1 binary uses `net/http`, parsed-once `html/template` pages, a small Fetch-based vanilla JS/CSS frontend, `modernc.org/sqlite`, and persistent SCS sessions. SQLite migrations and web assets have one authoritative source tree and are embedded by the root asset package. There is no Node production runtime or remote asset.

PWA artwork is committed as reviewable SVG source (including a maskable variant), so pull requests contain no binary assets.

```sh
go mod download
make run                 # http://localhost:8080; development password: noten
make check
make build
```

Set `DEV_PASSWORD` to replace the local password. Production requires `APP_ENV=production` and the Fly secret `ADMIN_PASSWORD`; there is no development fallback in production. Other variables are `PORT`, `DATABASE_PATH`, and `TZ`; see `.env.example`.

Explicit SQL lives in `db/queries`; numbered migrations live in `db/migrations`. Startup enables WAL, foreign keys, a 5-second busy timeout and normal synchronous mode, and applies each migration transactionally once. The service worker caches only an explicit public static allowlist. Private HTML, mutations, CSV, and login data are network-only and `no-store`.

## Docker, Fly, and CI

Build with `docker build -t noten .`. The checked-in Fly app name is `noten-gradebook`; do not rename an existing external app implicitly. For first deployment, run `fly apps create noten-gradebook`, `fly volumes create data --region fra --size 1 -a noten-gradebook`, set the login password with `fly secrets set ADMIN_PASSWORD='DEIN_PASSWORT' -a noten-gradebook`, deploy with `fly deploy --remote-only --ha=false`, and enforce `fly scale count 1 -a noten-gradebook`. Change the password with `fly secrets set ADMIN_PASSWORD='NEUES_PASSWORT' -a noten-gradebook`.

The scratch image is CGO-free. Fly configuration specifies `fra`, one `shared-cpu-1x` Machine at 256 MB, one `/data` volume, HTTPS, actual stop-on-idle, auto-start, and zero minimum running Machines. Verify via `fly machine list` and `fly volumes list`. Pull requests run checks/build; main deploys after checks with an app-scoped `FLY_API_TOKEN` GitHub secret and HA disabled.

Authentication protects private routes. Session cookies are HttpOnly, Secure in production and SameSite=Lax; sessions persist in SQLite. Same-origin mutations, CSP, SQL constraints, and conservative cache headers protect student data. The management panel includes an uncached CSV export. Use a Fly Volume snapshot or a SQLite-aware backup while writes are quiescent for full database backup.

### Full SQLite backup and restore

CSV is an export, not a complete backup. For a consistent local backup, stop writes and run `sqlite3 /path/to/noten.db "VACUUM INTO '/safe/noten-backup.db'"`; retain the resulting file outside the app volume. On Fly, use a Volume snapshot or stop the single Machine and copy a SQLite-created backup. To restore, stop the app, retain the current database as a rollback copy, place the verified backup at the configured `DATABASE_PATH`, preserve file permissions, and start the single Machine. Never copy only the main database file while WAL writes are active.
