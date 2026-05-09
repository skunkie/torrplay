# SPDX-FileCopyrightText: 2026 TorrPlay
#
# SPDX-License-Identifier: MIT

FROM node:24-trixie-slim

WORKDIR /build/client

RUN corepack prepare pnpm@latest --activate && corepack enable

COPY client/package.json .
COPY client/pnpm-lock.yaml .
RUN pnpm install --frozen-lockfile --ignore-scripts

COPY client-entrypoint.sh .
COPY client/ .

CMD [ "./client-entrypoint.sh" ]
