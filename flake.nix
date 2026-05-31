{
  description = "Minimalist Hysteria 2 Android Client Development Environment";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    android-nixpkgs = {
      url = "github:tadfisher/android-nixpkgs";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
      android-nixpkgs,
    }:
    flake-utils.lib.eachDefaultSystem (
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
    );
}
