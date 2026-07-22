import json
import urllib.error
import urllib.parse
import urllib.request


class JanusError(RuntimeError):
    def __init__(self, status, message):
        super().__init__(message)
        self.status = status


class Client:
    def __init__(self, base_url):
        self.base_url = base_url.rstrip("/")

    def request(self, method, path, body=None, content_type=None):
        return self.request_url(self.base_url + path, method, body, content_type)

    def request_url(self, url, method, body=None, content_type=None):
        data = body
        if isinstance(body, (dict, list)):
            data = json.dumps(body).encode()
        request = urllib.request.Request(
            url,
            data=data,
            method=method,
            headers={"Content-Type": content_type} if content_type else {},
        )
        try:
            return urllib.request.urlopen(request)
        except urllib.error.HTTPError as error:
            raise JanusError(error.code, error.read().decode()) from error

    def endpoint(self, namespace, alias):
        response = self.request("GET", self.alias_path(namespace, alias) + "/endpoint")
        return json.loads(response.read())

    def request_endpoint(self, endpoint, method, path="", body=None, content_type=None):
        relative = urllib.parse.urlsplit(path)
        target = endpoint["url"].rstrip("/") + "/" + relative.path.lstrip("/")
        if relative.query:
            target += "?" + relative.query
        return self.request_url(target, method, body, content_type)

    @staticmethod
    def _quote(value):
        return urllib.parse.quote(value, safe="")

    def alias_path(self, namespace, alias):
        return "/api/namespaces/{}/aliases/{}".format(
            self._quote(namespace), self._quote(alias)
        )

    def data_path(self, namespace, alias, path=""):
        return self.alias_path(namespace, alias) + "/data/" + path.lstrip("/")


class Emitter:
    def __init__(self, base_url, mode="direct"):
        self.client = Client(base_url)
        self.mode = mode

    def register(self, namespace, alias, registration=None):
        payload = dict(registration or {})
        payload.update(namespace=namespace, alias=alias)
        response = self.client.request(
            "PUT",
            self.client.alias_path(namespace, alias),
            payload,
            "application/json",
        )
        return json.loads(response.read())

    def send(self, namespace, alias, path="", body=b"", content_type="application/octet-stream"):
        if self.mode != "proxy":
            try:
                endpoint = self.client.endpoint(namespace, alias)
                return self.client.request_endpoint(endpoint, "POST", path, body, content_type)
            except (JanusError, urllib.error.URLError):
                if self.mode != "auto":
                    raise
        return self.client.request(
            "POST", self.client.data_path(namespace, alias, path), body, content_type
        )


class Receiver:
    def __init__(self, base_url, mode="direct"):
        self.client = Client(base_url)
        self.mode = mode

    def resolve(self, namespace, alias):
        response = self.client.request("GET", self.client.alias_path(namespace, alias))
        return json.loads(response.read())

    def resolve_endpoint(self, namespace, alias):
        return self.client.endpoint(namespace, alias)

    def receive(self, namespace, alias, path=""):
        return self.request("GET", namespace, alias, path)

    def request(self, method, namespace, alias, path="", body=None, content_type=None):
        if self.mode != "proxy":
            try:
                endpoint = self.client.endpoint(namespace, alias)
                return self.client.request_endpoint(endpoint, method, path, body, content_type)
            except (JanusError, urllib.error.URLError):
                if self.mode != "auto":
                    raise
        return self.client.request(
            method, self.client.data_path(namespace, alias, path), body, content_type
        )
