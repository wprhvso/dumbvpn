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
        -o ../android/app/src/main/jniLibs/arm64-v8a/libdumbvpn.so \
        .

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
        -o ../android/app/src/main/jniLibs/armeabi-v7a/libdumbvpn.so \
        .

go: go-arm64 go-armv7

apk: go
    gradle -p android :app:assembleRelease

run: apk
    adb install -r android/app/build/outputs/apk/release/dumbvpn-v*-arm64-v8a.apk
    adb shell am start -n ru.murasya.vpn/.MainActivity

jar:
    jar tvf android/app/build/outputs/apk/release/*

ls:
    nu -c "ls android/app/build/outputs/apk/release/"

log:
    adb logcat --pid=$(adb shell pidof -s ru.murasya.vpn)

icon source:
    #!/usr/bin/env bash
    SOURCE="{{source}}"
    RES="android/app/src/main/res"

    if [ ! -f "$SOURCE" ]; then
        echo "File $SOURCE not found!"
        exit 1
    fi

    mkdir -p $RES/mipmap-{mdpi,hdpi,xhdpi,xxhdpi,xxxhdpi}

    magick $SOURCE -resize 48x48   $RES/mipmap-mdpi/ic_goose.png
    magick $SOURCE -resize 72x72   $RES/mipmap-hdpi/ic_goose.png
    magick $SOURCE -resize 96x96   $RES/mipmap-xhdpi/ic_goose.png
    magick $SOURCE -resize 144x144 $RES/mipmap-xxhdpi/ic_goose.png
    magick $SOURCE -resize 192x192 $RES/mipmap-xxxhdpi/ic_goose.png
