{
  pkgs,
  dev-down,
}:
# Composes dev-down so the `docker stop` / `docker rm` logic stays in exactly one place.
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
