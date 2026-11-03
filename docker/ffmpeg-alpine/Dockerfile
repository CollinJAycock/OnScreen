# syntax=docker/dockerfile:1

# Alpine's own ffmpeg packages, rebuilt with OnScreen's Dolby Vision patch
# (docker/patches/0005-dovi-rpu-convert-p7-to-p81.patch: `-bsf:v
# dovi_rpu=convert_p81=1`, profile 7 -> 8.1 on a remux).
#
# The standard image (docker/Dockerfile) installs these .apk files in place of
# Alpine's stock ffmpeg. Building from Alpine's APKBUILD, pinned to an aports
# commit, keeps every codec, hardware path and library the stock package has
# (VAAPI, QSV through libvpl, Vulkan, libplacebo, x264/x265, ...); the only
# change is the extra patch and the release number (-r100, so the package
# says it isn't Alpine's).
#
# Published as ghcr.io/collinjaycock/onscreen-ffmpeg-alpine by
# .github/workflows/ffmpeg-alpine.yml, natively per platform (an emulated
# arm64 ffmpeg build takes hours). The image holds only the .apk files.
#
# To move to a new Alpine ffmpeg: point APORTS_COMMIT at the aports commit
# that upgrades community/ffmpeg on the matching -stable branch, set
# FFMPEG_PKGVER, check the patch still applies, and bump the image tag in the
# workflow and in docker/Dockerfile.

ARG ALPINE_VERSION=3.24
FROM alpine:${ALPINE_VERSION} AS build

# community/ffmpeg: upgrade to 8.1.3 (3.24-stable, 2026-10-24).
ARG APORTS_COMMIT=66cae15cc19799ecaefa1ba9b131aeba0e5f7a86
ARG FFMPEG_PKGVER=8.1.3
ARG PKGREL=100

RUN apk add --no-cache alpine-sdk sudo \
 && adduser -D builder \
 && addgroup builder abuild \
 && echo 'builder ALL=(ALL) NOPASSWD: ALL' > /etc/sudoers.d/builder

USER builder
WORKDIR /home/builder/aports/ffmpeg

# The APKBUILD and Alpine's own patches, at the pinned commit. The ffmpeg
# tarball itself is fetched by abuild and checked against the APKBUILD's
# sha512sums.
RUN for f in APKBUILD add-av_stream_get_first_dts-for-chromium.patch posix-ioctl.patch; do \
      wget -q "https://gitlab.alpinelinux.org/alpine/aports/-/raw/${APORTS_COMMIT}/community/ffmpeg/$f"; \
    done \
 && grep -qx "pkgver=${FFMPEG_PKGVER}" APKBUILD

COPY --chown=builder docker/patches/0005-dovi-rpu-convert-p7-to-p81.patch onscreen-dovi-rpu-convert-p7-to-p81.patch

# Add the patch to source= (abuild's default prepare() applies every *.patch
# there with -p1), set the release number, and recompute the checksums of
# the sources (only the new patch's changes). Then build. -r installs the
# makedepends; the signing key only satisfies abuild, since the standard
# image installs these files with --allow-untrusted from this image.
RUN sed -i \
      -e "s/^pkgrel=.*/pkgrel=${PKGREL}/" \
      -e '/^[[:space:]]*posix-ioctl\.patch$/a onscreen-dovi-rpu-convert-p7-to-p81.patch' \
      APKBUILD \
 && grep -q onscreen-dovi-rpu-convert-p7-to-p81.patch APKBUILD \
 && abuild-keygen -a -i -n -q \
 && abuild checksum \
 && sudo apk update -q \
 && abuild -r \
 && mkdir -p /home/builder/out \
 && find /home/builder/packages -name '*.apk' \
      ! -name '*-dev-*' ! -name '*-doc-*' ! -name '*-dbg-*' ! -name 'ffplay-*' \
      -exec cp {} /home/builder/out/ \; \
 && ls -l /home/builder/out

# Proof the patch is in: the dovi_rpu filter lists the option the server
# probes for (internal/transcode/dolbyvision.go, ProbeDVConvert).
RUN sudo apk add --no-cache --allow-untrusted /home/builder/out/*.apk \
 && ffmpeg -hide_banner -h bsf=dovi_rpu | grep -q convert_p81 \
 && ffmpeg -hide_banner -version | head -1

FROM scratch
COPY --from=build /home/builder/out/ /apks/
