# syntax=docker/dockerfile:1
FROM node:22-alpine AS deps
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci

FROM node:22-alpine AS build
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY . .
# NEXT_PUBLIC_* values are inlined into the browser bundle at build time. This is
# the URL the BROWSER uses to reach the API. Local development keeps the default;
# release images are built with an EMPTY value (same-origin: the reverse proxy
# routes /api/ and /agent/ to the API on the same host), which is what lets one
# immutable image run on any domain.
ARG NEXT_PUBLIC_NODERA_API_URL=
ENV NEXT_PUBLIC_NODERA_API_URL=$NEXT_PUBLIC_NODERA_API_URL NEXT_TELEMETRY_DISABLED=1
RUN npm run build

FROM node:22-alpine AS run
WORKDIR /app
ENV NODE_ENV=production NEXT_TELEMETRY_DISABLED=1 PORT=3000 HOSTNAME=0.0.0.0
RUN addgroup -S nodera && adduser -S -G nodera -u 10001 nodera \
 # The standalone server needs only node: drop the package managers the base
 # image ships and the TypeScript compiler Next's file tracing pulls in.
 && rm -rf /usr/local/lib/node_modules/npm /usr/local/bin/npm /usr/local/bin/npx /usr/local/bin/yarn /usr/local/bin/yarnpkg /opt/yarn*
COPY --from=build --chown=nodera:nodera /app/.next/standalone ./
COPY --from=build --chown=nodera:nodera /app/.next/static ./.next/static
COPY --from=build --chown=nodera:nodera /app/public ./public
RUN rm -rf node_modules/typescript
USER nodera
EXPOSE 3000
HEALTHCHECK --interval=15s --timeout=3s --start-period=20s --retries=5 CMD wget -qO- http://127.0.0.1:3000/ >/dev/null || exit 1
CMD ["node", "server.js"]
