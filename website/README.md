# Docusaurus Website

This directory contains the public documentation website for `harbor-relay`.

## Local preview

```bash
cd website
npm install
npm run start
```

## Production build

```bash
cd website
npm install
npm run build
```

The output will be generated in:

- `website/build/`

## Build a docs container image

```bash
docker build -t harbor-relay-docs:latest -f website/Dockerfile .
```

Run it locally:

```bash
docker run -d --name harbor-relay-docs -p 127.0.0.1:18081:8080 harbor-relay-docs:latest
```

Then point your external Caddy site to:

- `127.0.0.1:18081`
