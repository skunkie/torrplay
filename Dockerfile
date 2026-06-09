# SPDX-FileCopyrightText: 2026 TorrPlay
#
# SPDX-License-Identifier: MIT

FROM alpine:3.23

ARG TARGETARCH

WORKDIR /app

RUN addgroup -g 1000 -S torrplay && \
    adduser -u 1000 -S -G torrplay torrplay && \
    chown -R torrplay:torrplay /app

COPY --chown=torrplay:torrplay --chmod=0755 \
    "./bin/torrplay-linux-${TARGETARCH}" ./torrplay

USER torrplay

EXPOSE 8090

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8090/api/system/health || exit 1

ENTRYPOINT ["/app/torrplay"]
CMD []
