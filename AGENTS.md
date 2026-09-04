# AGENTS.md

# Mission

Build and maintain a complete, production-ready, private gradebook web application for a single teacher.

This repository is intended to be implemented and maintained autonomously by coding agents such as Codex Cloud. Treat this file as the authoritative product, architecture, security, deployment, and UX specification.

Do not stop at scaffolding or planning. Implement the application completely, verify it, commit generated code where required, and—when authenticated GitHub/Fly.io access is available—push and deploy it.

Do not ask the user about minor implementation choices. If something is unspecified, choose the simplest, safest, most maintainable solution consistent with this document.

The guiding principle is:

> Build a tiny, durable, tablet-first gradebook that feels like a purpose-built spreadsheet, not like school administration software.

---

# 1. Non-negotiable product scope

The application is for exactly one teacher.

The teacher must be able to fully manage:

- classes
- students
- assessments
- grades
- assessment weights
- class-level written/oral weighting

The application must support complete CRUD for the core objects.

## Classes

The teacher can:

- create a class
- rename/edit a class
- change its grading configuration
- manage its students
- view its assessments and grade matrix
- delete a class with an explicit destructive confirmation

Deleting a class must handle related students, assessments, and grades intentionally and safely.

No school/organization/tenant hierarchy is needed.

## Students

The teacher can:

- add one student
- bulk-add students by pasting one name per line
- rename/edit a student
- delete a student with an explicit confirmation
- view a student's grade detail

A student belongs to exactly one class in this application.

Do not over-engineer name parsing. For bulk paste, a reasonable split into first and last name is sufficient.

Example input:

```text
Anna Becker
Ben Müller
Clara Klein
David Schulz
```

## Assessments

There are exactly three assessment types:

- written exam / Klassenarbeit
- test
- oral grade / mündliche Note

Use stable internal identifiers such as:

- `written`
- `test`
- `oral`

The teacher can:

- create an assessment
- rename/edit it
- change its type
- change its date
- change its individual weight
- enter grades for it
- edit previously entered grades
- delete the assessment with explicit confirmation

Each assessment has at least:

- ID
- class ID
- name
- type
- date
- weight
- created timestamp
- updated timestamp

Reasonable defaults:

- Klassenarbeit: weight `2.0`
- Test: weight `1.0`
- Mündlich: weight `1.0`

Defaults are convenience only. Weight remains editable.

## Grades

Grades use the German upper-secondary 0–15 point scale.

Valid actual grade values are:

```text
0 through 15 inclusive
```

The teacher can:

- enter a grade
- change a grade
- clear/remove a grade
- mark a student absent for an assessment if absence support is implemented

Important semantic rules:

- `0` is a real grade.
- missing/unentered is not `0`.
- absent is not `0`.
- absent/missing values must not reduce averages.
- there may be at most one grade record for a given student + assessment pair.

Use a database uniqueness constraint and UPSERT where appropriate.

---

# 2. Grade calculation model

There are two calculation categories:

- written
- oral

Category membership:

- `written` assessment -> written category
- `test` assessment -> written category
- `oral` assessment -> oral category

Each assessment has its own positive weight.

Calculate the weighted average within each category.

Example:

```text
KA1 = 10, weight 2
Test1 = 12, weight 1

written average =
(10*2 + 12*1) / 3
= 10.666...
```

Each class also has configurable category weighting.

Default:

```text
written = 50%
oral = 50%
```

The teacher must be able to change this, for example:

```text
written = 60%
oral = 40%
```

The two class-level category percentages must remain logically valid and total 100%.

Display:

- written average
- oral average
- overall calculated average

Round only for display.

Use full precision internally.

Normally display one decimal place.

Examples:

```text
12.3
9.8
14.0
```

If only one category currently contains real grades, do not treat the empty category as zero. Show the available category and calculate/display the overall state sensibly rather than inventing a penalty.

The application does not assign a teacher-chosen report-card/final grade. It only shows calculated values.

Whenever a grade, assessment weight, assessment type, or class-level category weighting changes, calculated values must immediately reflect the new data.

---

# 3. Primary UX goal

The most important workflow is:

```text
Open class
→ create "Test 1"
→ immediately see all students
→ enter one 0–15 grade after another
→ automatically advance to the next student
→ return to matrix
→ averages are already updated
```

The application succeeds only if grading a class of roughly 28 students is extremely fast on a tablet.

Do not optimize primarily for dashboards, analytics, or administrative depth.

Optimize for repeated grade entry.

---

# 4. Target device and interaction model

Primary target:

- approximately 11-inch Android tablet
- landscape orientation
- touch input
- installable PWA

Desktop must also work well.

Phone support may be usable but is not the primary design target.

Touch targets must be comfortable.

Do not create a desktop-only tiny spreadsheet UI.

---

# 5. Main layout

Use a quiet two-column tablet layout.

Concept:

```text
┌────────────────┬────────────────────────────────────────────┐
│ Klassen        │ Mathematik 11a                  + Leistung │
│                │                                            │
│ 11a Mathematik │ Name       M1  T1  KA1  M2     S   M    Ø │
│ 11b Mathematik │ Anna       12  11   9   13   10.3 13 11.7 │
│ 10a Informatik │ Ben         9  10  11    8   10.0  8  9.0 │
│                │ Clara      14  15  13   14   13.7 14 13.9 │
│ + Klasse       │                                            │
└────────────────┴────────────────────────────────────────────┘
```

Left side:

- class list
- add class action

Right side:

- current class workspace

Do not create a dashboard home page.

When practical, open directly into the most recently used class after authentication.

---

# 6. Grade matrix

The grade matrix is the central screen.

