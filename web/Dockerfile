# syntax=docker/dockerfile:1
FROM node:22-alpine AS deps
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci

FROM node:22-alpine AS build
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY . .
# NEXT_PUBLIC_* values are inlined into the browser bundle at build time: this
# must be the URL the BROWSER uses to reach the API.
ARG NEXT_PUBLIC_NODERA_API_URL=http://localhost:8080
ENV NEXT_PUBLIC_NODERA_API_URL=$NEXT_PUBLIC_NODERA_API_URL NEXT_TELEMETRY_DISABLED=1
RUN npm run build

FROM node:22-alpine AS run
WORKDIR /app
ENV NODE_ENV=production NEXT_TELEMETRY_DISABLED=1 PORT=3000 HOSTNAME=0.0.0.0
RUN addgroup -S nodera && adduser -S -G nodera -u 10001 nodera
COPY --from=build --chown=nodera:nodera /app/.next/standalone ./
COPY --from=build --chown=nodera:nodera /app/.next/static ./.next/static
COPY --from=build --chown=nodera:nodera /app/public ./public
USER nodera
EXPOSE 3000
HEALTHCHECK --interval=15s --timeout=3s --start-period=20s --retries=5 CMD wget -qO- http://127.0.0.1:3000/ >/dev/null || exit 1
CMD ["node", "server.js"]
