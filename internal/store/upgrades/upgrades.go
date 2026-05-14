// Package upgrades holds the versioned schema migrations consumed by
// dbutil. Same shape as the mautrix/* ecosystem: a single embed.FS of
// numbered SQL files, registered into a package-level
// dbutil.UpgradeTable in init().
//
// Fresh databases land directly on the latest schema; numbered files
// exist so existing deployments can step their way up.
package upgrades

import (
	"embed"

	"go.mau.fi/util/dbutil"
)

// Table is the package-level UpgradeTable wired into the Container's
// *dbutil.Database. dbutil.Database.Upgrade(ctx) consults this on boot.
var Table dbutil.UpgradeTable

//go:embed *.sql
var upgrades embed.FS

func init() {
	Table.RegisterFS(upgrades)
}