Rows:

- students

Columns:

- assessments in a logical order
- written average
- oral average
- overall average

Requirements:

- student-name column remains sticky/fixed on the left
- header remains sticky where helpful
- assessment columns can horizontally scroll
- matrix remains touch-friendly
- assessment headers are compact
- changing viewport size must not break sticky behavior
- no huge cards around the table

Compact assessment labels may look like:

```text
KA1
T1
M1
```

Assessment type may be distinguished with subtle typography, a small icon, or a restrained accent.

Keep the matrix mostly monochrome.

Click/tap an assessment header to expose edit/delete actions without cluttering every header permanently.

Click/tap a grade cell to edit that grade.

Click/tap a student name to open student detail.

---

# 7. Assessment creation UX

The primary class action is:

```text
+ Leistung
```

Open a small dialog or sheet with:

- name
- type
- date
- weight

Example:

```text
Neue Leistung

Name
[Test 2]

Typ
[Test]

Datum
[04.09.2026]

Gewichtung
[1.0]

[Abbrechen] [Erstellen]
```

After creation, do not merely return to the matrix.

Immediately open class-wide grade entry for the new assessment.

---

# 8. Fast class-wide grade entry

This is the most important screen in the application.

Show:

- class
- assessment name/type/date/weight
- all students
- current stored value/state
- one clearly active student

Provide a large touch-friendly grade pad:

```text
 0   1   2   3   4   5   6   7
 8   9  10  11  12  13  14  15
```

Also provide:

- clear/no grade
- absent, if supported

Required interaction:

1. User selects/taps `12`.
2. Grade is persisted immediately.
3. UI gives subtle confirmation.
4. Next student becomes active automatically.
5. Active row scrolls into view when needed.
6. User continues.

No per-grade Save button.

Do not reload the entire page for every grade.

Use HTMX to update only the necessary fragment.

If the save fails:

- do not advance automatically
- show a concise visible error
- preserve the attempted state where sensible
- allow retry
- never silently lose a grade

Desktop convenience may include keyboard entry:

- number keys
- Enter to advance
- arrow navigation

Touch UX has priority.

---

# 9. Student detail

Student detail must remain minimal.

Show:

- student name
- overall calculated average
- written average
- oral average
- assessment history

Example:

```text
Anna Becker

Gesamt       11.8
Schriftlich  10.3
Mündlich     13.2

Test 1                    12
Test 2                    13
Klassenarbeit 1            9
Mündlich September        14
```

Do not add:

- charts
- radar diagrams
- AI comments
- behavior tracking
- attendance tracking
- competency grids
- messaging

unless explicitly requested later.

---

# 10. Class and student management UX

Rare administrative actions should be accessible but visually secondary.

Use a compact overflow/settings area for things such as:

- Klasse bearbeiten
- Gewichtung einstellen
- Schüler verwalten
- Export/Backup
- Klasse löschen

Do not create top-level navigation for each administrative function.

Bulk student paste is required.

Destructive actions require confirmation.

---

# 11. Visual design

The application should feel like a restrained blend of:

- a professional spreadsheet
- a high-quality productivity tool
- Apple Numbers / Linear-like restraint

Do not copy a specific product.

Design principles:

- compact
- calm
- professional
- mostly monochrome
- subtle borders
- very little shadow
- no gradients unless truly useful
- no giant hero headers
- no card-in-card dashboard
- no excessive rounded rectangles
- no colorful KPI widgets
- no decorative icon overload
- no unnecessary animation

Use system fonts. Do not load remote fonts.

Use CSS custom properties, for example:

```css
--bg
--surface
--surface-subtle
--text
--muted
--border
--accent
--danger
--radius
--space-xs
--space-sm
--space-md
--space-lg
```

Vanilla CSS only.

Dark mode is optional and low priority. If implemented, do it simply with CSS variables and platform preference.

---

# 12. Accessibility

Use:

- semantic HTML
- real buttons
- real labels
- visible focus states
- sufficient contrast
- keyboard navigation where sensible
- native dialog where practical
- ARIA only where it is actually needed

Do not make the touch UI awkward in pursuit of unnecessary abstraction.

---

# 13. Technology stack

Use this stack unless a current incompatibility makes a small adjustment necessary.

## Runtime

Prefer:

```text
Go 1.27.1
```

If the exact patch release is unavailable in the active environment, use the current stable Go 1.27.x (or newer current stable only when necessary), then pin the selected exact version consistently in:

- `go.mod`
- `toolchain` directive where appropriate
- Docker builder image
- GitHub Actions

Do not use a Node.js production runtime.

## HTTP

Use:

```text
net/http
```

Do not introduce a Go web framework unless genuinely necessary.

## HTML templates

Use:

```text
templ
```

## Frontend interaction

Use:

```text
HTMX
```

Bundle HTMX locally.

Do not load it from a CDN.

## JavaScript

Use:

```text
vanilla JavaScript
```

Keep it tiny.

Do not add Alpine.js initially.

Do not add:

- React
- Vue
- Svelte
- Angular
- Solid
- Stimulus
- jQuery

## CSS

Use:

```text
vanilla CSS
```

Do not add:

- Tailwind
- Bootstrap
- Material UI
- shadcn/ui
- a CSS-in-JS system

## Database

Use:

```text
SQLite
```

Production DB path:

```text
/data/noten.db
```

## SQLite driver

Prefer:

```text
modernc.org/sqlite
```

Reason:

- no CGO
- static binary friendliness
- scratch-container friendliness

Do not switch to a CGO SQLite driver merely for convenience.

## Query layer

Use:

```text
sqlc
```

Do not introduce an ORM.

