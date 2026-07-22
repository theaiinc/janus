# `@theaiinc/janus`

JavaScript emitter and receiver client for Janus.

```sh
npm install @theaiinc/janus
```

The SDK supports direct endpoint discovery by default, Janus proxy mode, and
automatic fallback:

```js
const { Receiver } = require("@theaiinc/janus");

const receiver = new Receiver("http://127.0.0.1:8088");
const response = await receiver.receive("team", "llm", "stream");
```

HTTP response streams are returned without buffering. Configure the receiver
with `"proxy"` or `"auto"` as the third constructor argument when needed.
