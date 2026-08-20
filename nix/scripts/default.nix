# Each script is standalone, so `nix run .#dev-up` works without entering the dev shell first. dev-up and
# dev-rebuild take `version` so the image tag they reference matches what `.#kshared-image` produces.
{
  pkgs,
  version,
}: rec {
  dev-up = import ./dev-up.nix {inherit pkgs version;};
  dev-down = import ./dev-down.nix {inherit pkgs;};
  dev-rebuild = import ./dev-rebuild.nix {inherit pkgs dev-up dev-down;};
  dev-clean = import ./dev-clean.nix {inherit pkgs dev-down;};
}
