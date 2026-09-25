# Wallhaven Control

A Go web control panel and live monitor for the Wallhaven downloader (port of `scrape-all.sh`).

- Presets (seeded from the shell script) with a full editor, search preview and reordering
- Queue, pause/resume, stop, re-run, run-all, custom one-off runs
- Live log over Server-Sent Events (filter, search, follow, download)
- Gallery with lightbox, library stats, schedules, run history
- Compatible with the script's `downloaded.txt` files, so existing libraries are not re-downloaded
- Wallhaven-style dark UI (edit the CSS variables at the top of `web/app.css`)

## Deploy with Docker Compose

```bash
cd /mnt/server/apps/projects/wallpaperhaven
cp .env.example .env        # adjust paths, PUID/PGID, optional login
docker compose pull
docker compose up -d
```

Open `http://<server>:8080`, go to **Settings**, paste your Wallhaven API key, press **Test key**.

The base download directory inside the container is `/wallpapers`, mapped to `WALLHAVEN_LIBRARY`.
If your old script stored files under `/wallpapers/Wallhaven`, mount that parent path and set the base directory
in Settings accordingly.

## Environment

| Variable | Purpose |
|---|---|
| `WH_API_KEY` | Seed the API key on first start |
| `WH_USER` / `WH_PASS` | Basic auth for the UI (recommended, it exposes your key settings) |
| `WH_BASE_DIR` | Default base directory (`/wallpapers` in the image) |
| `WH_DATA` | Config/history directory (`/data`) |
| `WH_LISTEN` | Listen address (`:8080`) |

## Run without Docker

```bash
go build -o wallhaven-control . && ./wallhaven-control -data ./data -listen :8080
```

## Publish to GHCR

Pushing to `main` or a `v*` tag runs `.github/workflows/docker.yml`, which publishes
`ghcr.io/net005/wallhaven` (amd64 + arm64).
