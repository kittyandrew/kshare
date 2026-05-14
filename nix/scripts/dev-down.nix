{pkgs}:
pkgs.writeShellScriptBin "dev-down" ''
  set -euo pipefail
  echo "Stopping kshared..."
  if docker stop kshared 2>/dev/null; then
    docker rm kshared 2>/dev/null || true
    echo "  → stopped"
  else
    echo "  → not running"
  fi
  echo "Done. State preserved in ./data/ (run dev-clean to nuke)."
''
