# SPDX-FileCopyrightText: 2026 TorrPlay
#
# SPDX-License-Identifier: MIT

FROM node:24-trixie-slim

WORKDIR /build/client

RUN corepack prepare pnpm@latest --activate && corepack enable

COPY client/package.json .
COPY client/pnpm-lock.yaml .
COPY client/pnpm-workspace.yaml .
RUN pnpm install --frozen-lockfile

COPY client-entrypoint.sh client/ ./

CMD ["./client-entrypoint.sh"]
