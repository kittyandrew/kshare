{
  pkgs,
  version,
}:
pkgs.writeShellScriptBin "dev-up" ''
  set -euo pipefail
  # cd to repo root so volume mounts ($(pwd)/...) resolve no matter
  # which subdir the script was invoked from.
  cd "$(git rev-parse --show-toplevel 2>/dev/null || echo .)"

  if ! command -v docker &>/dev/null; then
    echo "ERROR: docker not found. On NixOS: virtualisation.docker.enable = true"
    exit 1
  fi
  if [ ! -f .env ]; then
    echo "ERROR: .env not found."
    echo "       Copy .env.example -> .env and fill in your Zitadel config first."
    exit 1
  fi

  mkdir -p ./data

  IMAGE_TAG="kshared:${version}"

  echo "[1/3] Building OCI image..."
  IMAGE_PATH=$(nix build .#kshared-image --no-link --print-out-paths -L)
  docker load < "$IMAGE_PATH" >/dev/null

  echo "[2/3] Stopping any existing container..."
  docker stop kshared 2>/dev/null && docker rm kshared 2>/dev/null || true

  echo "[3/3] Starting container ($IMAGE_TAG)..."
  # Force console-tinted logs in dev for human-readable output. The
  # CLI constructs public URLs from its persisted server URL, so we
  # don't need to override any base URL on the server side.
  docker run -d \
    --name kshared \
    -p 6980:6980 \
    --env-file .env \
    -e KSHARE_LOG_FORMAT=console \
    -v "$(pwd)/data:/data" \
    "$IMAGE_TAG" >/dev/null

  printf "      waiting for /healthz..."
  for i in $(seq 1 15); do
    if ${pkgs.curl}/bin/curl -sf http://localhost:6980/healthz >/dev/null 2>&1; then
      echo " ready"
      break
    fi
    if [ "$i" -eq 15 ]; then
      echo " TIMEOUT"
      docker logs kshared || true
      exit 1
    fi
    sleep 1
  done

  echo ""
  echo "  kshared is running at http://localhost:6980"
  echo ""
  echo "  CLI quickstart (in another terminal, with direnv loading .env):"
  echo "    nix run .#kshare -- auth login                  # defaults to localhost:6980"
  echo "    nix run .#kshare -- /path/to/file --ttl 1h"
  echo "    nix run .#kshare -- ls"
  echo ""
  echo "  Logs:        docker logs -f kshared"
  echo "  Rebuild:     nix run .#dev-rebuild   (after editing Go code)"
  echo "  Stop:        nix run .#dev-down      (preserves state in ./data)"
  echo "  Nuke state:  nix run .#dev-clean     (wipes ./data)"
''