## Sessions

Use:

```text
github.com/alexedwards/scs/v2
```

Sessions must survive Fly Machine auto-stop/start cycles.

Do NOT use only an in-memory session store in production, because scale-to-zero restarts would force a login after every stop.

Implement a small SCS store backed by the same SQLite database using `database/sql` + `modernc.org/sqlite`, or another equally small CGO-free persistent SCS solution.

Do not introduce Redis or a second database for sessions.

## CSRF / cross-origin protection

Prefer the current standard-library mechanism:

```text
net/http CrossOriginProtection
```

when supported by the selected Go version.

Do not add `nosurf` unless current Go APIs are demonstrably insufficient.

---

# 14. Architecture philosophy

Keep the request path simple:

```text
browser
→ net/http handler
→ small domain/service function where useful
→ sqlc
→ SQLite
→ templ
→ HTML
→ HTMX
```

Do not create a separate JSON API for the frontend.

Normal page navigation returns full HTML pages.

HTMX endpoints return HTML fragments.

Do not introduce unnecessary layers such as:

- repository interfaces everywhere
- DTO mapping layers
- dependency injection containers
- CQRS
- event sourcing
- microservices
- domain-event buses
- GraphQL
- a separate REST backend for the web UI

Use plain, idiomatic Go.

---

# 15. Suggested repository layout

Use something close to:

```text
.
├── AGENTS.md
├── README.md
├── Makefile
├── Dockerfile
├── fly.toml
├── go.mod
├── go.sum
├── sqlc.yaml
├── .gitignore
├── .env.example
│
├── cmd/
│   └── web/
│       └── main.go
│
├── internal/
│   ├── app/
│   ├── auth/
│   ├── classes/
│   ├── students/
│   ├── assessments/
│   ├── grades/
│   ├── db/
│   └── web/
│
├── db/
│   ├── migrations/
│   └── queries/
│
├── ui/
│   ├── layouts/
│   ├── components/
│   └── pages/
│
├── static/
│   ├── app.css
│   ├── app.js
│   ├── htmx.min.js
│   ├── app.webmanifest
│   ├── offline.html
│   └── icons/
│       ├── icon.svg
│       └── icon-maskable.svg
│
├── sw.js
│
└── .github/
    └── workflows/
        ├── ci.yml
        └── deploy.yml
```

Adjust package layout only when a simpler idiomatic arrangement is clearly better.

Do not create folders merely to imitate enterprise architecture.

---

# 16. Database schema

Create a normalized but small schema.

At minimum:

## `classes`

Suggested fields:

- `id`
- `name`
- `subject`
- `written_weight`
- `oral_weight`
- `created_at`
- `updated_at`

## `students`

Suggested fields:

- `id`
- `class_id`
- `first_name`
- `last_name`
- `sort_order`
- `created_at`
- `updated_at`

## `assessments`

Suggested fields:

- `id`
- `class_id`
- `name`
- `type`
- `date`
- `weight`
- `created_at`
- `updated_at`

## `grades`

Suggested fields:

- `id`
- `assessment_id`
- `student_id`
- nullable `points`
- optional `status`
- `created_at`
- `updated_at`

## `sessions`

Add the fields needed by the persistent SCS SQLite store.

## Constraints

Enforce important rules at the database level:

- assessment type only contains valid values
- points are NULL or between 0 and 15
- assessment weight is positive
- written/oral class weights are valid
- student + assessment pair is unique
- foreign keys are explicit
- delete/cascade/restrict behavior is intentional

Do not rely only on application validation.

---

# 17. SQLite configuration

On database startup, configure at least:

```sql
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;
PRAGMA synchronous = NORMAL;
```

For this single-user application, prefer:

```go
db.SetMaxOpenConns(1)
```

unless tests demonstrate a compelling reason not to.

Correctness and simplicity matter more than theoretical concurrency.

Use transactions for logically atomic operations.

The application must safely handle repeated:

```text
start → use → stop → start
```

cycles caused by Fly scale-to-zero.

---

# 18. Database migrations

Do not require an external migration service.

Store numbered SQL migrations in:

```text
db/migrations/
```

Example:

```text
001_initial.sql
002_add_sessions.sql
003_...
```

Embed migrations into the Go binary.

At startup:

1. open SQLite
2. configure pragmas
3. ensure migration metadata table exists
4. detect pending migrations
5. apply each pending migration transactionally
6. mark it applied
7. start the HTTP server

Already applied numbered migrations must never be rerun.

Once a migration has been released, do not silently rewrite it. Add a new migration instead.

---

# 19. sqlc rules

Keep SQL explicit in:

```text
db/queries/
```

Examples:

```text
classes.sql
students.sql
assessments.sql
grades.sql
sessions.sql
```

Use sqlc-generated strongly typed code.

Generated sqlc files must be committed to Git.

Do not manually edit generated sqlc output.

Do not add an ORM.

---

# 20. templ rules

Use templ for:

- application layout
- sidebar
- class page
- grade matrix
- assessment dialog
- grade-entry view
- student detail
- form fragments
- error fragments

Generated `*_templ.go` files must be committed to Git.

Do not manually edit generated templ output.

Do not fragment the UI into hundreds of tiny components. Keep components meaningful.

---

# 21. Tool pinning and reproducible builds

Pin generation tools in the repository.

Prefer Go's supported tool dependency mechanism for:

- templ
- sqlc

The repository must still compile immediately after clone from committed generated files without requiring generation first.

Generation is required only after changing templ/SQL sources.

The same commands must work:

- locally
- in Codex Cloud
- in GitHub Actions

Avoid environment-specific build paths.

---

