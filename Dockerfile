FROM golang:1.24 AS builder
ARG CGO_ENABLED=0
ARG APP_NAME
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN go build -o /app/$APP_NAME ./cmd/$APP_NAME

# Build wolf's fake-udev static binary. Only the wolf-agent app uses it; build.sh
# sets BUNDLE_FAKE_UDEV=true for that target. When false the heavy toolchain
# install/clone/compile is skipped and a placeholder file is produced so the
# COPY in the output stage still resolves.
FROM alpine:3.21.3 AS fake-udev-builder
ARG BUNDLE_FAKE_UDEV=false
ARG WOLF_REF=stable
RUN mkdir /out \
 && if [ "$BUNDLE_FAKE_UDEV" = "true" ]; then \
      apk add --no-cache cmake ninja gcc g++ musl-dev linux-headers git && \
      git clone --depth 1 --branch ${WOLF_REF} https://github.com/games-on-whales/wolf.git /wolf && \
      # src/fake-udev/CMakeLists.txt is meant to be add_subdirectory()'d from
      # wolf's top-level project, which sets cmake_minimum_required(VERSION 3.13)
      # and so pins CMP0076 to NEW. Built standalone here, CMP0076 stays OLD and
      # the relative header path in target_sources(... PUBLIC ...) ends up in
      # INTERFACE_SOURCES, which CMake rejects. Set the policy explicitly.
      # Also: fake-udev.hpp uses htobe32() without #include <endian.h>; on glibc
      # (wolf's usual target) it's pulled in transitively, but not on musl/Alpine,
      # so force-include it here rather than patching upstream source.
      cmake -S /wolf/src/fake-udev -B /build -G Ninja -DCMAKE_BUILD_TYPE=Release \
            -DCMAKE_POLICY_DEFAULT_CMP0076=NEW \
            -DCMAKE_CXX_FLAGS="-include endian.h" && \
      cmake --build /build --target fake-udev && \
      cp /build/fake-udev /out/fake-udev ; \
    else \
      touch /out/fake-udev ; \
    fi

# Second stage: minimal runtime
FROM alpine:3.21.3 AS output
WORKDIR /app

# Alpine sh uses busybox which doesnt expand $@
# We need to use a shell for entrypoint in order to expand the envar for app
# Bash properly expands $@ to forwards args, so use that.
RUN apk add --no-cache bash

# Set the APP_NAME explicitly as an ENV var so it's available in runtime
ARG APP_NAME
ENV APP_NAME=${APP_NAME}

COPY --from=builder --chmod=755 /app/${APP_NAME} /app/${APP_NAME}

# Bundle wolf's fake-udev binary (only meaningful for wolf-agent).
ARG BUNDLE_FAKE_UDEV=false
COPY --from=fake-udev-builder /out/fake-udev /usr/local/bin/fake-udev
RUN if [ "$BUNDLE_FAKE_UDEV" != "true" ]; then rm -f /usr/local/bin/fake-udev; fi

ENTRYPOINT ["bash", "-c", "exec /app/${APP_NAME} \"$0\" \"$@\""]
CMD []
