{
  pkgs,
  dev-down,
}:
# dev-clean = dev-down + wipe state. Composes dev-down so the
# `docker stop kshared` / `docker rm kshared` logic stays in exactly
# one place (dev-down).
pkgs.writeShellScriptBin "dev-clean" ''
  set -euo pipefail
  cd "$(git rev-parse --show-toplevel 2>/dev/null || echo .)"

  ${dev-down}/bin/dev-down

  if [ -d ./data ]; then
    echo "Removing ./data..."
    rm -rf ./data
  fi

  echo "Done. Run 'nix run .#dev-up' for a fresh start."
''
