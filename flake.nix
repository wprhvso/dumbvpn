{
  description = "Minimalist Hysteria 2 Android Client Development Environment";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    android-nixpkgs = {
      url = "github:tadfisher/android-nixpkgs";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    go-gost-x = {
      url = "github:wprhvso/go-gost-x";
      flake = false;
    };
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
      android-nixpkgs,
      go-gost-x,
    }:
    (flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs {
          inherit system;
          config.allowUnfree = true;
        };
        androidSdk = android-nixpkgs.sdk.${system} (
          sdkPkgs: with sdkPkgs; [
            cmdline-tools-latest
            platform-tools
            platforms-android-34
            build-tools-34-0-0
            ndk-26-1-10909125
          ]
        );
        ndkVersion = "26.1.10909125";
      in
      {
        packages.default = pkgs.buildGoModule {
          pname = "dumbvpn";
          version = "0.1.1";
          src = ./go-core;

          subPackages = [ "." ];

          env = {
            CGO_ENABLED = "0";
          };

          postPatch = ''
            cp -r ${go-gost-x} ./go-gost-x
            chmod -R +w ./go-gost-x
            go mod edit -replace github.com/go-gost/x=./go-gost-x
          '';

          vendorHash = "sha256-vktPgwPy/ZnGuw3ORiBulUMyfjSpwcZ+FHXrY9nQKpI=";
        };

        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [
            jdk17
            gradle
            go
            just
            scrcpy
            android-tools
            git
          ];

          shellHook = ''
            export ANDROID_HOME=${androidSdk}/share/android-sdk
            export ANDROID_SDK_ROOT=$ANDROID_HOME
            export ANDROID_NDK_ROOT=$ANDROID_HOME/ndk/${ndkVersion}
            export NDK_HOME=$ANDROID_NDK_ROOT
            export JAVA_HOME=${pkgs.jdk17.home}
            export PATH=$ANDROID_HOME/platform-tools:$ANDROID_HOME/cmdline-tools/latest/bin:$PATH
          '';
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
                ExecStart = "${self.packages.${pkgs.system}.default}/bin/dumbvpn";

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
