# node-content

HTTP delivery service for AVXTube. This repository keeps the route behavior from the original content-node and reads the current schemas in `platform/packages/db/src/models`.

## Data flow

`files.slug -> files._id -> medias.fileId -> medias.storageId -> storages.publicUrl -> node-storage`

The active media schema is `file-media.model.ts`: `fileId`, `storageId`, `type`, `quality`, `key`, `mime`, and `slug`. Object URLs sent to node-storage use the public File slug and the path inside the File key. For example, `key=file-id/subtitles/en.vtt` becomes `/{fileSlug}/subtitles/en.vtt`.

Files must be `ready` and must not have `metadata.trashedAt` or `metadata.deletedAt`. Storages must be enabled, online, not deleted, and have `publicUrl` configured. Both local and S3 providers are accessed through node-storage; node-content does not decrypt S3 credentials.

## Routes

| Route | Purpose |
| --- | --- |
| `/{fileSlug}/playlist.m3u8` | Build the master HLS playlist from video and audio Media rows |
| `/{mediaSlug}/video.m3u8` | Fetch and rewrite a video rendition playlist from node-storage |
| `/{mediaSlug}/audio.m3u8` | Fetch and rewrite an audio rendition playlist from node-storage |
| `/{mediaSlug}/subtitle.vtt` | Proxy a subtitle Media object |
| `/thumb/{fileSlug}/{second}.jpg` | Proxy an nginx-vod thumbnail; `poster.jpg` selects the midpoint |
| `/{fileSlug}/sprite/sprite.vtt` | Proxy the sprite VTT |
| `/{fileSlug}/sprite/sprite-{n}.jpg` | Proxy a sprite image |
| `/{posterFileSlug}/poster.{ext}` | Proxy the original poster image |
| `/{posterFileSlug}/thumb.webp` | Return a 330×168 WebP thumbnail of the poster |
| `/{previewFileSlug}/preview.{ext}` | Proxy the preview video with Range support |
| `/{shortFileSlug}/short.{ext}` | File kind stream → video Media matching mp4/webm/mov/m4v; GET/HEAD and Range supported |
| `/{fileSlug}.{ext}` | Legacy flat image route, with query-string resize support |
| `/playlist/{fileSlug}.json` | JW Player feed |
| `/vast/hobby.xml` | VAST from `settings.advert_hobby` |
| `/advert/hobby.json` | Advert feed from `settings.advert_hobby` |
| `/health` | Health status |

Only the current platform collections are queried: `files`, `medias`, `storages`, `settings`, and `video_process`. The service snapshots settings and safe storage delivery fields to `conf/setting.json` at startup and every minute. The platform's `domain_setting` value is exposed in that file as `custom_domain`; the old `custom_domains` and `workspaces` collections are not used.

## Configuration

```dotenv
DATABASE_URL=mongodb://127.0.0.1:27017/avxtube
PORT=8082
DOMAIN_STATIC=
# Optional. Leave empty to use the local .cached directory.
REDIS_URL=
```

`DATABASE_URL` must include the database name. The service always keeps a short in-memory cache. When `REDIS_URL` is empty or Redis cannot be reached at startup, cache entries are persisted in `.cached` for 5 minutes. Under systemd this directory is `/var/lib/node-content/.cached`; during local development it is created under the current working directory. Redis remains optional.

The `.cached` directory contains readable `{slug}.json` lookup files. Poster and preview slugs store their resolved proxy URL; stream slugs store the File and playlist Media metadata; video and audio Media slugs store the resolved m3u8 source URL, public domains, and Media key. Response bodies are kept only in memory (or Redis) and are never written to `.cached`. Image, preview video, and m3u8 bytes are fetched or generated normally. `DOMAIN_STATIC` is a fallback when the `domain_static` setting is empty. The hostname in each storage `publicUrl` must resolve from the node-content server and serve the node-storage/nginx endpoint.

Request handlers read storages from the in-memory snapshot and do not query the `storages` collection. The snapshot contains `_id`, name, provider, enabled/status flags, priority, purposes, kinds, `publicUrl`, `originUrl`, and timestamps. Encrypted S3 credentials and local filesystem configuration are excluded.

## Development

```bash
go test ./...
go run ./cmd
```

## Installation

```bash
curl -fsSL https://raw.githubusercontent.com/avxtube/node-content/main/install.sh | sudo -E bash -s -- \
  --database-url "mongodb://127.0.0.1:27017/avxtube" \
  --redis-url "redis://127.0.0.1:6379/0"
```

The installer creates `/opt/node-content`, the `node-content` systemd service, and an nginx virtual host. Use `--app` or `--nginx` to install one side only.

```bash
journalctl -u node-content -f
systemctl restart node-content
curl http://127.0.0.1:8082/health
```

Indexes remain owned by the platform Mongoose models; this service does not create or modify them.
