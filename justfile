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

get VERSION:
    #!/usr/bin/env bash
    set -euo pipefail

    KEY_FILE="secrets/enc_key"
    if [ ! -f "$KEY_FILE" ]; then
        echo "ERROR: $KEY_FILE not found." >&2
        exit 1
    fi
    ENC_KEY="$(sed -e 's/[[:space:]]*$//' "$KEY_FILE" | tr -d '\n')"
    if [ -z "$ENC_KEY" ]; then
        echo "ERROR: $KEY_FILE is empty." >&2
        exit 1
    fi
    export ENC_KEY

    DEST="releases/{{VERSION}}"
    TMP="$DEST/.enc"
    mkdir -p "$TMP"

    gh release download "{{VERSION}}" --dir "$TMP" --clobber
    (cd "$TMP" && sha256sum -c SHA256SUMS.txt)

    for f in "$TMP"/*.enc; do
        out="$DEST/$(basename "${f%.enc}")"
        openssl enc -d -aes-256-cbc -pbkdf2 -iter 200000 \
            -in "$f" -out "$out" \
            -pass env:ENC_KEY
    done

    rm -rf "$TMP"
    ls -la "$DEST"

push-secrets:
    #!/usr/bin/env bash
    set -euo pipefail

    DIR="secrets"
    if [ ! -d "$DIR" ]; then
        echo "Directory '$DIR/' not found."
        exit 1
    fi

    set_secret() {
        name="$1"
        file="$DIR/$2"
        if [ ! -f "$file" ]; then
            echo "SKIP  $name (no $file)"
            return
        fi
        value="$(sed -e 's/[[:space:]]*$//' "$file" | tr -d '\n')"
        if [ -z "$value" ]; then
            echo "SKIP  $name (empty after trim)"
            return
        fi
        printf '%s' "$value" | gh secret set "$name"
        echo "OK    $name"
    }

    set_secret SERVER_IP       ip
