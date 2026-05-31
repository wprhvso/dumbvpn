default:
    just --list

go-arm64:
    #!/usr/bin/env bash
    export CGO_ENABLED=1
    export GOOS=android
    export GOARCH=arm64
    
    export CC=$ANDROID_NDK_ROOT/toolchains/llvm/prebuilt/linux-x86_64/bin/aarch64-linux-android34-clang
    
    go build \
        -C go-core \
        -trimpath \
        -ldflags="-s -w -buildid=" \
        -buildmode=c-shared \
        -o ../android/app/src/main/jniLibs/arm64-v8a/libh2core.so \
        main.go

go-armv7:
    #!/usr/bin/env bash
    export CGO_ENABLED=1
    export GOOS=android
    export GOARCH=arm
    export GOARM=7
    
    export CC=$ANDROID_NDK_ROOT/toolchains/llvm/prebuilt/linux-x86_64/bin/armv7a-linux-androideabi34-clang
    
    go build \
        -C go-core \
        -trimpath \
        -ldflags="-s -w -buildid=" \
        -buildmode=c-shared \
        -o ../android/app/src/main/jniLibs/armeabi-v7a/libh2core.so \
        main.go

go: go-arm64 go-armv7

apk: go
    gradle -p android :app:assembleRelease

run: apk
    adb install -r android/app/build/outputs/apk/release/dumbvpn-v*-arm64-v8a.apk
    adb shell am start -n ru.murasya.vpn/.MainActivity

jar-tvf:
    jar tvf android/app/build/outputs/apk/release/*

du:
    du -sh android/app/build/outputs/apk/release/*

log:
    adb logcat --pid=$(adb shell pidof -s ru.murasya.vpn)

