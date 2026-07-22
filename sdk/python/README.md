# `janus-client`

Python emitter and receiver client for Janus.

```sh
pip install janus-client
```

Direct endpoint discovery is enabled by default. Use `mode="proxy"` to keep
requests in Janus or `mode="auto"` to allow proxy fallback:

```python
from janus_client import Receiver

receiver = Receiver("http://127.0.0.1:8088")
response = receiver.receive("team", "llm", "stream")
```

Response bodies are returned as streams and are not buffered by the client.
