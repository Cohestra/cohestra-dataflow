FROM node:22-slim AS build
WORKDIR /app
COPY package.json package-lock.json tsconfig.base.json ./
COPY packages ./packages
COPY apps/web ./apps/web
RUN npm install --workspaces --include-workspace-root
WORKDIR /app/apps/web
ARG VITE_PASSWORD_LOGIN_ENABLED=true
ARG VITE_GOOGLE_LOGIN_ENABLED=false
ENV VITE_PASSWORD_LOGIN_ENABLED=$VITE_PASSWORD_LOGIN_ENABLED
ENV VITE_GOOGLE_LOGIN_ENABLED=$VITE_GOOGLE_LOGIN_ENABLED
RUN npm run build

FROM nginx:1.27-alpine
# Pull patched OS package revisions (openssl/c-ares CVE fixes).
RUN apk -U upgrade --no-cache
COPY --from=build /app/apps/web/dist /usr/share/nginx/html
COPY apps/web/nginx.conf /etc/nginx/conf.d/default.conf
