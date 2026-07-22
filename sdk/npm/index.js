class JanusError extends Error {
  constructor(status, message) {
    super(message);
    this.name = "JanusError";
    this.status = status;
  }
}

class Client {
  constructor(baseURL, fetchImpl = globalThis.fetch) {
    if (typeof fetchImpl !== "function") throw new TypeError("fetch is required");
    this.baseURL = baseURL.replace(/\/+$/, "");
    this.fetch = fetchImpl;
  }

  async request(method, path, body, headers = {}) {
    const response = await this.fetch(this.baseURL + path, {
      method,
      headers,
      body,
      redirect: "follow",
    });
    if (!response.ok) {
      throw new JanusError(response.status, await response.text());
    }
    return response;
  }

  async endpoint(namespace, alias) {
    const response = await this.request(
      "GET",
      `${this.aliasPath(namespace, alias)}/endpoint`,
    );
    return response.json();
  }

  requestEndpoint(endpoint, method, path, body, headers = {}) {
    const target = new URL(endpoint.url);
    const relative = new URL(path.replace(/^\/+/, ""), "http://janus.invalid/");
    target.pathname = `${target.pathname.replace(/\/+$/, "")}/${relative.pathname.replace(/^\/+/, "")}`;
    target.search = relative.search;
    return this.requestURL(target.toString(), method, body, headers);
  }

  async requestURL(url, method, body, headers = {}) {
    const response = await this.fetch(url, {
      method,
      headers,
      body,
      redirect: "follow",
    });
    if (!response.ok) {
      throw new JanusError(response.status, await response.text());
    }
    return response;
  }

  aliasPath(namespace, alias) {
    return `/api/namespaces/${encodeURIComponent(namespace)}/aliases/${encodeURIComponent(alias)}`;
  }

  dataPath(namespace, alias, path = "") {
    return `${this.aliasPath(namespace, alias)}/data/${path.replace(/^\/+/, "")}`;
  }
}

class Emitter {
  constructor(baseURL, fetchImpl, mode = "direct") {
    this.client = new Client(baseURL, fetchImpl);
    this.mode = mode;
  }

  async register(namespace, alias, registration = {}) {
    const response = await this.client.request(
      "PUT",
      this.client.aliasPath(namespace, alias),
      JSON.stringify({ ...registration, namespace, alias }),
      { "content-type": "application/json" },
    );
    return response.json();
  }

  send(namespace, alias, path, body, contentType = "application/octet-stream") {
    const headers = { "content-type": contentType };
    if (this.mode === "proxy") {
      return this.client.request("POST", this.client.dataPath(namespace, alias, path), body, headers);
    }
    return this.client.endpoint(namespace, alias)
      .then((endpoint) => this.client.requestEndpoint(endpoint, "POST", path, body, headers))
      .catch((error) => {
        if (this.mode !== "auto") throw error;
        return this.client.request("POST", this.client.dataPath(namespace, alias, path), body, headers);
      });
  }
}

class Receiver {
  constructor(baseURL, fetchImpl, mode = "direct") {
    this.client = new Client(baseURL, fetchImpl);
    this.mode = mode;
  }

  async resolve(namespace, alias) {
    const response = await this.client.request("GET", this.client.aliasPath(namespace, alias));
    return response.json();
  }

  resolveEndpoint(namespace, alias) {
    return this.client.endpoint(namespace, alias);
  }

  receive(namespace, alias, path = "") {
    return this.request("GET", namespace, alias, path);
  }

  request(method, namespace, alias, path, body, contentType = "application/octet-stream") {
    const headers = { "content-type": contentType };
    if (this.mode === "proxy") {
      return this.client.request(method, this.client.dataPath(namespace, alias, path), body, headers);
    }
    return this.client.endpoint(namespace, alias)
      .then((endpoint) => this.client.requestEndpoint(endpoint, method, path, body, headers))
      .catch((error) => {
        if (this.mode !== "auto") throw error;
        return this.client.request(method, this.client.dataPath(namespace, alias, path), body, headers);
      });
  }
}

module.exports = { Client, Emitter, Receiver, JanusError };
