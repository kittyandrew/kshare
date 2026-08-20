// Package upgrades holds the schema migrations dbutil applies at boot, in the mautrix/* convention: numbered
// SQL files in an embed.FS. Fresh databases land directly on the latest schema; the numbered files exist so
// existing deployments can step their way up.
package upgrades

import (
	"embed"

	"go.mau.fi/util/dbutil"
)

var Table dbutil.UpgradeTable

//go:embed *.sql
var upgrades embed.FS

func init() {
	Table.RegisterFS(upgrades)
}