# 22. Makefile contract

Create a small Makefile with at least:

```text
generate
fmt
test
vet
check
build
run
```

Desired semantics:

## `make generate`

- run templ generation
- run sqlc generation

## `make fmt`

- format Go code
- format templ/source where supported without adding heavy tooling

## `make test`

- `go test ./...`

## `make vet`

- `go vet ./...`

## `make check`

Run:

1. generation
2. formatting
3. vet
4. tests
5. verify generated files/source formatting are committed/up to date

`make check` must fail if generation creates uncommitted changes.

## `make build`

Build the application deterministically.

For production-compatible Linux build, use:

```text
CGO_ENABLED=0
```

## `make run`

Run local development with a local SQLite database path.

Before finishing any coding task, run:

```bash
make check
```

and fix all failures.

---

# 23. Authentication

This is a private single-user application.

Do not build:

- registration
- password reset email
- OAuth
- multiple roles
- organizations
- user profiles
- invitations

Implement:

```text
GET  /login
POST /login
POST /logout
```

Protect all application routes except:

- `/login`
- `/healthz`
- approved public static/PWA assets

Use a production secret such as:

```text
ADMIN_PASSWORD
```

Store the plaintext production password only in the Fly secret/environment variable. Compare submitted passwords with `crypto/subtle.ConstantTimeCompare`; never store or log the value.

Session cookies in production must be:

- HttpOnly
- Secure
- SameSite set appropriately
- scoped narrowly enough
- long-lived enough for normal personal use

Persistent SCS sessions must survive Machine stop/start.

Do not log passwords, cookies, tokens, or password hashes.

---

# 24. Security and privacy

This application stores student names and grades.

Security is mandatory, but avoid enterprise bloat.

Required:

- HTTPS in production
- authentication for all gradebook data
- CSRF/cross-origin request protection
- secure cookies
- prepared/sqlc queries
- SQL constraints
- conservative error messages
- no secrets in Git
- no production `.env`
- no student/grade data in routine logs
- no third-party analytics
- no trackers
- no remote fonts
- no third-party JS/CDN dependencies
- no debug/admin endpoints exposed in production

Prefer:

```text
Cache-Control: no-store
```

for authenticated HTML and responses containing student names, grades, exports, or authentication-sensitive data.

Do not leak SQL errors to the browser.

---

# 25. Logging

Use minimal structured logging, preferably standard library logging.

Log:

- startup
- migration status
- listening address
- unexpected server errors
- authentication failures without credentials

Do not routinely log:

- student names
- grades
- request bodies containing grades
- cookies
- session tokens
- passwords

---

# 26. Health endpoint

Implement:

```text
GET /healthz
```

Requirements:

- no authentication
- fast
- returns HTTP 200 when healthy
- returns no sensitive details

A simple response:

```text
ok
```

Optionally execute `SELECT 1` if it remains fast and reliable.

---

# 27. Static assets

All production frontend assets must be local.

Bundle:

- HTMX
- CSS
- JavaScript
- manifest
- service worker
- icons
- favicon
- optional offline page

Do not load:

- HTMX from a CDN
- Google Fonts
- remote icon libraries
- unpkg/jsDelivr assets
- analytics
- tracking pixels

Prefer `go:embed` so production remains essentially one binary plus the SQLite file.

Do not add nginx, Caddy, or a separate static asset service.

---

# 28. Installable PWA

The application must be installable as a minimal Progressive Web App.

Primary install target:

- Android tablet
- Samsung Galaxy Tab
- Chromium-based browsers / Samsung Internet

Also support installation reasonably in Chrome/Edge desktop.

The PWA exists to provide:

- home-screen installation
- standalone display
- proper icon
- local static assets
- app-like launch

The PWA is NOT offline-first.

Do not implement:

- offline grade editing
- background sync
- push notifications
- offline database synchronization
- a client-side data store

The server + SQLite remain the source of truth.

---

# 29. Web app manifest

Create a manifest such as:

```text
/static/app.webmanifest
```

Reference it from full HTML pages.

Use values conceptually like:

```json
{
  "name": "Noten",
  "short_name": "Noten",
  "start_url": "/",
  "scope": "/",
  "display": "standalone",
  "background_color": "#ffffff",
  "theme_color": "#ffffff",
  "prefer_related_applications": false,
  "icons": [
    {
      "src": "/static/icons/icon.svg",
      "sizes": "any",
      "type": "image/svg+xml",
      "purpose": "any"
    },
    {
      "src": "/static/icons/icon-maskable.svg",
      "sizes": "any",
      "type": "image/svg+xml",
      "purpose": "maskable"
    }
  ]
}
```

Adapt colors to the final design.

Do not add manifest capabilities with no product need.

---

# 30. PWA icons

Provide local icons at minimum:

- scalable SVG

Provide a maskable SVG icon as well so all committed icon assets remain text-reviewable.

Keep the icon visually minimal and professional.

Generate it locally as a repository asset if needed.

Do not fetch an icon from an external service.

Do not make production runtime depend on image-processing software.

---

# 31. Minimal service worker

Implement exactly one minimal service worker, preferably at:

```text
/sw.js
```

Register it with a few lines of vanilla JS.

Do not add Workbox or a service-worker build framework.

The service worker may cache ONLY approved static, non-sensitive assets such as:

```text
/static/app.css
/static/app.js
/static/htmx.min.js
/static/app.webmanifest
/static/icons/icon.svg
/static/icons/icon-maskable.svg
/static/offline.html
```

## Critical privacy rule

Never cache:

