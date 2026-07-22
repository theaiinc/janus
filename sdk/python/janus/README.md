# `theaiinc-janus`

Janus Cloudflared tunnel guardian CLI.

```sh
pip install theaiinc-janus
janus validate-config --config janus.example.yaml
```

The wrapper downloads the matching Janus binary from GitHub Releases on first
use. Supported targets are Linux amd64/arm64, macOS amd64/arm64, and Windows
amd64. Set `JANUS_CACHE_DIR` to customize the cache location.
