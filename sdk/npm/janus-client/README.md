# `@theaiinc/janus-client`

JavaScript emitter and receiver client for Janus.

```sh
npm install @theaiinc/janus-client
```

Direct endpoint discovery is enabled by default. Use `"proxy"` or `"auto"` as
the optional transport mode when constructing an emitter or receiver.
`new Client(url).discover()` lists public services without an API key; private
namespace records still require a scoped credential.
