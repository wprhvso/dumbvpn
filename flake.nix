{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    gomod2nix = {
      url = "github:amarbel-llc/gomod2nix/7a765734636281d59e12f9ecabaa8c8a5746709a";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    {
      self,
      flake-utils,
      gomod2nix,
      ...
    }:
    (flake-utils.lib.eachDefaultSystem (
      system:
      let
        goBuilder = gomod2nix.legacyPackages.${system};
      in
      {
        packages.default = goBuilder.buildGoApplication {
          pname = "dumbvpn";
          version = "0.1.3";
          src = ./go-core;
          modules = ./go-core/gomod2nix.toml;
          GOSUMDB = "off";
        };
      }
    ))
    // {
      nixosModules.default =
        {
          config,
          pkgs,
          lib,
          ...
        }:
        let
          cfg = config.services.dumbvpn;
        in
        {
          options.services.dumbvpn = {
            enable = lib.mkEnableOption "DumbVPN Client Service";
          };

          config = lib.mkIf cfg.enable {
            systemd.services.dumbvpn = {
              description = "DumbVPN Client Service Daemon";

              after = [ "network-online.target" ];
              wants = [ "network-online.target" ];
              wantedBy = [ "multi-user.target" ];

              path = with pkgs; [
                iproute2
                iptables
                procps
                systemd
              ];

              serviceConfig = {
                ExecStart = "${self.packages.${pkgs.stdenv.hostPlatform.system}.default}/bin/dumbvpn";

                Restart = "always";
                User = "root";
                Group = "root";

                KillSignal = "SIGINT";
                TimeoutStopSec = 15;
              };
            };
          };
        };
    };
}
