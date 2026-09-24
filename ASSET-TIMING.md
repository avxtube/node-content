# Asset diagnostics

Thumbnail, original poster/image, preview/video and generated `/thumb/*/*.jpg`
responses include `Server-Timing`. No request query parameters or source URLs
are added to the timing log.

- `lookup_cache`: reading the URL/delivery cache.
- `file_db`, `media_db`, `content_db`: StreamFile MongoDB queries on cache miss.
- `delivery_lookup`: generated poster delivery resolution, including DB reads.
- `cache_write`: persisting lookup metadata.
- `upstream_headers`: contacting the source until response headers arrive.
- `upstream_body`: reading the source image before transformation.
- `image_transform`: image decode, resize and WebP encode combined.
- `origin_headers`: elapsed origin time before sending headers.

`[asset-timing]` completion logs add `totalMs`, bytes, status and `stream_body`
for passthrough media. Streaming duration includes upstream reads and writes to
the downstream client; it is not pure origin processing time. Body transfer
timing cannot be placed in headers that have already been sent. Videos remain
streamed, including Range/206 responses.

By default, log requests taking at least one second, HTTP errors, or streaming
errors. Set `ASSET_TIMING=1` in the service environment and restart to log every
request temporarily. Remove it after diagnosis to reduce log volume.

Inspect `Server-Timing` in DevTools Network response headers. If Cloudflare says
`CF-Cache-Status: HIT`, timing and X-Lookup-Cache headers may be cached from the
original fill; they do not describe new origin work. Use a direct origin request
or a controlled CDN miss to diagnose origin latency. This feature requires
deploying the updated node-content binary, not only the web app.

## Connection diagnostics

Timing logs now include an `upstream` object: `dnsMs`, `tcpMs`, `tlsMs`,
`reused`, `idleMs`, `cfCache`, `age`, `cfRay`, `protocol`, and `status`.
DNS/TCP/TLS are also exposed in Server-Timing. Reused connections may have zero
connection timings. TCP time sums connection attempts (including failed or
parallel attempts), not necessarily wall-clock latency. Preview and original
poster passthrough retain streaming and Range behavior.

## Completed thumbnail cache

`thumb.webp` and `thumb-s.webp` cache successful transformed bytes for 24 hours
in `.thumbnail-cache` relative to the service working directory. Override with
`THUMB_CACHE_DIR=/path/writable/by/service`. Prefer a persistent directory outside
release directories. Existing lookup checks still run before this cache is used.

`X-Image-Cache` reports HIT, MISS, or SHARED (singleflight). Concurrent requests
for the same source URL and transformation share one fetch/encode. Leader
cancellation can fail that group; errors are not cached and later requests retry.
HEAD uses cached bytes without fetching/encoding again on a hit.

Disk storage uses 1,024 fixed hash slots, each at most 256 KiB including metadata:
at most 256 MiB of completed entries, plus bounded in-flight temporary files.
Slot collisions replace old entries; full-key checks prevent wrong-image hits.
Expired entries are misses and are overwritten as slots are reused, without
directory scans in request handlers. Failed writes do not prevent serving a
successfully generated image. A cache write failure is recorded in timing logs.

At most eight source loads/transforms run concurrently. Sources are limited to
20 MiB and 40 million pixels. Invalid/oversized sources or encode failures return
a non-cacheable error rather than caching the original image under a thumbnail
URL. Source 404 remains 404. Other upstream failures return 502. Only successful
WebP output is persisted. Generated `/thumb/*/*.jpg`, original posters and video
previews are not stored in this thumbnail cache.
