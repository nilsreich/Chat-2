package gradebook

import "embed"

// Files contains the single authoritative copies of migrations and web assets.
//
//go:embed db/migrations/*.sql static/* static/icons/* sw.js
var Files embed.FS
