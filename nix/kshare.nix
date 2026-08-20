{
  pkgs,
  src,
  rev,
  version,
}: let
  # vendorHash comes from go.sum, and one hash covers both binaries because they share the module and differ
  # only in subPackages. Bump it after `go get` changes deps (per .claude/rules/003-dev-stack.md):
  #   1. set to pkgs.lib.fakeHash
  #   2. run `nix build .#kshared`
  #   3. paste the "got" sha256 from the error message back here.
  vendorHash = "sha256-SO/4mM/NxWPb3euu0F2Nu9k/KTXpblWriSZ7yxWNS8Q=";

  # Docker's Healthcheck struct wants nanosecond ints. Self-documenting arithmetic beats a magic 10000000000.
  nsPerSec = 1000 * 1000 * 1000;

  kshare = pkgs.buildGoModule {
    pname = "kshare";
    inherit version src vendorHash;
    subPackages = ["cmd/cli"];
    postInstall = ''
      mv $out/bin/cli $out/bin/kshare
    '';
    meta.mainProgram = "kshare";
  };

  kshared = pkgs.buildGoModule {
    pname = "kshared";
    inherit version src vendorHash;
    subPackages = ["cmd/server"];
    ldflags = [
      "-X main.Commit=${builtins.substring 0 8 rev}"
      "-X main.Version=${version}"
    ];
    postInstall = ''
      mv $out/bin/server $out/bin/kshared
    '';
  };

  # OCI image, tagged with the CalVer version (e.g. `kshared:v26.05`). Load it with
  # `docker load < $(nix build .#kshared-image --print-out-paths)`. The tag is also exposed to the dev
  # scripts via Nix substitution, so they don't have to parse `docker load` output.
  kshared-image = pkgs.dockerTools.buildLayeredImage {
    name = "kshared";
    tag = version;
    contents = [
      kshared
      pkgs.cacert # outbound TLS to Zitadel JWKS
      pkgs.curl # used by the Docker Healthcheck below
    ];
    # @NOTE: scratch images have no NSS files. Lay them down so the kshared process can resolve its own uid
    # (1000). /data is bind-mounted at runtime, not part of the image, and the binary writes only under
    # /data, so no /tmp is needed.
    extraCommands = ''
      mkdir -p etc
      cat > etc/passwd <<-EOF
      	root:x:0:0:root:/root:/bin/sh
      	kshared:x:1000:1000:kshared:/data:/bin/sh
      	nobody:x:65534:65534:nobody:/:/bin/sh
      EOF
      cat > etc/group <<-EOF
      	root:x:0:
      	kshared:x:1000:
      	nogroup:x:65534:
      EOF
    '';
    config = {
      User = "1000:1000";
      Entrypoint = ["${kshared}/bin/kshared"];
      Env = [
        "SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
        "KSHARE_LISTEN=:6980"
        "KSHARE_DATA=/data"
      ];
      ExposedPorts = {"6980/tcp" = {};};
      WorkingDir = "/data";
      Healthcheck = {
        Test = ["CMD" "${pkgs.curl}/bin/curl" "-sf" "http://localhost:6980/healthz"];
        Interval = 10 * nsPerSec;
        Timeout = 3 * nsPerSec;
      };
    };
  };
in {inherit kshare kshared kshared-image;}
