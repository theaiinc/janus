# `@theaiinc/janus`

Janus CLI package. It downloads the matching platform binary from the Janus
GitHub Release the first time the command runs.

```sh
npm install -g @theaiinc/janus
janus validate-config --config janus.example.yaml
```

Supported targets are Linux amd64/arm64, macOS amd64/arm64, and Windows
amd64. Set `JANUS_CACHE_DIR` to customize the binary cache location.
