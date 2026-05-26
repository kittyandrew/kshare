{
  description = "kshare -- OIDC-gated single-uploader file share";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = {
    self,
    nixpkgs,
    flake-utils,
  }:
    flake-utils.lib.eachDefaultSystem (system: let
      pkgs = nixpkgs.legacyPackages.${system};
      # CalVer: vYY.MM derived from the flake's source modification
      # date. `self.lastModifiedDate` is "YYYYMMDDhhmmss". May 2026
      # builds report `v26.05`. Operator-managed release tags follow
      # an expanded semver shape: `v0.YYMM.Z` (e.g. `v0.2605.0`) so
      # `git tag --sort=v:refname` orders cleanly across both. See
      # docs/deployment.md::OCI image for the tag-shape rationale.
      date = self.lastModifiedDate or "00000000";
      yy = builtins.substring 2 2 date;
      mm = builtins.substring 4 2 date;
      version = "v${yy}.${mm}";
      # cleanSourceWith filters out the local `data/` directory + Nix
      # build artifacts. Without this, running `dev-up` (which writes
      # ./data/share.db) busts the build cache on every change to dev
      # state. The filter composes with `cleanSourceFilter` (strips
      # .git/, swap files, editor backups) so the result is the same
      # shape as `cleanSource ./.` minus our extras.
      src = pkgs.lib.cleanSourceWith {
        src = ./.;
        filter = path: type: let
          base = baseNameOf (toString path);
        in
          pkgs.lib.cleanSourceFilter path type
          && base != "data"
          && !(pkgs.lib.hasPrefix "result" base);
      };
      kshare = import ./nix/kshare.nix {
        inherit pkgs version src;
        rev = self.rev or "dirty";
      };
      devScripts = import ./nix/scripts {inherit pkgs version;};
      # The NixOS module + agenix wiring for kshared live out of
      # tree, in the operator's own NixOS configuration. This repo
      # ships the OCI image only; integrate it via your own systemd
      # unit or NixOS module that consumes `kshared-image` (or pulls
      # the loaded image by tag).
    in {
      packages = {
        inherit (kshare) kshare kshared kshared-image;
        inherit (devScripts) dev-up dev-down dev-rebuild dev-clean;
        default = kshare.kshare;
      };

      devShells.default = pkgs.mkShell {
        name = "kshare-dev";
        packages = with pkgs; [
          # Go toolchain
          go
          gopls
          gotools
          delve
          go-tools # staticcheck

          # SQLite CLI for manual schema poking during dev
          sqlite

          # Container build / load for end-to-end testing
          docker

          # Git + GitHub CLI
          git
          gh

          # Smoke-test client
          curl

          # Dev lifecycle scripts on PATH so you can type `dev-up`
          # directly inside the dev shell (without the `nix run .#`
          # wrapper).
          devScripts.dev-up
          devScripts.dev-down
          devScripts.dev-rebuild
          devScripts.dev-clean
        ];

        shellHook = ''
          echo "kshare dev shell"
          echo "  go:    $(go version)"
          echo
          echo "Dev loop (end-to-end via Docker):"
          echo "  dev-up        # build image + start container + wait for /healthz"
          echo "  dev-rebuild   # rebuild image + restart after Go changes"
          echo "  dev-down      # stop container (state preserved in ./data)"
          echo "  dev-clean     # stop + wipe ./data"
          echo
          echo "Or hit handlers directly without Docker:"
          echo "  go run ./cmd/server            # server (needs .env loaded via direnv)"
          echo "  go run ./cmd/cli auth login    # CLI"
        '';
      };
    });
}
