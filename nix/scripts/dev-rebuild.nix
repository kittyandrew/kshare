{
  pkgs,
  dev-up,
  dev-down,
}:
# dev-rebuild is the same as a stop + fresh start. Composing dev-down
# + dev-up keeps the `docker run` invocation in exactly one place
# (dev-up) so a new env var, port, or mount doesn't have to be edited
# in two scripts.
pkgs.writeShellScriptBin "dev-rebuild" ''
  set -euo pipefail
  ${dev-down}/bin/dev-down
  exec ${dev-up}/bin/dev-up
''
