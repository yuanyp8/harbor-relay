FROM node:22-alpine AS builder
WORKDIR /app

COPY website/package.json ./package.json
COPY website/package-lock.json ./package-lock.json
RUN npm ci

COPY docs ../docs
COPY website .
RUN npm run build

FROM nginx:1.29-alpine
COPY website/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=builder /app/build /usr/share/nginx/html
EXPOSE 8080
