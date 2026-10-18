#!/usr/bin/env bash
set -Eeuo pipefail
trap 'echo "::error::Package verification failed at line $LINENO: $BASH_COMMAND" >&2' ERR
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
      [[ "$(dpkg-deb -f "$package" Depends)" == ca-certificates ]]
      dpkg-deb -x "$package" "$WORK/$format"
      LICENSE_PATH=usr/share/doc/ghoten/copyright
      ;;
    rpm)
      [[ "$(rpm -qp --qf '%{NAME} %{VERSION} %{ARCH}' "$package")" == "ghoten $VERSION $RPM_ARCH" ]]
      rpm -qp --requires "$package" | grep -Fx ca-certificates
      rpm2cpio "$package" > "$WORK/payload.cpio"
      (cd "$WORK/$format"; cpio -idm --quiet --no-absolute-filenames < "$WORK/payload.cpio")
      LICENSE_PATH=usr/share/licenses/ghoten/LICENSE
      ;;
    apk)
      tar --ignore-zeros -xzf "$package" -C "$WORK/$format"
      grep -Fx "pkgname = ghoten" "$WORK/$format/.PKGINFO"
      grep -Fx "pkgver = $VERSION" "$WORK/$format/.PKGINFO"
      grep -Fx "arch = $APK_ARCH" "$WORK/$format/.PKGINFO"
      grep -Fx "depends = ca-certificates-bundle" "$WORK/$format/.PKGINFO"
      LICENSE_PATH=usr/share/licenses/ghoten/LICENSE
      ;;
  esac
  cmp "$BIN" "$WORK/$format/usr/bin/ghoten"
  [[ "$(stat -c '%a' "$WORK/$format/usr/bin/ghoten")" == 755 ]]
  test -s "$WORK/$format/$LICENSE_PATH"
done
# Installation smoke tests run on the runner's native architecture only.
# Package managers (apt/dnf/apk) resolve the CA-certificate dependency, and
# ghoten init then fetches a pinned provider over TLS using the system trust
# store, so a missing or broken CA setup fails the run.
if [[ "$GOARCH" == amd64 ]]; then
  mkdir "$WORK/init"
  cat > "$WORK/init/main.tf" <<'EOF'
terraform {
  required_providers {
    null = {
      source  = "registry.opentofu.org/hashicorp/null"
      version = "3.2.3"
    }
  }
}
EOF
  RUN_ARGS=(--rm -v "$OUTPUT:/packages:ro" -v "$WORK/init:/init" -w /init)
  docker run "${RUN_ARGS[@]}" debian:bookworm-slim \
    sh -ec 'apt-get update -qq && apt-get install -y /packages/*.deb &&
      ghoten version && ghoten init -backend=false -input=false && dpkg -r ghoten'
  docker run "${RUN_ARGS[@]}" fedora:43 \
    sh -ec 'dnf -y install /packages/*.rpm &&
      ghoten version && ghoten init -backend=false -input=false && rpm -e ghoten'
  docker run "${RUN_ARGS[@]}" alpine:3.22 \
    sh -ec 'apk add --allow-untrusted /packages/*.apk &&
      ghoten version && ghoten init -backend=false -input=false && apk del ghoten'
fi
