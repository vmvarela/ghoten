#!/usr/bin/env bash
set -euo pipefail
: "${VERSION:?}" "${GOARCH:?}" "${GHOTEN_BIN:?}" "${PACKAGE_OUTPUT:?}"
BIN="$(realpath "$GHOTEN_BIN")"
OUTPUT="$(realpath "$PACKAGE_OUTPUT")"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
case "$GOARCH" in
  amd64) DEB_ARCH=amd64; RPM_ARCH=x86_64; APK_ARCH=x86_64; MACHINE='Advanced Micro Devices X86-64' ;;
  arm64) DEB_ARCH=arm64; RPM_ARCH=aarch64; APK_ARCH=aarch64; MACHINE=AArch64 ;;
  *) exit 1 ;;
esac
readelf -h "$BIN" | grep -F "$MACHINE"
for format in deb rpm apk; do
  files=("$OUTPUT"/*."$format")
  [[ ${#files[@]} -eq 1 && -f "${files[0]}" ]]
  package="${files[0]}"
  mkdir "$WORK/$format"
  case "$format" in
    deb)
      [[ "$(dpkg-deb -f "$package" Package)" == ghoten ]]
      [[ "$(dpkg-deb -f "$package" Version)" == "$VERSION" ]]
      [[ "$(dpkg-deb -f "$package" Architecture)" == "$DEB_ARCH" ]]
      dpkg-deb -x "$package" "$WORK/$format"
      LICENSE_PATH=usr/share/doc/ghoten/copyright
      ;;
    rpm)
      [[ "$(rpm -qp --qf '%{NAME} %{VERSION} %{ARCH}' "$package")" == "ghoten $VERSION $RPM_ARCH" ]]
      (cd "$WORK/$format"; rpm2cpio "$package" | cpio -idm --quiet --no-absolute-filenames)
      LICENSE_PATH=usr/share/licenses/ghoten/LICENSE
      ;;
    apk)
      tar --ignore-zeros -xzf "$package" -C "$WORK/$format"
      grep -Fx "pkgname = ghoten" "$WORK/$format/.PKGINFO"
      grep -Fx "pkgver = $VERSION" "$WORK/$format/.PKGINFO"
      grep -Fx "arch = $APK_ARCH" "$WORK/$format/.PKGINFO"
      LICENSE_PATH=usr/share/licenses/ghoten/LICENSE
      ;;
  esac
  cmp "$BIN" "$WORK/$format/usr/bin/ghoten"
  [[ "$(stat -c '%a' "$WORK/$format/usr/bin/ghoten")" == 755 ]]
  test -s "$WORK/$format/$LICENSE_PATH"
done
# Installation smoke tests run on the runner's native architecture only.
if [[ "$GOARCH" == amd64 ]]; then
  docker run --rm -v "$OUTPUT:/packages:ro" debian:bookworm-slim \
    sh -ec 'dpkg -i /packages/*.deb; ghoten version; dpkg -r ghoten'
  docker run --rm -v "$OUTPUT:/packages:ro" fedora:43 \
    sh -ec 'rpm -i /packages/*.rpm; ghoten version; rpm -e ghoten'
  docker run --rm -v "$OUTPUT:/packages:ro" alpine:3.22 \
    sh -ec 'apk add --allow-untrusted /packages/*.apk; ghoten version; apk del ghoten'
fi
