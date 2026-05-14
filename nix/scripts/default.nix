# Dev environment lifecycle scripts. Each script is a self-contained
# pkgs.writeShellScriptBin so it works under `nix run .#dev-up` etc.
# without needing the dev shell to be entered first. dev-up and
# dev-rebuild take the CalVer `version` so the image tag they reference
# matches what `nix build .#kshared-image` produces.
{
  pkgs,
  version,
}: rec {
  dev-up = import ./dev-up.nix {inherit pkgs version;};
  dev-down = import ./dev-down.nix {inherit pkgs;};
  dev-rebuild = import ./dev-rebuild.nix {inherit pkgs dev-up dev-down;};
  dev-clean = import ./dev-clean.nix {inherit pkgs dev-down;};
}