- authenticated HTML
- class pages
- grade matrix HTML
- student pages
- student names
- assessment responses
- HTMX fragments containing private data
- grade-entry responses
- login responses
- POST/PUT/PATCH/DELETE responses
- CSV exports
- backups
- any dynamically generated private data

Do not use:

```javascript
cache.addAll(["/"])
```

if `/` may render authenticated data.

Do not use generic stale-while-revalidate for application pages.

Recommended strategy:

```text
approved static assets -> cache first
navigation/dynamic HTML -> network only
HTMX -> network only
mutations -> network only
private/authenticated content -> network only
```

If navigation fails because the device truly has no network, it is acceptable to return a cached static `offline.html` page that contains no private data.

Do not show stale cached grades.

Do not allow offline writes.

---

# 32. Service-worker cache hygiene

Use a simple app-specific version, for example:

```javascript
const CACHE_NAME = "noten-static-v1";
```

On install:

- cache only the explicit approved static asset list

On activate:

- remove old caches owned by this application
- do not remove unrelated caches

Service worker file itself should not be aggressively cached in a way that prevents updates.

Authenticated/private HTTP responses should use `Cache-Control: no-store` where appropriate.

A deployment must not leave the user indefinitely stuck on stale JS/CSS.

---

# 33. PWA standalone navigation

Because installed mode uses:

```text
display: standalone
```

do not rely on browser Back UI for essential navigation.

Provide in-app:

- class navigation
- back/cancel where needed
- close dialog
- return from student detail
- return from assessment grade entry

Do not add a large navigation system.

---

# 34. Docker

Use a multi-stage Docker build.

Builder:

- official Go image pinned to the selected Go version

Final:

```dockerfile
FROM scratch
```

Compile with:

```text
CGO_ENABLED=0
```

Use:

- `-trimpath`
- sensible linker flags such as `-s -w` if appropriate

Embed application static assets into the Go binary.

Include Go timezone data when required, e.g. with the appropriate `time/tzdata` mechanism, so `Europe/Berlin` works in scratch.

The production image should be extremely small and contain only what is necessary.

Conceptually production consists of:

```text
/app
```

plus persistent data:

```text
/data/noten.db
```

Do not add a Node runtime.

Do not add shell-based runtime requirements.

---

# 35. Runtime configuration

Support configuration via environment variables such as:

```text
PORT=8080
DATABASE_PATH=/data/noten.db
ADMIN_PASSWORD=...
APP_ENV=production
TZ=Europe/Berlin
```

Local defaults may use a repository-local temporary DB outside production.

Do not commit:

- `.env`
- passwords
- password hashes intended for production
- Fly tokens
- GitHub tokens
- SQLite production data

An `.env.example` may document variable names with dummy values only.

---

# 36. Fly.io production architecture: authoritative requirements

Production MUST use exactly:

- one Fly.io application
- exactly one Fly Machine
- exactly one persistent Fly Volume
- region `fra`
- `shared-cpu-1x`
- one shared CPU
- exactly 256 MB RAM
- automatic stop when idle
- automatic start on incoming traffic
- zero Machines required to remain running while idle

This is optimized for minimum recurring compute cost.

Cold starts after inactivity are acceptable.

Cost minimization has priority over eliminating cold starts.

Do NOT:

- keep a Machine permanently running
- deploy redundant Machines
- create standby Machines
- create a second Machine for HA
- deploy multi-region
- use LiteFS
- use Postgres
- use Redis
- add keep-alive pings
- add cron jobs that wake the app
- add external uptime monitoring that prevents scale-to-zero

Exactly one Machine must exist—not one running plus another stopped spare.

---

# 37. Fly Machine size

The intended current configuration is conceptually:

```toml
[[vm]]
  size = "shared-cpu-1x"
  memory = "256mb"
```

Before finalizing, verify current Fly syntax using current official documentation or installed `flyctl` help.

Do not silently increase memory.

The application must fit comfortably into 256 MB.

If memory usage is unexpectedly high, fix the application instead of increasing the instance size.

---

# 38. Fly auto-start / auto-stop

The intended `http_service` behavior is conceptually:

```toml
[http_service]
  internal_port = 8080
  force_https = true
  auto_stop_machines = "stop"
  auto_start_machines = true
  min_machines_running = 0
```

The desired lifecycle is:

```text
request arrives
→ Fly starts the stopped Machine
→ Go process starts
→ /data Volume mounts
→ SQLite opens
→ migrations run
→ app serves request

idle period
→ Fly stops Machine
→ compute charges stop

next request
→ Machine auto-starts again
```

Do not set:

```text
min_machines_running = 1
```

Do not keep the app artificially awake.

---

# 39. Fly persistent Volume

Use exactly one persistent Fly Volume.

Region:

```text
fra
```

Mount:

```text
/data
```

SQLite DB:

```text
/data/noten.db
```

Use a small reasonable volume, normally 1 GB unless Fly's current minimum requires something else.

Do not store static application assets on the Volume.

Only persistent app data belongs under `/data`.

---

# 40. Graceful lifecycle

The process must tolerate repeated start/stop cycles.

Startup:

1. initialize config
2. open SQLite
3. apply pragmas
4. apply pending migrations
5. initialize session store
6. initialize routes/assets
7. start HTTP server

Shutdown:

1. receive termination signal
2. stop accepting new traffic
3. allow a short bounded graceful shutdown
4. finish outstanding writes
5. close SQLite cleanly

Do not assume a continuously running server.

---

# 41. Fly configuration target

The final `fly.toml` should conceptually resemble:

