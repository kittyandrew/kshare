{
  pkgs,
  dev-up,
  dev-down,
}:
# Composing dev-down and dev-up keeps the `docker run` invocation in exactly one place, so a new env var,
# port or mount is never edited in two scripts.
pkgs.writeShellScriptBin "dev-rebuild" ''
  set -euo pipefail
  ${dev-down}/bin/dev-down
  exec ${dev-up}/bin/dev-up
''
