# Uploaded files: storage, URLs and serving

gotodon stores uploaded files on local disk. It serves them itself at
`/files/`, and every API, webfinger, compat, Web Push and ActivityPub response
links to them with absolute URLs. A reverse proxy, a CDN or an object storage
alias can take over serving: point `media.base_url` at it.

## Where files are stored

| Setting | Environment variable | Default |
|---|---|---|
| `media.storage_path` | `GOTODON_MEDIA_STORAGE_PATH` | `./uploads` |

gotodon creates directories below the storage path as it needs them. The
layout is:

| Files | Path below the storage root |
|---|---|
| Media attachments, avatars and headers | `media_attachments/files/<account id % 1000>/<account id>/<yyyy>/<mm>/<dd>/<random>.<ext>` |
| Media attachment thumbnails | the attachment's path with `_thumb` before the extension |
| Preview card images | `preview_cards/images/<hash>.<ext>` |
| Custom emojis | `custom_emojis/images/original/<random>.<ext>` |

A media attachment's date directory is its `created_at` in UTC. The upload
stores that same instant as `created_at`, so the path gotodon derives from the
record always points at the file. Attachments uploaded by earlier versions may
sit under the server's local date instead; the processing worker, the media
cleanup job and `DELETE /api/v1/media/:id` look there as well.

A file's extension comes from the content type gotodon detected in its bytes,
never from the name the client sent. A file named `photo.html` that holds a
PNG is stored as `<random>.png`.

## URLs

Every URL to a stored file is `<base URL>/<path below the storage root>`, with
each path segment percent-encoded. `media.FileURL` builds them; no other code
may (a test fails if a `/files` URL is put together by hand).

| Setting | Environment variable | Default |
|---|---|---|
| `media.base_url` | `GOTODON_MEDIA_BASE_URL` | `https://<web domain>/files` |

The web domain is `instance.web_domain`, or `instance.local_domain` when no
web domain is set. `gotodon serve` refuses to start without
`instance.local_domain`, so there is always a domain to build URLs on; no
code falls back to a placeholder one. `media.base_url` must be an absolute `http` or `https` URL
without credentials, query or fragment; gotodon refuses to start otherwise.
Trailing slashes are dropped.

Mastodon's settings translate as follows:

| Mastodon | gotodon |
|---|---|
| `CDN_HOST=https://cdn.example.com` | `media.base_url: https://cdn.example.com/files` |
| `S3_ALIAS_HOST=files.example.com` | `media.base_url: https://files.example.com` |

Values that are already absolute URLs stay as they are: avatars and headers of
remote accounts, remote media attachments and their thumbnails, remote custom
emojis (`image_remote_url`) and preview card images stored as URLs. Relative
remote values are not turned into local URLs; they are dropped.

Accounts without an avatar or header link to the default images, as Mastodon
does:

- `https://<web domain>/avatars/original/missing.png`, a neutral silhouette,
  400×400 pixels
- `https://<web domain>/headers/original/missing.png`, one transparent pixel

These are built into gotodon and always served by it, whatever
`media.base_url` says.

## Serving by gotodon

gotodon serves the storage root at `GET /files/<path>` and the default images
at their paths. They answer like the location Mastodon's `dist/nginx.conf`
serves uploads from:

- `GET` and `HEAD` only; other methods get `405` with `Allow: GET, HEAD`.
- Missing files, directories (no listings), paths outside the storage root,
  symbolic links leading out of it, dot-files and other non-regular files get
  `404`. Lookups go through `os.Root`, which confines them to the storage root.
- The `Content-Type` is detected from the file's first 512 bytes, never from
  its extension. Image, video and audio types are served as detected;
  anything else, SVG included, as `application/octet-stream`.
- Files are served with these headers. Error responses carry the last two.

  | Header | Value |
  |---|---|
  | `Cache-Control` | `public, max-age=2419200, immutable` |
  | `X-Content-Type-Options` | `nosniff` |
  | `Content-Security-Policy` | `default-src 'none'; form-action 'none'` |

- `Range`, `If-None-Match`, `If-Modified-Since` and `If-Range` work; files get
  an `ETag` and `Last-Modified`.
- Large files get a longer write budget; see
  [HTTP server limits](http-server-limits.md).

A CDN in front of gotodon can pull from `https://<web domain>/files/`. Set
`media.base_url` to the CDN's URL for those files.

## Serving by the deployment

A reverse proxy can serve the storage root itself and leave gotodon out of
file requests. Keep the proxy's behaviour as close to the list above as it
allows. For nginx, with the storage path `/var/lib/gotodon/uploads`:

```nginx
server {
  server_name social.example.com;
  # listen, TLS and the proxy_pass to gotodon for everything else ...

  location /files/ {
    alias /var/lib/gotodon/uploads/;

    # GET implies HEAD; everything else is refused.
    limit_except GET {
      deny all;
    }

    autoindex off;
    disable_symlinks on;

    # Type files by their extension, which gotodon derives from their bytes.
    # Anything else is a download.
    types {
      image/jpeg      jpg;
      image/png       png;
      image/gif       gif;
      image/webp      webp;
      video/mp4       mp4;
      video/webm      webm;
      video/quicktime mov;
      video/ogg       ogv;
      audio/mpeg      mp3;
      audio/ogg       ogg;
      audio/flac      flac;
      audio/wav       wav;
      audio/mp4       m4a;
      audio/aac       aac;
    }
    default_type application/octet-stream;

    add_header Cache-Control "public, max-age=2419200, immutable" always;
    add_header X-Content-Type-Options nosniff always;
    add_header Content-Security-Policy "default-src 'none'; form-action 'none'" always;

    location ~ /\. {
      return 404;
    }

    try_files $uri =404;
  }
}
```

The default images stay with gotodon: proxy `/avatars/` and `/headers/` to it
like every other route. With this configuration `media.base_url` keeps its
default. Serve the storage root from another host, such as
`files.example.com`, and set `media.base_url: https://files.example.com`.

The nginx user needs read access to the storage path, and gotodon needs write
access. nginx types files by extension, which is why gotodon names stored
files by their detected content type; the `types` block keeps anything else
from being served as a type a browser would render.