```toml
app = "APP_NAME"
primary_region = "fra"

[build]
  dockerfile = "Dockerfile"

[env]
  PORT = "8080"
  DATABASE_PATH = "/data/noten.db"
  APP_ENV = "production"
  TZ = "Europe/Berlin"

[http_service]
  internal_port = 8080
  force_https = true
  auto_stop_machines = "stop"
  auto_start_machines = true
  min_machines_running = 0

  [[http_service.checks]]
    grace_period = "5s"
    interval = "30s"
    method = "GET"
    timeout = "2s"
    path = "/healthz"

[[mounts]]
  source = "data"
  destination = "/data"

[[vm]]
  size = "shared-cpu-1x"
  memory = "256mb"
```

Use the current valid Fly syntax if it has changed.

The intent is authoritative:

```text
ONE app
ONE Machine
ONE shared CPU
256 MB
ONE Volume
fra
auto-start ON
auto-stop STOP
min running 0
```

---

# 42. High availability must be disabled

Initial provisioning/deployment must explicitly avoid Fly's automatic redundant-Machine behavior.

Use the current equivalent of:

```bash
fly launch --ha=false
```

Deploy with the current equivalent of:

```bash
fly deploy --remote-only --ha=false
```

or:

```bash
fly deploy --ha=false
```

If necessary, enforce:

```bash
fly scale count 1
```

After initial deployment, verify the actual Machine inventory.

Do not treat one running Machine plus one stopped Machine as compliant.

There must be exactly one Machine object.

---

# 43. GitHub Actions

Set up CI/CD.

## Pull requests

Run:

- generation verification
- formatting
- vet
- tests
- build

## Push to `main`

Run the full checks first.

Deploy only if checks pass.

Use the current Fly GitHub deployment approach.

Conceptually:

```yaml
- run: flyctl deploy --remote-only --ha=false
  env:
    FLY_API_TOKEN: ${{ secrets.FLY_API_TOKEN }}
```

Use concurrency control to prevent simultaneous Fly deployments.

Never commit `FLY_API_TOKEN`.

Prefer an app-scoped Fly deploy token rather than a broad personal token.

The deploy workflow must preserve exactly one Machine.

---

# 44. Git/GitHub autonomy

If the repository already exists:

- inspect `git status`
- inspect current branch
- inspect remotes
- preserve existing history and sensible conventions

If no Git repository exists:

- initialize one

If authenticated GitHub access is available and no remote exists:

- create a private repository
- use a simple name such as `noten` or `gradebook`
- push the implementation

Do not make it public without explicit instruction.

Do not commit:

- database files
- secrets
- `.env`
- generated runtime backups
- binaries
- tokens

Use clear commits.

---

# 45. Fly autonomy

If authenticated Fly access is available:

- inspect authentication
- inspect whether the app already exists
- create the app if needed
- provision the single `fra` Volume if needed
- ensure exactly one Machine
- configure secrets
- deploy
- verify

If a required external credential or permission is unavailable:

- complete everything else
- do not leave the code half-configured
- state exactly which credential/action is missing

Do not invent credentials.

---

# 46. Fly deployment verification

After initial production deployment verify:

- region is `fra`
- exactly one Machine exists
- Machine uses `shared-cpu-1x`
- one CPU
- exactly 256 MB RAM
- exactly one Volume exists
- Volume is attached
- Volume is mounted at `/data`
- SQLite is at `/data/noten.db`
- `auto_start_machines = true`
- auto-stop uses actual `"stop"` behavior
- `min_machines_running = 0`
- HTTPS works
- `/healthz` works

Where practical, verify lifecycle persistence:

1. create test data
2. restart/stop the Machine
3. access the app again
4. verify auto-start
5. verify the data remains
6. verify persistent login/session behavior remains reasonable

Do not declare deployment complete before verifying persistence across a restart/stop-start cycle where tooling permits.

---

# 47. Cold-start UX

The first request after inactivity may be slower because Fly starts the Machine.

This is acceptable.

Do not prevent cold starts by paying for an always-running Machine.

The installed PWA may show a minimal neutral loading state such as:

```text
App wird gestartet …
```

if helpful, but do not build complex startup infrastructure.

Do not use keep-alive traffic.

---

# 48. Backup and export

Grade data matters.

Implement a simple authenticated backup/export mechanism without bloating the product.

At minimum provide:

- CSV export of a class grade matrix

Preferred additional backup:

- authenticated download of a consistent SQLite database backup

If implementing database backup, use a SQLite-safe mechanism such as `VACUUM INTO` to a server-generated temporary file and stream the completed file; do not naively copy an active WAL database.

Clean temporary backup files after serving them.

Keep backup/export actions in a secondary settings/overflow area.

Do not cache backup or export responses.

Do not expose them without authentication.

---

# 49. HTMX usage

Use HTMX for interactions where it reduces complexity:

- grade save/update
- grade cell edit
- assessment creation
- assessment edit/delete
- student add/edit/delete
- class settings
- dialogs/sheets
- partial matrix refreshes

Do not force HTMX onto normal navigation that is simpler as regular links.

Server/database remains the source of truth.

Do not duplicate domain state into a client-side store.

---

# 50. Vanilla JavaScript responsibilities

Keep JS small and local.

Appropriate uses:

- active student state in grade-entry screen
- grade-pad ergonomics
- automatic focus/advance
- `scrollIntoView`
- keyboard shortcuts
- service-worker registration
- small native-dialog enhancements

Do not build a frontend state framework.

Prefer native browser APIs.

---

# 51. Native browser features

Prefer native capabilities when reliable:

- `<dialog>`
- `position: sticky`
- form validation
- `scrollIntoView`
- focus management
- CSS grid/flex
- semantic table markup where appropriate

Do not add libraries for simple browser functionality.

---

# 52. Autosave feedback

