#!/usr/bin/env bash

set -euo pipefail
VA_DIRS_LIST=("$@")

SOURCE_DIR="$(dirname "$(realpath "$0")")"
GIT_ROOT="$(git rev-parse --show-toplevel)"
GIT_REPO_NAME="$(basename "$GIT_ROOT")"

if [ -z "$VA_DIRS_LIST" ]; then
  DIRS=($(find "$GIT_ROOT/cmd" -mindepth 1 -maxdepth 1 -type d))
else
  DIRS=()
  for dir in "${VA_DIRS_LIST[@]}"; do
    DIRS+=("$GIT_ROOT/cmd/$dir")
  done
fi

echo "Building images for the following directories:"
for dir in "${DIRS[@]}"; do
    app_name="$(basename "$dir")"
    echo "Building $GIT_REPO_NAME/$app_name from $dir"
    # Only wolf-agent plays the fake-udev role, so only it needs the binary
    # bundled into its image.
    build_args=(--build-arg "APP_NAME=$app_name")
    if [ "$app_name" = "wolf-agent" ]; then
      build_args+=(--build-arg "BUNDLE_FAKE_UDEV=true")
    fi
    "$SOURCE_DIR/build_and_push_multiarch.sh" \
        "$GIT_ROOT/Dockerfile" \
        "registry.zielenski.dev/$GIT_REPO_NAME/$app_name" \
        "${build_args[@]}"
done
