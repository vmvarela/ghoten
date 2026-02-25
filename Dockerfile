# Copyright (c) The OpenTofu Authors
# SPDX-License-Identifier: MPL-2.0
# Copyright (c) 2023 HashiCorp, Inc.
# SPDX-License-Identifier: MPL-2.0

FROM alpine:3.20

ARG TARGETARCH

LABEL maintainer="Ghoten"

RUN apk add --no-cache git bash openssh

COPY --from=binaries linux/${TARGETARCH}/ghoten /usr/local/bin/ghoten

ONBUILD RUN echo -e "\033[1;33mWARNING! PLEASE READ!\033[0m" >&2 \
            && echo -e "\033[1;33mPlease read carefully: you are using the Ghoten image as a base image\033[0m" >&2 \
            && echo -e "\033[1;33mfor your own builds. This is no longer supported.\033[0m" >&2 \
            && echo -e "\033[1;33mPlease build your own image instead.\033[0m" >&2 \
            && echo -e "\033[1;33mSee https://github.com/vmvarela/ghoten for details.\033[0m" >&2

ONBUILD RUN exit 1

ENTRYPOINT ["/usr/local/bin/ghoten"]