Grade entry must autosave.

On successful save:

- subtle check/transition is enough
- do not show a toast after every grade

On failure:

- visible error
- no automatic advance
- retry possible

Never silently discard a grade.

---

# 53. Destructive actions

Require explicit confirmation for:

- delete class
- delete assessment
- delete student when related grade data will be removed

The confirmation must clearly state the consequences.

Do not build a complex recycle-bin subsystem.

---

# 54. Testing

Create meaningful deterministic automated tests.

At minimum test:

## Grade calculations

- weighted assessment averages
- written/test classification
- oral category
- 50/50 combination
- 60/40 or other class weighting
- only written grades exist
- only oral grades exist
- no grades exist
- 0 is a real grade
- 15 is valid
- missing is not 0
- absent is not 0
- changed assessment weight changes result
- changed assessment type changes category result

Example:

```text
KA1 = 10 weight 2
Test1 = 12 weight 1
written = 10.666...

M1 = 13
M2 = 11
oral = 12

50/50 overall = 11.333...
display = 11.3
```

## Database behavior

- migrations on empty DB
- migrations on already initialized DB
- grade UPSERT
- grade uniqueness
- range validation
- foreign-key behavior
- CRUD for core objects

## Authentication/security

- unauthenticated gradebook access rejected
- login success/failure
- logout
- session persistence across app restart where practical
- mutation request protection

## HTTP

- health endpoint
- key page handlers
- HTMX grade mutation response

Use temporary SQLite databases.

Never rely on production data.

---

# 55. Browser smoke testing

If a browser automation capability is available, use it.

Test at minimum:

- login page renders
- login works
- class creation works
- class rename/edit works
- bulk student paste works
- student rename/edit works
- student delete confirmation works
- assessment creation works
- assessment edit/weight change works
- assessment delete confirmation works
- grade entry 0–15 works
- changing an existing grade works
- clearing a grade works
- fast auto-advance works
- grade matrix updates
- averages recalculate
- horizontal scrolling works
- sticky student column works
- student detail opens
- tablet landscape viewport is usable
- logout works
- browser console has no obvious errors

Compilation alone is not proof of UX correctness.

---

# 56. PWA acceptance testing

Verify:

- manifest loads
- manifest content type is correct
- 192x192 icon exists
- 512x512 icon exists
- maskable icon exists if referenced
- `start_url` works
- `scope` works
- `display` is standalone
- HTTPS works in production
- service worker registers
- service worker has no console errors
- approved static assets are cacheable
- dynamic authenticated HTML is not in Cache Storage
- grade responses are not cached
- student data is not cached
- mutation responses are not cached
- CSV/backup data is not cached
- logout behaves correctly
- a new deployment can update JS/CSS
- installed mode remains navigable without browser Back UI
- tablet-sized installed view works

Explicitly inspect browser Cache Storage after using real app flows.

If student names or grade HTML appear in service-worker cache, treat it as a production-blocking privacy bug.

---

# 57. Performance and memory

Expected dataset is tiny:

- a few classes
- tens of students per class
- tens of assessments
- thousands of grade rows

Do not optimize for hypothetical large SaaS scale.

Do not add:

- Redis
- queues
- caching services
- background workers
- search infrastructure
- distributed systems

The whole application must comfortably fit within 256 MB RAM.

Avoid:

- large in-memory caches
- loading entire unrelated datasets
- runtime image processing
- headless browsers in production
- memory-heavy libraries

---

# 58. README

Create a clear README containing:

- project purpose
- product scope
- architecture
- technology choices
- repository layout
- local setup
- environment variables
- generation commands
- `make check`
- tests
- Docker build
- SQLite/migration behavior
- PWA behavior
- Fly.io architecture
- GitHub Actions
- required secrets
- first deployment steps
- backup/export behavior
- security model

Avoid marketing fluff.

---

# 59. Dependency policy

Before adding any dependency, ask internally:

> Can this be implemented cleanly with the Go standard library, HTMX, templ, or a few lines of vanilla JS/CSS?

If yes, do not add the dependency.

Approved core third-party dependencies are limited to the needs of:

- templ
- sqlc
- modernc SQLite
- SCS v2
- a well-justified password hash dependency
- bundled HTMX asset

Everything else requires a clear concrete benefit.

---

# 60. Explicitly forbidden scope creep

Unless explicitly requested later, do NOT add:

- React
- Vue
- Svelte
- Angular
- Alpine.js
- Tailwind
- Bootstrap
- shadcn
- Node.js production runtime
- Postgres
- MySQL
- Redis
- Docker Compose
- Kubernetes
- ORMs
- GORM
- Prisma
- GraphQL
- frontend REST API layer
- JWT auth
- OAuth
- multi-user support
- organizations
- roles
- multi-region
- LiteFS
- microservices
- queues
- background job infrastructure
- analytics
- telemetry
- AI features
- student messaging
- parent portal
- attendance
- competency tracking
- notifications
- email infrastructure
- offline grade editing
- offline synchronization

Keep the application small.

---

# 61. Agent autonomy rules

Do not ask the user about minor implementation choices.

Examples that do not require clarification:

- route naming
- package names
- exact spacing
- icon choice
- dialog implementation
- internal SQL column naming
- whether a helper belongs in one small package or another
- button radius
- minor copy wording

Choose sensible defaults.

If two approaches are valid:

- choose the simpler one
- choose fewer dependencies
- choose standard library where practical
- choose the option that preserves the single-binary architecture
- choose the option easiest for a future coding agent to understand

Ask for user intervention only when progress is genuinely blocked by an external credential, permission, or irreversible external action that cannot be safely inferred.

---

