{
  description = "ffxiv-stream: stream FINAL FANTASY XIV with Sunshine, Wolf or Selkies, set up by a wizard";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      forAll = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      packages = forAll (pkgs: rec {
        ffxiv-stream = pkgs.buildGoModule {
          pname = "ffxiv-stream";
          version = self.shortRev or "dev";
          src = ./.;
          # After changing go.mod/go.sum: set pkgs.lib.fakeHash, build, and paste the hash nix prints.
          vendorHash = "sha256-Kh9FwlPYK0l04KRamzYyNlUuur7/syRJebTfSxW2YxM=";
          subPackages = [ "cmd/ffxiv-stream" ];
          env.CGO_ENABLED = 0;
          ldflags = [ "-s" "-w" "-X main.version=${self.shortRev or "dev"}" ];
          meta = {
            description = "Stream FINAL FANTASY XIV with Sunshine, Wolf or Selkies, set up by a wizard";
            homepage = "https://github.com/Spaceghost/ffxiv-stream";
            license = pkgs.lib.licenses.mit;
            mainProgram = "ffxiv-stream";
          };
        };
        default = ffxiv-stream;
      });

      # NixOS declares services; `ffxiv-stream apply` does not write units there.
      # The wizard's config file is still the one source of settings.
      nixosModules.default = { config, lib, pkgs, ... }:
        let
          cfg = config.services.ffxiv-stream;
          pkg = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
          toml = pkgs.formats.toml { };
          configFile = if cfg.settings != null then toml.generate "ffxiv-stream.toml" cfg.settings else cfg.configFile;
        in
        {
          options.services.ffxiv-stream = {
            enable = lib.mkEnableOption "ffxiv-stream's host services (GPU sharing, container start)";
            configFile = lib.mkOption {
              type = lib.types.path;
              default = "/etc/ffxiv-stream/config.toml";
              description = "Config written by `ffxiv-stream wizard` (used when settings is null).";
            };
            settings = lib.mkOption {
              type = lib.types.nullOr toml.type;
              default = null;
              example = { topology = "incus"; backend = "sunshine"; gpu_share = { mode = "reserve"; container = "almanac"; }; };
              description = "The config as Nix, instead of configFile.";
            };
          };
          config = lib.mkIf cfg.enable {
            environment.systemPackages = [ pkg ];
            boot.kernelModules = [ "uinput" "uhid" ];
            systemd.services.ffxiv-stream-gpu-share = {
              description = "ffxiv-stream: give the game the GPU's memory while it runs";
              wantedBy = [ "multi-user.target" ];
              after = [ "incus.service" ];
              wants = [ "incus.service" ];
              path = [ pkgs.incus pkgs.curl pkgs.procps pkgs.iproute2 ];
              environment.FFXIV_STREAM_CONFIG = toString configFile;
              serviceConfig = {
                ExecStart = "${pkg}/bin/ffxiv-stream gpu-share";
                ExecStopPost = "${pkg}/bin/ffxiv-stream gpu-share --stop";
                Restart = "always";
                RestartSec = 5;
                StateDirectory = "ffxiv-stream";
              };
            };
            systemd.services.ffxiv-stream-container = {
              description = "ffxiv-stream: start the game container once its stream address exists";
              wantedBy = [ "multi-user.target" ];
              after = [ "incus.service" "tailscaled.service" "network-online.target" ];
              wants = [ "incus.service" "network-online.target" ];
              path = [ pkgs.incus pkgs.iproute2 ];
              environment.FFXIV_STREAM_CONFIG = toString configFile;
              serviceConfig = {
                Type = "oneshot";
                RemainAfterExit = true;
                ExecStart = "${pkg}/bin/ffxiv-stream start-container";
              };
            };
          };
        };
    };
}