# 62. Execution order for a fresh repository

Use approximately this sequence:

1. inspect repository
2. inspect Git/GitHub/Fly access
3. initialize/pin Go module and tools
4. create minimal project structure
5. implement SQLite connection
6. implement migrations
7. implement schema
8. configure sqlc
9. generate DB code
10. implement grade calculation logic and tests
11. implement persistent session store
12. implement authentication/security middleware
13. implement class CRUD
14. implement student CRUD + bulk paste
15. implement assessment CRUD
16. implement grade CRUD/UPSERT
17. implement calculated averages
18. build templ layout/sidebar
19. build grade matrix
20. build assessment creation/editing
21. build fast grade-entry flow
22. build student detail
23. build class settings
24. add CSV/backup capability
25. build vanilla CSS polish
26. add tiny vanilla JS behavior
27. add PWA manifest/icons
28. add service worker
29. add cache/privacy headers
30. add health endpoint
31. embed assets/migrations
32. add Dockerfile
33. add `fly.toml`
34. add GitHub Actions
35. add README
36. ensure this AGENTS.md remains accurate
37. run `make generate`
38. run `make check`
39. build Docker image
40. run locally
41. run browser smoke tests when available
42. fix failures
43. commit
44. push to GitHub when authenticated
45. provision/configure Fly when authenticated
46. deploy
47. verify exactly one Machine
48. verify 256 MB + `fra` + one Volume
49. verify health
50. verify stop/start persistence
51. perform production smoke test

Do not skip verification.

---

# 63. Definition of done

Do not declare the project complete until all applicable items pass.

## Product

- class create/edit/delete works
- student create/bulk-add/edit/delete works
- assessment create/edit/delete works
- assessment weight can be changed
- class written/oral weight can be changed
- grade create/edit/clear works
- absent/missing never equals zero
- grade calculations are correct
- matrix updates correctly
- student detail is correct
- fast grade-entry auto-advance is excellent on tablet

## Code

- generated code is current
- `make check` passes
- `go test ./...` passes
- `go vet ./...` passes
- production build uses `CGO_ENABLED=0`
- Docker scratch image builds
- migrations work on empty and existing DB
- no secrets are tracked

## Security

- login works
- logout works
- sessions persist through normal Fly stop/start
- unauthenticated app access is blocked
- secure cookie settings are correct in production
- mutation protection is active
- private responses use conservative caching
- logs do not expose student data

## PWA

- manifest works
- icons exist
- standalone installability works where supported
- all frontend assets are local
- service worker is minimal
- only approved static assets are cached
- no student/grade/authenticated HTML is cached
- no offline grade editing exists
- installed tablet navigation works

## Fly

- app is in `fra`
- exactly one Machine exists
- `shared-cpu-1x`
- exactly 256 MB RAM
- exactly one persistent Volume exists
- Volume mounted at `/data`
- DB at `/data/noten.db`
- auto-start enabled
- auto-stop actual stop enabled
- minimum running Machines is 0
- HA disabled
- no standby/spare Machine
- app auto-starts after stop
- data survives stop/restart
- HTTPS works
- health endpoint works

## GitHub

- repository is clean
- generated files committed
- CI is valid
- `main` deploys only after checks
- Fly deploy uses HA-disabled/single-Machine-safe command
- push completed when authenticated

---

# 64. Completion report

At the end of autonomous implementation, report concisely:

## Implemented

List major product features.

## Architecture

State the actual final stack and any justified deviation.

## Verification

List:

- `make check`
- tests
- vet
- Docker build
- browser/PWA smoke test

and whether each passed.

## GitHub

State:

- repository
- branch
- final commit
- pushed or not

## Fly.io

State:

- app name
- region
- Machine count
- Machine size/RAM
- Volume
- auto-stop/start status
- deployment status
- health status

## Remaining

Only list genuine remaining blockers.

If deployment is blocked by a missing external credential, state exactly which credential/action is missing.

Do not finish with a vague "ready to deploy" if authenticated deployment access was available and the deployment could have been performed.

---

# Final engineering principle

## Change safety (core product behavior)

The gradebook includes a durable, append-only audit history for successful changes to grades, assessments, students, and classes. Every audited domain mutation and its audit row must commit in the same SQLite transaction. Failed mutations must never appear as successful history.

Recent changes expose a compact immediate **Rückgängig** action. Undo references a server-side audit row, verifies that the current state still matches that row's resulting state, and refuses stale or conflicting undo rather than overwriting a newer change. An undo is itself appended to history. Grade history may intentionally restore an older value; this also appends a new restore entry and never rewrites existing history.

Classes, students, and assessments use soft deletion. Active queries and calculations exclude deleted records, while grades and relationships remain intact. The secondary **Gelöschte Elemente** view restores each entity independently; restoring a parent must not silently restore children that were separately deleted.

History, undo, trash, and restore routes are authenticated, use the existing cross-origin protection, and are never cached. The service-worker cache allowlist must remain limited to non-sensitive static assets. Keep grade-entry undo subtle and temporary so class-wide entry still advances immediately without confirmation dialogs.

Do not replace this mechanism with event sourcing, CQRS, a queue, a second datastore, or client-side authoritative state. Domain tables remain the source of truth and SQLite remains the sole persistent store.

This is a personal tool, not a startup platform.

Prefer:

```text
simple > clever
explicit > abstract
server-rendered > SPA
SQL > ORM
one binary > service graph
one Machine > HA
scale-to-zero > always-on
local assets > CDN
correct data > flashy UI
fast grade entry > feature count
```

A future developer—or coding agent—should be able to open this repository months later and understand almost everything quickly.

Preserve that property.
